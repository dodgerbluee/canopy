package insights

import (
	"canopy/server/internal/agentcontext"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/aiconnection"
)

// RequestInput mirrors the frozen RequestAnalysisInput contract shape.
type RequestInput struct {
	Kind           string
	PlantID        string
	PhotoIDs       []string
	ReceiptAssetID string
	ForceRerun     bool
}

// Request gates and queues one analysis run. Everything (dedup, budget,
// insert, job enqueue) happens in one transaction serialized behind the
// per-workspace budget advisory lock, so concurrent requests can never
// both pass a budget check and both insert.
func (s *Service) Request(ctx context.Context, a Actor, in RequestInput, cmid *string) (dbgen.AnalysisRun, []Problem, error) {
	kind, ok := analysisKind(in.Kind)
	if !ok {
		return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.kind", "unknown analysis kind")}, nil
	}
	capName, ok := capabilityFor(kind)
	if !ok {
		return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.kind", "unknown analysis kind")}, nil
	}
	plantID, err := parseUUID(in.PlantID)
	plantGiven := err == nil
	if in.PlantID != "" && !plantGiven {
		return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.plantId", "plantId is not a valid id")}, nil
	}
	receiptID, err := parseUUID(in.ReceiptAssetID)
	receiptGiven := err == nil
	if in.ReceiptAssetID != "" && !receiptGiven {
		return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.receiptAssetId", "receiptAssetId is not a valid id")}, nil
	}
	// Target/kind matrix (SDL): plant kinds reject receiptAssetId and
	// vice versa; photoIds only for VISION (exactly two).
	switch kind {
	case dbgen.AnalysisKindRECEIPTEXTRACTION:
		if !receiptGiven {
			return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.receiptAssetId", "receiptAssetId is required for RECEIPT_EXTRACTION")}, nil
		}
		if plantGiven {
			return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.plantId", "plantId is not accepted for RECEIPT_EXTRACTION")}, nil
		}
		if len(in.PhotoIDs) > 0 {
			return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.photoIds", "photoIds are only accepted for VISION")}, nil
		}
	default:
		if !plantGiven {
			return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.plantId", "plantId is required for this kind")}, nil
		}
		if receiptGiven {
			return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.receiptAssetId", "receiptAssetId is only accepted for RECEIPT_EXTRACTION")}, nil
		}
		if kind == dbgen.AnalysisKindVISION {
			if len(in.PhotoIDs) != 2 {
				return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.photoIds", "VISION requires exactly two photo ids")}, nil
			}
		} else if len(in.PhotoIDs) > 0 {
			return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.photoIds", "photoIds are only accepted for VISION")}, nil
		}
	}

	return s.mutateRun(ctx, a, "requestAnalysis", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (dbgen.AnalysisRun, []Problem) {
			// Capability gate first: no provider, nothing queued.
			caps := s.Provider.Capabilities(aiconnection.WithWorkspace(ctx, a.WorkspaceID))
			st := caps[capName]
			if !st.Available {
				reason := st.Reason
				if reason == "" {
					reason = "this analysis capability is not configured"
				}
				return dbgen.AnalysisRun{}, []Problem{problem(CodeCapabilityUnavailable, "input.kind", reason)}
			}

			ws, err := q.GetWorkspaceByID(ctx, a.WorkspaceID)
			if err != nil {
				s.fail(ctx, "requestAnalysis: workspace", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "workspace unavailable")}
			}

			var plant dbgen.Plant
			if plantGiven {
				plant, err = q.GetPlant(ctx, dbgen.GetPlantParams{ID: plantID, WorkspaceID: a.WorkspaceID})
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return dbgen.AnalysisRun{}, []Problem{problem(CodeNotFound, "input.plantId", "plant not found")}
					}
					s.fail(ctx, "requestAnalysis: plant", err)
					return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "plant unavailable")}
				}
				// Archive/death disables new analyses (03); history stays readable.
				if plant.Status != dbgen.PlantStatusACTIVE {
					return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.plantId",
						"new analyses are disabled for archived or deceased plants")}
				}
			} else {
				// RECEIPT_EXTRACTION: target must be a READY receipt asset.
				if _, err := q.GetReadyReceiptForRun(ctx, dbgen.GetReadyReceiptForRunParams{
					ID: receiptID, WorkspaceID: a.WorkspaceID,
				}); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.receiptAssetId",
							"receipt asset not found or not ready")}
					}
					s.fail(ctx, "requestAnalysis: receipt", err)
					return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "receipt unavailable")}
				}
			}

			// VISION pair: live, READY, same plant (SDL comparison pair).
			var pair []SnapPhoto
			if kind == dbgen.AnalysisKindVISION {
				seen := map[string]bool{}
				for _, pid := range in.PhotoIDs {
					pu, err := parseUUID(pid)
					if err != nil {
						return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.photoIds", "photoIds must be valid ids")}
					}
					row, err := q.GetReadyPhotoForRun(ctx, dbgen.GetReadyPhotoForRunParams{ID: pu, WorkspaceID: a.WorkspaceID})
					if err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.photoIds",
								"photo not found, deleted, or its asset is not ready")}
						}
						s.fail(ctx, "requestAnalysis: photo", err)
						return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "photo unavailable")}
					}
					if row.PlantID != plantID || seen[uuidString(pu)] {
						return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.photoIds",
							"both photos must belong to the target plant and be distinct")}
					}
					seen[uuidString(pu)] = true
					pair = append(pair, SnapPhoto{ID: uuidString(pu), AssetID: uuidString(row.AssetID),
						Sha256: row.Sha256, TakenAt: tsText(row.TakenAt)})
				}
			}

			snap, err := BuildSnapshot(ctx, q, ws, plant, kind == dbgen.AnalysisKindSYNTHESIS)
			if err != nil {
				s.fail(ctx, "requestAnalysis: snapshot", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "snapshot build failed")}
			}
			snap.Kind = string(kind)
			if kind == dbgen.AnalysisKindVISION {
				snap.Photos = pair // explicit comparison pair replaces context photos
			}
			raw, hash, err := snap.Marshal()
			if err != nil {
				s.fail(ctx, "requestAnalysis: marshal", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "snapshot encode failed")}
			}
			skey := semanticKey(string(kind), targetKeyFor(kind, plantID, receiptID), hash)

			// Serialize dedup + budget + insert per workspace (race rule:
			// "maximum possible known plus encumbered spend cannot exceed
			// configured ceiling").
			if err := q.LockWorkspaceBudget(ctx, budgetLockID(a.WorkspaceID)); err != nil {
				s.fail(ctx, "requestAnalysis: lock", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "budget lock failed")}
			}

			// Semantic dedup: active duplicate always conflicts; same-hash
			// terminal reuse unless forceRerun.
			if existing, err := q.FindActiveRunBySemanticKey(ctx, dbgen.FindActiveRunBySemanticKeyParams{
				WorkspaceID: a.WorkspaceID, SemanticKey: skey,
			}); err == nil {
				return existing, []Problem{problem(CodeIdempotencyConflict, dupField(kind),
					"An analysis for this plant with unchanged input is already running; follow the existing run or force a rerun.")}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				s.fail(ctx, "requestAnalysis: dedup", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "dedup check failed")}
			}
			if !in.ForceRerun {
				if prior, err := q.FindPriorRunBySemanticKey(ctx, dbgen.FindPriorRunBySemanticKeyParams{
					WorkspaceID: a.WorkspaceID, SemanticKey: skey,
				}); err == nil {
					return prior, []Problem{problem(CodeIdempotencyConflict, dupField(kind),
						"An analysis for this plant with unchanged input already exists; force a rerun to re-bill.")}
				} else if !errors.Is(err, pgx.ErrNoRows) {
					s.fail(ctx, "requestAnalysis: prior", err)
					return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "dedup check failed")}
				}
			}

			// Daily budget (workspace-local day).
			used, err := q.CountRunsSince(ctx, dbgen.CountRunsSinceParams{
				WorkspaceID: a.WorkspaceID,
				RequestedAt: pgtype.Timestamptz{Time: localDayStart(ws.Timezone, s.now()), Valid: true},
			})
			if err != nil {
				s.fail(ctx, "requestAnalysis: daily", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "budget check failed")}
			}
			if used >= s.Limit.DailyRunLimit {
				return dbgen.AnalysisRun{}, []Problem{problem(CodeRateLimited, "input.kind",
					fmt.Sprintf("Daily analysis limit reached (%d of %d runs used today); try again tomorrow.", used, s.Limit.DailyRunLimit))}
			}

			// Concurrency: max one live run per provider capability.
			live, err := q.CountConcurrentRunsPerCapability(ctx, a.WorkspaceID)
			if err != nil {
				s.fail(ctx, "requestAnalysis: concurrency", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "budget check failed")}
			}
			for _, c := range live {
				if c.Capability == string(capName) && c.LiveCount >= s.Limit.ConcurrentRunLimit {
					return dbgen.AnalysisRun{}, []Problem{problem(CodeRateLimited, "input.kind",
						"Only one analysis per provider can run at a time; wait for the current run to finish.")}
				}
			}

			// Monthly ceiling: encumbered + spent + this attempt's max cost.
			if s.Limit.MonthCeilingMicroUSD > 0 {
				mStart, mEnd := utcMonth(s.now())
				led, err := q.BudgetMonthLedger(ctx, dbgen.BudgetMonthLedgerParams{
					WorkspaceID:   a.WorkspaceID,
					RequestedAt:   pgtype.Timestamptz{Time: mStart, Valid: true},
					RequestedAt_2: pgtype.Timestamptz{Time: mEnd, Valid: true},
				})
				if err != nil {
					s.fail(ctx, "requestAnalysis: ledger", err)
					return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "budget check failed")}
				}
				if led.EncumberedMicroUsd+led.SpentMicroUsd+s.Limit.AttemptCostMicroUSD > s.Limit.MonthCeilingMicroUSD {
					return dbgen.AnalysisRun{}, []Problem{problem(CodeRateLimited, "input.kind",
						"Monthly external-cost ceiling reached; no further analyses this month.")}
				}
			}

			runID := s.uuid.New()
			var puA, puB pgtype.UUID
			if kind == dbgen.AnalysisKindVISION && len(pair) == 2 {
				puA, puB = pgUUID(pair[0].AssetID), pgUUID(pair[1].AssetID)
			}
			run, err := q.InsertAnalysisRun(ctx, dbgen.InsertAnalysisRunParams{
				ID: runID, WorkspaceID: a.WorkspaceID, Kind: kind, Capability: string(capName),
				PlantID: plantID, ReceiptAssetID: receiptID, PhotoIDA: puA, PhotoIDB: puB,
				InputSnapshot: raw, InputHash: hash, SemanticKey: skey,
				CostReservedMicroUsd: pgtype.Int8{Int64: s.Limit.AttemptCostMicroUSD, Valid: true},
			})
			if err != nil {
				// The partial unique index is the authority under races.
				if isUniqueViolation(err) {
					if existing, err2 := q.FindActiveRunBySemanticKey(ctx, dbgen.FindActiveRunBySemanticKeyParams{
						WorkspaceID: a.WorkspaceID, SemanticKey: skey,
					}); err2 == nil {
						return existing, []Problem{problem(CodeIdempotencyConflict, dupField(kind),
							"An analysis for this plant with unchanged input is already running; follow the existing run or force a rerun.")}
					}
				}
				s.fail(ctx, "requestAnalysis: insert", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "run insert failed")}
			}
			if err := s.riverInsertTx(ctx, tx, runID); err != nil {
				s.fail(ctx, "requestAnalysis: enqueue", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "job enqueue failed")}
			}
			return run, nil
		})
}

