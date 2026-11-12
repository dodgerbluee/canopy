package collection

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media"
	"canopy/server/internal/money"
	"canopy/server/internal/portability"
	"canopy/server/internal/purchases"
)

// ContextInput mirrors PlantContextInput from the contract.
type ContextInput struct {
	EffectiveFrom   *time.Time
	LocationID      *string
	Placement       string // CONTAINER | GROUND
	PotVolumeLiters *pgtype.Numeric
	MediumNote      *string
	LightNote       *string
}

// AcquisitionInput mirrors AcquisitionInput from the contract.
type AcquisitionInput struct {
	Seller       *string
	AcquiredOn   *time.Time
	PricingState string // UNKNOWN | KNOWN
}

// InitialPurchaseInput mirrors InitialPurchaseInput with amounts already
// parsed to minor ints by the transport. When set, createPlant atomically
// creates a single-line purchase fully allocated to the new plant and
// prices the acquisition KNOWN against it.
type InitialPurchaseInput struct {
	Currency    string
	Total       int64
	Tax         int64
	Shipping    int64
	Seller      *string
	PurchasedOn *time.Time
}

// CreatePlantInput mirrors CreatePlantInput from the contract.
type CreatePlantInput struct {
	Name              string
	ScientificName    *string
	Cultivar          *string
	Rootstock         *string
	Type              string
	Count             *int32
	Category          string
	PlantedOn         *time.Time
	EstimatedAgeYears *pgtype.Numeric
	TagIDs            []string
	InitialContext    *ContextInput
	Acquisition       *AcquisitionInput
	InitialPurchase   *InitialPurchaseInput
}

// UpdatePlantInput mirrors UpdatePlantInput. acquiredOn is NOT editable:
// it is the acquisition projection.
type UpdatePlantInput struct {
	ID                string
	ExpectedRevision  int32
	Name              *string
	ScientificName    *string
	Cultivar          *string
	Rootstock         *string
	Type              *string
	Count             *int32
	Category          *string
	PlantedOn         *time.Time
	EstimatedAgeYears *pgtype.Numeric
}

// PlantResult identifies the affected plant aggregate.
type PlantResult struct {
	ID       string `json:"id"`
	Revision int32  `json:"revision"`
}

// ContextResult identifies a created context plus the bumped plant.
type ContextResult struct {
	PlantID   string    `json:"plantId"`
	ContextID string    `json:"contextId"`
	Revision  int32     `json:"revision"`
	Effective time.Time `json:"effective"`
}

// PurgeResult identifies the removed plant.
type PurgeResult struct {
	PlantID string `json:"plantId"`
}