// Cancel applies the optimistic revision guard. A cancel before any
// dispatch refunds the reservation (proven-undispatched); a cancel after
// dispatch keeps the encumbrance until reconciliation (SDL payload note).
func (s *Service) Cancel(ctx context.Context, a Actor, runID string, expectedRevision int32, cmid *string) (dbgen.AnalysisRun, []Problem, error) {
	id, err := parseUUID(runID)
	if err != nil {
		return dbgen.AnalysisRun{}, []Problem{problem(CodeValidation, "input.runId", "runId is not a valid id")}, nil
	}
	return s.mutateRun(ctx, a, "cancelAnalysis", cmid,
		map[string]any{"runId": runID, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (dbgen.AnalysisRun, []Problem) {
			row, err := q.CancelAnalysisRun(ctx, dbgen.CancelAnalysisRunParams{
				ID: id, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				cur, err := q.GetAnalysisRun(ctx, dbgen.GetAnalysisRunParams{ID: id, WorkspaceID: a.WorkspaceID})
				if errors.Is(err, pgx.ErrNoRows) {
					return dbgen.AnalysisRun{}, []Problem{problem(CodeNotFound, "input.runId", "run not found")}
				}
				if err != nil {
					s.fail(ctx, "cancelAnalysis: get", err)
					return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "run read failed")}
				}
				return cur, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "This run already finished; it cannot be cancelled."), cur.Revision)}
			}
			if err != nil {
				s.fail(ctx, "cancelAnalysis: cancel", err)
				return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "cancel failed")}
			}
			if row.Attempts == 0 {
				// Refund: the reserved attempt was provably never dispatched.
				if _, err := q.SettleAnalysisRunCost(ctx, dbgen.SettleAnalysisRunCostParams{
					ID: id, CostActualMicroUsd: pgtype.Int8{Int64: 0, Valid: true},
				}); err != nil {
					s.fail(ctx, "cancelAnalysis: refund", err)
					return dbgen.AnalysisRun{}, []Problem{problem(CodeConflict, "", "refund failed")}
				}
				row.CostActualMicroUsd = pgtype.Int8{Int64: 0, Valid: true}
			}
			return row, nil
		})
}