// CreatePlant inserts plant + optional context/tags/acquisition(unknown)
// atomically. Unknowns stay unknown: nulls are stored as nulls.
func (s *Service) CreatePlant(ctx context.Context, a Actor, in CreatePlantInput, cmid *string) (PlantResult, []Problem, error) {
	return mutate[PlantResult](s, ctx, a, "createPlant", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (PlantResult, []Problem) {
			var problems []Problem
			name := strings.TrimSpace(in.Name)
			if name == "" || len(name) > 160 {
				problems = append(problems, problem(CodeValidation, "input.name", "name must be 1-160 characters"))
			}
			if in.Count != nil && *in.Count <= 0 {
				problems = append(problems, problem(CodeValidation, "input.count", "count must be positive when known"))
			}
			if in.EstimatedAgeYears != nil && numericSign(in.EstimatedAgeYears) < 0 {
				problems = append(problems, problem(CodeValidation, "input.estimatedAgeYears", "estimatedAgeYears must not be negative"))
			}
			if in.InitialPurchase != nil {
				if in.Acquisition == nil {
					problems = append(problems, problem(CodeValidation, "input.initialPurchase",
						"initialPurchase requires an acquisition record"))
				} else if in.Acquisition.PricingState != "KNOWN" {
					problems = append(problems, problem(CodeValidation, "input.acquisition.pricingState",
						"initialPurchase requires pricingState KNOWN"))
				}
			} else if in.Acquisition != nil && in.Acquisition.PricingState == "KNOWN" {
				problems = append(problems, problem(CodeValidation, "input.acquisition.pricingState",
					"KNOWN pricing requires initialPurchase"))
			}
			tagIDs, p := parseTagIDs(ctx, q, a, in.TagIDs, "input.tagIds")
			if p != nil {
				problems = append(problems, *p)
			}
			var ctxProblems []Problem
			if in.InitialContext != nil {
				ctxProblems = validateContext(ctx, q, a, *in.InitialContext)
				problems = append(problems, ctxProblems...)
			}
			if len(problems) > 0 {
				return PlantResult{}, problems
			}

			acquiredOn := pgtype.Date{}
			if in.Acquisition != nil && in.Acquisition.AcquiredOn != nil {
				acquiredOn = pgtype.Date{Time: *in.Acquisition.AcquiredOn, Valid: true}
			}
			plant, err := q.InsertPlant(ctx, dbgen.InsertPlantParams{
				ID:                s.uuid.New(),
				WorkspaceID:       a.WorkspaceID,
				Name:              name,
				ScientificName:    textPtr(in.ScientificName),
				Cultivar:          textPtr(in.Cultivar),
				Rootstock:         textPtr(in.Rootstock),
				Type:              dbgen.PlantType(in.Type),
				Count:             int4Ptr(in.Count),
				Category:          dbgen.PlantCategory(in.Category),
				AcquiredOn:        acquiredOn,
				PlantedOn:         datePtr(in.PlantedOn),
				EstimatedAgeYears: in.EstimatedAgeYears,
			})
			if err != nil {
				s.Log.Error("createPlant insert", "err", err)
				return PlantResult{}, []Problem{problem(CodeValidation, "input", "plant could not be stored")}
			}
			if in.InitialContext != nil {
				if _, err := s.insertContext(ctx, q, a, plant.ID, *in.InitialContext); err != nil {
					s.Log.Error("createPlant context", "err", err)
					return PlantResult{}, []Problem{problem(CodeValidation, "input.initialContext", "context could not be stored")}
				}
			}
			for _, tag := range tagIDs {
				if err := q.InsertPlantTag(ctx, dbgen.InsertPlantTagParams{
					WorkspaceID: a.WorkspaceID, PlantID: plant.ID, TagID: tag,
				}); err != nil {
					s.Log.Error("createPlant tag", "err", err)
					return PlantResult{}, []Problem{problem(CodeValidation, "input.tagIds", "tag link could not be stored")}
				}
			}
			if in.Acquisition != nil {
				state := dbgen.PricingStateUNKNOWN
				var allocID pgtype.UUID
				if in.InitialPurchase != nil {
					ip := in.InitialPurchase
					if ip.Currency == "" || !money.ValidCurrency(ip.Currency) {
						return PlantResult{}, []Problem{problem(CodeValidation, "input.initialPurchase.currency",
							"currency must be an ISO-4217 three-letter code")}
					}
					if ip.Tax+ip.Shipping > ip.Total {
						return PlantResult{}, []Problem{problem(CodeValidation, "input.initialPurchase.taxMinor",
							"tax + shipping exceeds total")}
					}
					prow, _, alloc, err := purchases.CreateAtomic(ctx, q, a.WorkspaceID, purchases.AtomicSpec{
						Currency: ip.Currency, Total: ip.Total, Tax: ip.Tax, Shipping: ip.Shipping,
						Seller: ip.Seller, PurchasedOn: ip.PurchasedOn, LineDesc: name, PlantID: plant.ID,
					})
					if err != nil {
						s.Log.Error("createPlant initialPurchase", "err", err)
						return PlantResult{}, []Problem{problem(CodeValidation, "input.initialPurchase",
							"purchase could not be stored")}
					}
					allocID = alloc.ID
					state = dbgen.PricingStateKNOWN
					if err := s.audit(ctx, q, a, "createPlant.initialPurchase", "purchase", prow.ID, prow.Revision, nil); err != nil {
						return PlantResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
					}
				}
				if _, err := q.InsertAcquisition(ctx, dbgen.InsertAcquisitionParams{
					ID:                          s.uuid.New(),
					WorkspaceID:                 a.WorkspaceID,
					PlantID:                     plant.ID,
					Seller:                      textPtr(in.Acquisition.Seller),
					AcquiredOn:                  acquiredOn,
					PricingState:                state,
					InitialPurchaseAllocationID: allocID,
				}); err != nil {
					s.Log.Error("createPlant acquisition", "err", err)
					return PlantResult{}, []Problem{problem(CodeValidation, "input.acquisition", "acquisition could not be stored")}
				}
			}
			if err := s.audit(ctx, q, a, "createPlant", "plant", plant.ID, plant.Revision, nil); err != nil {
				return PlantResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return PlantResult{ID: uuidString(plant.ID), Revision: plant.Revision}, nil
		})
}

// UpdatePlant applies a guarded revision bump; stale writes answer CONFLICT
// with the current revision and never a partial write.
func (s *Service) UpdatePlant(ctx context.Context, a Actor, in UpdatePlantInput, cmid *string) (PlantResult, []Problem, error) {
	return mutate[PlantResult](s, ctx, a, "updatePlant", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (PlantResult, []Problem) {
			id, ok := parseUUID(in.ID)
			if !ok {
				return PlantResult{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}
			}
			plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return PlantResult{}, notFound("input.id", "plant not found")
			}
			var problems []Problem
			if in.Name != nil {
				name := strings.TrimSpace(*in.Name)
				if name == "" || len(name) > 160 {
					problems = append(problems, problem(CodeValidation, "input.name", "name must be 1-160 characters"))
				} else {
					in.Name = &name
				}
			}
			if in.Count != nil && *in.Count <= 0 {
				problems = append(problems, problem(CodeValidation, "input.count", "count must be positive when known"))
			}
			if in.EstimatedAgeYears != nil && numericSign(in.EstimatedAgeYears) < 0 {
				problems = append(problems, problem(CodeValidation, "input.estimatedAgeYears", "estimatedAgeYears must not be negative"))
			}
			if len(problems) > 0 {
				return PlantResult{}, problems
			}
			params := dbgen.UpdatePlantParams{
				ID: id, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				Name:              textPtr(in.Name),
				ScientificName:    textPtr(in.ScientificName),
				Cultivar:          textPtr(in.Cultivar),
				Rootstock:         textPtr(in.Rootstock),
				Count:             int4Ptr(in.Count),
				PlantedOn:         datePtr(in.PlantedOn),
				EstimatedAgeYears: in.EstimatedAgeYears,
			}
			if in.Type != nil {
				params.Type = dbgen.NullPlantType{PlantType: dbgen.PlantType(*in.Type), Valid: true}
			}
			if in.Category != nil {
				params.Category = dbgen.NullPlantCategory{PlantCategory: dbgen.PlantCategory(*in.Category), Valid: true}
			}
			updated, err := q.UpdatePlant(ctx, params)
			if err != nil {
				return PlantResult{}, s.conflictProblem(ctx, q, "plants", id, a.WorkspaceID, err)
			}
			if err := s.audit(ctx, q, a, "updatePlant", "plant", id, updated.Revision, plant); err != nil {
				return PlantResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return PlantResult{ID: uuidString(id), Revision: updated.Revision}, nil
		})
}

// ArchivePlant marks ACTIVE/DEAD plants ARCHIVED; read history (contexts,
// audit, acquisition) stays exactly as it was.
func (s *Service) ArchivePlant(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (PlantResult, []Problem, error) {
	return s.setStatus(ctx, a, "archivePlant", id, expectedRevision, dbgen.PlantStatusARCHIVED, cmid)
}

// RestorePlant returns an ARCHIVED plant to ACTIVE.
func (s *Service) RestorePlant(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (PlantResult, []Problem, error) {
	return s.setStatus(ctx, a, "restorePlant", id, expectedRevision, dbgen.PlantStatusACTIVE, cmid)
}

func (s *Service) setStatus(ctx context.Context, a Actor, op, id string, expectedRevision int32, to dbgen.PlantStatus, cmid *string) (PlantResult, []Problem, error) {
	return mutate[PlantResult](s, ctx, a, op, cmid, map[string]any{"id": id, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries) (PlantResult, []Problem) {
			uid, ok := parseUUID(id)
			if !ok {
				return PlantResult{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}
			}
			plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: uid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return PlantResult{}, notFound("input.id", "plant not found")
			}
			if op == "archivePlant" && plant.Status == dbgen.PlantStatusARCHIVED {
				return PlantResult{}, []Problem{problem(CodeConflict, "input.id", "plant is already archived")}
			}
			if op == "restorePlant" && plant.Status != dbgen.PlantStatusARCHIVED {
				return PlantResult{}, []Problem{problem(CodeConflict, "input.id", "plant is not archived")}
			}
			updated, err := q.SetPlantStatus(ctx, dbgen.SetPlantStatusParams{
				ID: uid, Status: to, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			})
			if err != nil {
				return PlantResult{}, s.conflictProblem(ctx, q, "plants", uid, a.WorkspaceID, err)
			}
			if err := s.audit(ctx, q, a, op, "plant", uid, updated.Revision, plant); err != nil {
				return PlantResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			// Care lifecycle integration (T24), in this transaction:
			// archive cancels open work, restore regenerates it. A hook
			// failure rolls the status change back with it.
			if to == dbgen.PlantStatusARCHIVED {
				if err := s.Hooks.PlantArchived(ctx, q, a.WorkspaceID, uid); err != nil {
					if s.Log != nil {
						s.Log.Error("care hook: PlantArchived", "plant", uuidString(uid), "err", err)
					}
					return PlantResult{}, []Problem{problem(CodeValidation, "", "care integration failed")}
				}
			} else if to == dbgen.PlantStatusACTIVE {
				if err := s.Hooks.PlantRestored(ctx, q, a.WorkspaceID, uid); err != nil {
					if s.Log != nil {
						s.Log.Error("care hook: PlantRestored", "plant", uuidString(uid), "err", err)
					}
					return PlantResult{}, []Problem{problem(CodeValidation, "", "care integration failed")}
				}
			}
			return PlantResult{ID: uuidString(uid), Revision: updated.Revision}, nil
		})
}

// PurgePlant permanently deletes a plant under the 03 deletion matrix:
// owned history cascades; purchase allocations become deleted-plant
// markers with their amounts preserved; photo links are released so an
// unreferenced asset enters DELETING with its bytes purged after the
// grace period (a receipt-shared asset survives); every deleted id is
// written to the deletion ledger; audit content mentioning them is
// redacted; stored mutation receipts mentioning them replay as
// RESOURCE_GONE; live exports are revoked with their archives scheduled
// for deletion; and the workspace privacy epoch advances.
func (s *Service) PurgePlant(ctx context.Context, a Actor, id string, expectedRevision int32, confirm string, cmid *string) (PurgeResult, []Problem, error) {
	return mutateTx(s, ctx, a, "purgePlant", cmid, map[string]any{"id": id, "expectedRevision": expectedRevision, "confirm": confirm},
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (PurgeResult, []Problem) {
			if confirm != "PERMANENTLY_DELETE_PLANT" {
				return PurgeResult{}, []Problem{problem(CodeValidation, "input.confirm", "confirmation phrase required")}
			}
			uid, ok := parseUUID(id)
			if !ok {
				return PurgeResult{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}
			}
			plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: uid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return PurgeResult{}, notFound("input.id", "plant not found")
			}

			// financial references become deleted-plant markers; the
			// allocation rows keep their amounts and line
			if err := q.ClearPlantAllocations(ctx, dbgen.ClearPlantAllocationsParams{
				WorkspaceID: a.WorkspaceID, PlantID: uid,
			}); err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "allocation references could not be cleared")}
			}

			// photo rows go now; the asset follows the cleanup rule
			photos, err := q.ListPlantPhotoRows(ctx, dbgen.ListPlantPhotoRowsParams{
				WorkspaceID: a.WorkspaceID, PlantID: uid,
			})
			if err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "photo rows could not be listed")}
			}
			for _, ph := range photos {
				if err := q.DeletePlantPhotoRow(ctx, dbgen.DeletePlantPhotoRowParams{
					WorkspaceID: a.WorkspaceID, ID: ph.ID,
				}); err != nil {
					return PurgeResult{}, []Problem{problem(CodeValidation, "", "photo row could not be deleted")}
				}
				if p := s.releaseAssetIfUnreferenced(ctx, q, tx, a, ph.AssetID); p != nil {
					return PurgeResult{}, []Problem{*p}
				}
			}

			// every id that cascades away with the plant
			owned, err := q.ListPlantOwnedIDs(ctx, dbgen.ListPlantOwnedIDsParams{
				WorkspaceID: a.WorkspaceID, PlantID: uid,
			})
			if err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "owned entities could not be listed")}
			}
			all := append([]dbgen.ListPlantOwnedIDsRow{{EntityKind: "plant", ID: uid}}, owned...)
			ids := make([]pgtype.UUID, 0, len(all))
			refs := make([]string, 0, len(all))
			for _, o := range all {
				ids = append(ids, o.ID)
				refs = append(refs, uuidString(o.ID))
			}

			epoch, err := q.BumpPrivacyEpoch(ctx, a.WorkspaceID)
			if err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "privacy epoch could not be advanced")}
			}
			for _, o := range all {
				if _, err := q.InsertTombstone(ctx, dbgen.InsertTombstoneParams{
					ID: s.uuid.New(), WorkspaceID: a.WorkspaceID,
					EntityKind: o.EntityKind, EntityID: o.ID, Action: "purgePlant",
					ActorID:      a.OwnerID,
					OccurredAt:   pgtype.Timestamptz{Time: s.clock.Now(), Valid: true},
					PrivacyEpoch: epoch,
				}); err != nil {
					return PurgeResult{}, []Problem{problem(CodeValidation, "", "deletion ledger entry could not be written")}
				}
			}

			// audit prior content mentioning purged ids is redacted;
			// actor/action/time/aggregate/revision stay
			if err := q.RedactAuditTouching(ctx, dbgen.RedactAuditTouchingParams{
				WorkspaceID: a.WorkspaceID, AggregateIds: ids, Refs: refs,
			}); err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "audit content could not be redacted")}
			}
			// stored responses mentioning purged ids replay as
			// RESOURCE_GONE; the purge's own receipt replays its success
			if err := q.TombstoneReceiptsTouching(ctx, dbgen.TombstoneReceiptsTouchingParams{
				WorkspaceID: a.WorkspaceID, Refs: refs,
			}); err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "stored responses could not be tombstoned")}
			}

			// in-flight exports are stale the moment a purge lands:
			// revoked now, archive files deleted within 24h
			live, err := q.ListLiveExportJobs(ctx, a.WorkspaceID)
			if err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "export jobs could not be listed")}
			}
			if err := q.RevokeWorkspaceExports(ctx, a.WorkspaceID); err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "exports could not be revoked")}
			}
			if s.Queue != nil {
				for _, jid := range live {
					if _, err := s.Queue.InsertTx(ctx, tx,
						portability.ExportCleanupArgs{ExportID: uuidString(jid)}, nil); err != nil {
						return PurgeResult{}, []Problem{problem(CodeValidation, "", "export cleanup could not be scheduled")}
					}
				}
			}

			// the purge's own audit row carries no prior content
			if err := s.audit(ctx, q, a, "purgePlant", "plant", uid, plant.Revision, nil); err != nil {
				return PurgeResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}

			if _, err := q.DeletePlant(ctx, dbgen.DeletePlantParams{
				ID: uid, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			}); err != nil {
				if isForeignKeyViolation(err) {
					return PurgeResult{}, []Problem{problem(CodeConflict, "input.id",
						"plant is referenced by data that cannot be detached; correct those first")}
				}
				return PurgeResult{}, s.conflictProblem(ctx, q, "plants", uid, a.WorkspaceID, err)
			}
			return PurgeResult{PlantID: uuidString(uid)}, nil
		})
}

// releaseAssetIfUnreferenced mirrors the media cleanup rule: the last
// live link sends the asset to DELETING and schedules byte deletion
// after the grace period; an asset still linked from a receipt (or any
// other live link) is preserved — one plant's purge never erases a
// shared receipt image.
func (s *Service) releaseAssetIfUnreferenced(ctx context.Context, q *dbgen.Queries, tx pgx.Tx, a Actor, assetID pgtype.UUID) *Problem {
	photos, err := q.CountLivePhotoLinksByAsset(ctx, dbgen.CountLivePhotoLinksByAssetParams{
		AssetID: assetID, WorkspaceID: a.WorkspaceID,
	})
	if err != nil {
		return pgProblem(CodeValidation, "asset references could not be counted")
	}
	receipts, err := q.CountReceiptLinksByAsset(ctx, dbgen.CountReceiptLinksByAssetParams{
		AssetID: assetID, WorkspaceID: a.WorkspaceID,
	})
	if err != nil {
		return pgProblem(CodeValidation, "asset references could not be counted")
	}
	if photos+receipts > 0 {
		return nil // shared asset preserved until the last link is gone
	}
	if _, err := q.MarkAssetStateFrom(ctx, dbgen.MarkAssetStateFromParams{
		ID: assetID, WorkspaceID: a.WorkspaceID,
		FromState: dbgen.AssetStateREADY, ToState: dbgen.AssetStateDELETING,
		ErrorCode: pgtype.Text{},
	}); errors.Is(err, pgx.ErrNoRows) {
		return nil // already DELETING/FAILED: nothing to schedule
	} else if err != nil {
		return pgProblem(CodeValidation, "asset state could not be advanced")
	}
	if s.Queue == nil {
		return nil
	}
	if _, err := s.Queue.InsertTx(ctx, tx, media.PurgeAssetArgs{AssetID: uuidString(assetID)},
		&river.InsertOpts{ScheduledAt: s.clock.Now().Add(media.PurgeGrace)}); err != nil {
		return pgProblem(CodeValidation, "asset purge could not be scheduled")
	}
	return nil
}