// GetRun is the polling read: nil when absent or cross-workspace.
func (s *Service) GetRun(ctx context.Context, ws pgtype.UUID, runID string) (*dbgen.AnalysisRun, error) {
	id, err := parseUUID(runID)
	if err != nil {
		return nil, nil
	}
	row, err := s.q().GetAnalysisRun(ctx, dbgen.GetAnalysisRunParams{ID: id, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// BudgetView is the settings-surface budget picture (month figures belong
// to the UTC calendar month of "now"; reservations never migrate).
type BudgetView struct {
	DailyRunsUsed           int32
	DailyRunLimit           int32
	ConcurrentRunLimit      int32
	MonthUTC                string
	MonthCeilingMicroUSD    int64 // 0 = no monetary ceiling
	MonthEncumberedMicroUSD int64
	MonthSpentMicroUSD      int64
}

// Capabilities answers the settings query: provider availability per
// kind plus the live budget envelope. Never exposes credentials.
func (s *Service) Capabilities(ctx context.Context, ws pgtype.UUID) (map[Capability]CapabilityStatus, BudgetView, error) {
	view := BudgetView{
		DailyRunLimit:        s.Limit.DailyRunLimit,
		ConcurrentRunLimit:   s.Limit.ConcurrentRunLimit,
		MonthCeilingMicroUSD: s.Limit.MonthCeilingMicroUSD,
	}
	zone := "UTC"
	if w, err := s.q().GetWorkspaceByID(ctx, ws); err == nil {
		zone = w.Timezone
	}
	now := s.now()
	view.MonthUTC = now.UTC().Format("2006-01")
	used, err := s.q().CountRunsSince(ctx, dbgen.CountRunsSinceParams{
		WorkspaceID: ws, RequestedAt: pgtype.Timestamptz{Time: localDayStart(zone, now), Valid: true},
	})
	if err != nil {
		return nil, view, err
	}
	view.DailyRunsUsed = used
	mStart, mEnd := utcMonth(now)
	led, err := s.q().BudgetMonthLedger(ctx, dbgen.BudgetMonthLedgerParams{
		WorkspaceID:   ws,
		RequestedAt:   pgtype.Timestamptz{Time: mStart, Valid: true},
		RequestedAt_2: pgtype.Timestamptz{Time: mEnd, Valid: true},
	})
	if err != nil {
		return nil, view, err
	}
	view.MonthEncumberedMicroUSD = led.EncumberedMicroUsd
	view.MonthSpentMicroUSD = led.SpentMicroUsd
	return s.Provider.Capabilities(aiconnection.WithWorkspace(ctx, ws)), view, nil
}

// mutateRun is the insights-flavored receipt protocol wrapper: unlike
// care's mutate it can return a payload row alongside problems (the
// IDEMPOTENCY_CONFLICT payload carries the existing run).
func (s *Service) mutateRun(ctx context.Context, a Actor, op string, cmid *string, input any,
	fn func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (dbgen.AnalysisRun, []Problem),
) (dbgen.AnalysisRun, []Problem, error) {
	var zero dbgen.AnalysisRun
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	if err := agentcontext.Enter(ctx, tx); err != nil {
		return zero, nil, err
	}

	key := ""
	if cmid != nil {
		key = strings.TrimSpace(*cmid)
	}
	if key != "" {
		lockID := uuidString(a.WorkspaceID) + "|" + op + "|" + key
		if err := q.LockMutationKey(ctx, lockID); err != nil {
			return zero, nil, err
		}
		rec, err := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{
			WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key,
		})
		switch {
		case err == nil:
			if rec.RequestHash != requestHash(input) {
				return zero, []Problem{problem(CodeIdempotencyConflict, "clientMutationId",
					"clientMutationId was already used for a different request.")}, nil
			}
			var out dbgen.AnalysisRun
			if err := json.Unmarshal(rec.Result, &out); err != nil {
				return zero, nil, fmt.Errorf("insights: receipt decode: %w", err)
			}
			return out, nil, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return zero, nil, err
		}
	}

	out, probs := fn(ctx, q, tx)
	if len(probs) > 0 {
		// Problem paths persist nothing (rollback via defer).
		return out, probs, nil
	}
	if key != "" {
		raw, err := json.Marshal(out)
		if err != nil {
			return zero, nil, err
		}
		if _, err := q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{
			ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key,
			RequestHash: requestHash(input), Result: raw,
			ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(ReceiptRetention), Valid: true},
		}); err != nil {
			return zero, nil, err
		}
	}
	if err := agentcontext.Complete(ctx, tx, out); err != nil {
		return zero, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, nil, err
	}
	return out, nil, nil
}