func pgProblem(code string, msg string) *Problem {
	p := problem(code, "", msg)
	return &p
}

// SetPlantTags replaces the tag set (archived tags cannot be newly assigned)
// and bumps the plant revision under the same guard.
func (s *Service) SetPlantTags(ctx context.Context, a Actor, in SetPlantTagsInput, cmid *string) (PlantResult, []Problem, error) {
	return mutate[PlantResult](s, ctx, a, "setPlantTags", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (PlantResult, []Problem) {
			pid, ok := parseUUID(in.PlantID)
			if !ok {
				return PlantResult{}, []Problem{problem(CodeValidation, "input.plantId", "malformed id")}
			}
			if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: pid, WorkspaceID: a.WorkspaceID}); err != nil {
				return PlantResult{}, notFound("input.plantId", "plant not found")
			}
			tagIDs, p := parseTagIDs(ctx, q, a, in.TagIDs, "input.tagIds")
			if p != nil {
				return PlantResult{}, []Problem{*p}
			}
			plant, err := q.BumpPlantRevision(ctx, dbgen.BumpPlantRevisionParams{
				ID: pid, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return PlantResult{}, s.conflictProblem(ctx, q, "plants", pid, a.WorkspaceID, err)
			}
			if err := q.ReplacePlantTags(ctx, dbgen.ReplacePlantTagsParams{PlantID: pid, WorkspaceID: a.WorkspaceID}); err != nil {
				return PlantResult{}, []Problem{problem(CodeValidation, "", "tags could not be replaced")}
			}
			for _, tag := range tagIDs {
				if err := q.InsertPlantTag(ctx, dbgen.InsertPlantTagParams{
					WorkspaceID: a.WorkspaceID, PlantID: pid, TagID: tag,
				}); err != nil {
					return PlantResult{}, []Problem{problem(CodeValidation, "input.tagIds", "tag link could not be stored")}
				}
			}
			if err := s.audit(ctx, q, a, "setPlantTags", "plant", pid, plant.Revision, nil); err != nil {
				return PlantResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return PlantResult{ID: uuidString(pid), Revision: plant.Revision}, nil
		})
}

// SetPlantTagsInput is the domain mirror of the contract input.
type SetPlantTagsInput struct {
	PlantID          string
	ExpectedRevision int32
	TagIDs           []string
}

// SetPlantContext appends a context entry (history, never overwrite) and
// bumps the plant revision under one guard.
func (s *Service) SetPlantContext(ctx context.Context, a Actor, in SetPlantContextInput, cmid *string) (ContextResult, []Problem, error) {
	res, problems, err := mutate[ContextResult](s, ctx, a, "setPlantContext", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (ContextResult, []Problem) {
			pid, ok := parseUUID(in.PlantID)
			if !ok {
				return ContextResult{}, []Problem{problem(CodeValidation, "input.plantId", "malformed id")}
			}
			if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: pid, WorkspaceID: a.WorkspaceID}); err != nil {
				return ContextResult{}, notFound("input.plantId", "plant not found")
			}
			if in.Context == nil {
				return ContextResult{}, []Problem{problem(CodeValidation, "input.context", "context is required")}
			}
			if problems := validateContext(ctx, q, a, *in.Context); len(problems) > 0 {
				return ContextResult{}, problems
			}
			plant, err := q.BumpPlantRevision(ctx, dbgen.BumpPlantRevisionParams{
				ID: pid, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return ContextResult{}, s.conflictProblem(ctx, q, "plants", pid, a.WorkspaceID, err)
			}
			row, err := s.insertContext(ctx, q, a, pid, *in.Context)
			if err != nil {
				return ContextResult{}, []Problem{problem(CodeValidation, "input.context", "context could not be stored")}
			}
			if err := s.audit(ctx, q, a, "setPlantContext", "plant", pid, plant.Revision, nil); err != nil {
				return ContextResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			// Care invalidation rides this transaction (T24): future advice
			// is cancelled and regenerated against the new context, or the
			// context edit rolls back with it.
			if herr := s.Hooks.PlantContextChanged(ctx, q, a.WorkspaceID, pid, row.EffectiveFrom.Time); herr != nil {
				if s.Log != nil {
					s.Log.Error("care hook: PlantContextChanged", "plant", uuidString(pid), "err", herr)
				}
				return ContextResult{}, []Problem{problem(CodeValidation, "", "care integration failed")}
			}
			return ContextResult{
				PlantID: uuidString(pid), ContextID: uuidString(row.ID),
				Revision: plant.Revision, Effective: row.EffectiveFrom.Time,
			}, nil
		})
	return res, problems, err
}