func (s *Service) fail(ctx context.Context, msg string, err error) {
	if s.Log != nil {
		s.Log.Error("insights: "+msg, "err", err)
	}
}

// dupField names the target field for the semantic-duplicate problem
// (fixture: input.plantId / input.receiptAssetId).
func dupField(k dbgen.AnalysisKind) string {
	if k == dbgen.AnalysisKindRECEIPTEXTRACTION {
		return "input.receiptAssetId"
	}
	return "input.plantId"
}

func analysisKind(s string) (dbgen.AnalysisKind, bool) {
	k := dbgen.AnalysisKind(s)
	switch k {
	case dbgen.AnalysisKindTEXT, dbgen.AnalysisKindVISION, dbgen.AnalysisKindIDENTIFICATION,
		dbgen.AnalysisKindRECEIPTEXTRACTION, dbgen.AnalysisKindSYNTHESIS:
		return k, true
	}
	return "", false
}

func capabilityFor(k dbgen.AnalysisKind) (Capability, bool) {
	switch k {
	case dbgen.AnalysisKindTEXT, dbgen.AnalysisKindSYNTHESIS:
		return CapTextAnalyzer, true
	case dbgen.AnalysisKindVISION:
		return CapVisionAnalyzer, true
	case dbgen.AnalysisKindIDENTIFICATION:
		return CapPlantIdentifier, true
	case dbgen.AnalysisKindRECEIPTEXTRACTION:
		return CapReceiptExtractor, true
	}
	return "", false
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func pgUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}