// SetPlantContextInput is the domain mirror of the contract input.
type SetPlantContextInput struct {
	PlantID          string
	ExpectedRevision int32
	Context          *ContextInput
}

func (s *Service) insertContext(ctx context.Context, q *dbgen.Queries, a Actor, plantID pgtype.UUID, in ContextInput) (dbgen.PlantContext, error) {
	effective := s.clock.Now()
	if in.EffectiveFrom != nil {
		effective = *in.EffectiveFrom
	}
	var locID pgtype.UUID
	if in.LocationID != nil {
		locID, _ = parseUUID(*in.LocationID) // validated earlier
	}
	return q.InsertPlantContext(ctx, dbgen.InsertPlantContextParams{
		ID:              s.uuid.New(),
		WorkspaceID:     a.WorkspaceID,
		PlantID:         plantID,
		EffectiveFrom:   pgtype.Timestamptz{Time: effective, Valid: true},
		LocationID:      locID,
		Placement:       dbgen.ContextPlacement(in.Placement),
		PotVolumeLiters: in.PotVolumeLiters,
		MediumNote:      textPtr(in.MediumNote),
		LightNote:       textPtr(in.LightNote),
	})
}

// validateContext checks location membership (cross-workspace links are
// denied as NOT_FOUND) and container/ground consistency.
func validateContext(ctx context.Context, q *dbgen.Queries, a Actor, in ContextInput) []Problem {
	var problems []Problem
	if in.LocationID != nil {
		locID, ok := parseUUID(*in.LocationID)
		if !ok {
			return []Problem{problem(CodeValidation, "input.locationId", "malformed id")}
		}
		if _, err := q.GetLocation(ctx, dbgen.GetLocationParams{ID: locID, WorkspaceID: a.WorkspaceID}); err != nil {
			// Cross-workspace behaves exactly like absent.
			return []Problem{problem(CodeNotFound, "input.locationId", "location not found")}
		}
	}
	if in.Placement == "GROUND" && in.PotVolumeLiters != nil {
		problems = append(problems, problem(CodeValidation, "input.potVolumeLiters",
			"pot volume does not apply to ground placements"))
	}
	if in.PotVolumeLiters != nil && numericSign(in.PotVolumeLiters) <= 0 {
		problems = append(problems, problem(CodeValidation, "input.potVolumeLiters",
			"pot volume must be positive"))
	}
	return problems
}

// parseTagIDs resolves ids to workspace-live tags; archived or foreign tags
// are refused for new assignment.
func parseTagIDs(ctx context.Context, q *dbgen.Queries, a Actor, ids []string, field string) ([]pgtype.UUID, *Problem) {
	seen := map[string]bool{}
	out := make([]pgtype.UUID, 0, len(ids))
	for _, raw := range ids {
		if seen[raw] {
			continue
		}
		seen[raw] = true
		uid, ok := parseUUID(raw)
		if !ok {
			return nil, ptrProblem(problem(CodeValidation, field, "malformed id"))
		}
		tag, err := q.GetTag(ctx, dbgen.GetTagParams{ID: uid, WorkspaceID: a.WorkspaceID})
		if err != nil {
			return nil, ptrProblem(problem(CodeNotFound, field, "tag not found"))
		}
		if tag.ArchivedAt.Valid {
			return nil, ptrProblem(problem(CodeValidation, field, "tag is archived and cannot be newly assigned"))
		}
		out = append(out, uid)
	}
	return out, nil
}

// conflictProblem turns a failed revision guard into CONFLICT with the
// current revision (re-read inside the same transaction).
func (s *Service) conflictProblem(ctx context.Context, q *dbgen.Queries, table string, id, ws pgtype.UUID, guardErr error) []Problem {
	if !errors.Is(guardErr, pgx.ErrNoRows) {
		s.Log.Error(table+" guard", "err", guardErr)
		return []Problem{problem(CodeConflict, "input.expectedRevision", "update could not be applied")}
	}
	switch table {
	case "plants":
		cur, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return notFound("input.id", "plant not found")
		}
		return conflictWith("input.expectedRevision", cur.Revision)
	case "locations":
		cur, err := q.GetLocation(ctx, dbgen.GetLocationParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return notFound("input.id", "location not found")
		}
		return conflictWith("input.expectedRevision", cur.Revision)
	case "tags":
		cur, err := q.GetTag(ctx, dbgen.GetTagParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return notFound("input.id", "tag not found")
		}
		return conflictWith("input.expectedRevision", cur.Revision)
	case "acquisitions":
		cur, err := q.GetAcquisition(ctx, dbgen.GetAcquisitionParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return notFound("input.id", "acquisition not found")
		}
		return conflictWith("input.expectedRevision", cur.Revision)
	}
	return []Problem{problem(CodeConflict, "input.expectedRevision", "update could not be applied")}
}

func conflictWith(field string, revision int32) []Problem {
	rev := revision
	return []Problem{Problem{Code: CodeConflict, Field: field,
		Message: "revision is stale; the aggregate changed", CurrentRevision: &rev}}
}

func notFound(field, msg string) []Problem {
	return []Problem{problem(CodeNotFound, field, msg)}
}

func textPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func int4Ptr(n *int32) pgtype.Int4 {
	if n == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *n, Valid: true}
}

func datePtr(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}
