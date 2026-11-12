package media

import (
	"context"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Finalize verifies the recorded checksum against the caller's, checks
// the stored object is present, then transactionally marks PROCESSING
// and inserts the processor job on the same tx. Repeat finalize is
// safe: PROCESSING/READY assets return their current state unchanged
// and no second job is queued.
func (s *Service) Finalize(ctx context.Context, a Actor, assetID, sha256hex string, cmid *string) (AssetView, []Problem, error) {
	input := map[string]string{"assetId": assetID, "sha256": sha256hex}
	return mutate(s, ctx, a, "finalizeUpload", cmid, input,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (AssetView, []Problem, bool) {
			id, ok := parseUUID(assetID)
			if !ok {
				return AssetView{}, []Problem{problem(CodeValidation, "input.assetId", "assetId must be a UUID")}, false
			}
			if !shaRe.MatchString(strings.ToLower(sha256hex)) {
				return AssetView{}, []Problem{problem(CodeValidation, "input.sha256",
					"sha256 must be 64 lowercase hex characters")}, false
			}
			asset, err := q.LockAsset(ctx, dbgen.LockAssetParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return AssetView{}, []Problem{problem(CodeNotFound, "input.assetId", "asset not found")}, false
			}
			switch asset.State {
			case dbgen.AssetStatePROCESSING, dbgen.AssetStateREADY:
				// safe repeat: same state, no re-enqueue
				v, ps := s.viewWithVariants(ctx, q, asset)
				return v, ps, false
			case dbgen.AssetStateRESERVED:
				return AssetView{}, []Problem{problem(CodeValidation, "input.assetId",
					"bytes have not been uploaded yet; PUT the body first")}, false
			case dbgen.AssetStateFAILED:
				return AssetView{}, []Problem{problem(CodeValidation, "input.assetId",
					"asset failed; upload a new copy")}, false
			case dbgen.AssetStateDELETING:
				return AssetView{}, []Problem{problem(CodeNotFound, "input.assetId", "asset not found")}, false
			}

			// state UPLOADED: the finalize checksum must match what was stored
			if !strings.EqualFold(sha256hex, asset.Sha256) {
				return AssetView{}, []Problem{problem(CodeValidation, "input.sha256",
					"sha256 does not match the stored bytes")}, false
			}
			present, err := s.Store.Exists(asset.StorageKey)
			if err != nil {
				return AssetView{}, []Problem{s.internalProblem(err)}, false
			}
			if !present {
				// DB without file is a recoverable failure: record it
				// honestly and keep that row even though the response is
				// a problem (keep=true commits the FAILED transition).
				if _, err := q.MarkAssetState(ctx, dbgen.MarkAssetStateParams{
					ID: asset.ID, WorkspaceID: a.WorkspaceID,
					State:     dbgen.AssetStateFAILED,
					ErrorCode: pgtype.Text{String: "OBJECT_MISSING", Valid: true},
				}); err != nil {
					return AssetView{}, []Problem{s.internalProblem(err)}, false
				}
				return AssetView{}, []Problem{problem(CodeConflict, "input.assetId",
					"stored object is missing; upload again")}, true
			}

			updated, err := q.MarkAssetState(ctx, dbgen.MarkAssetStateParams{
				ID: asset.ID, WorkspaceID: a.WorkspaceID,
				State: dbgen.AssetStatePROCESSING,
			})
			if err != nil {
				return AssetView{}, []Problem{s.internalProblem(err)}, false
			}
			if s.Queue == nil {
				return AssetView{}, []Problem{s.internalProblem(errNoQueue)}, false
			}
			if _, err := s.Queue.InsertTx(ctx, tx, ProcessAssetArgs{AssetID: uuidString(asset.ID)}, nil); err != nil {
				return AssetView{}, []Problem{s.internalProblem(err)}, false
			}
			v, ps := s.viewWithVariants(ctx, q, updated)
			return v, ps, false
		})
}

var errNoQueue = &staticError{"media processor queue not wired"}

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }

func (s *Service) viewWithVariants(ctx context.Context, q *dbgen.Queries, asset dbgen.Asset) (AssetView, []Problem) {
	variants, err := q.ListAssetVariants(ctx, []pgtype.UUID{asset.ID})
	if err != nil {
		return AssetView{}, []Problem{s.internalProblem(err)}
	}
	return viewOf(asset, variants), nil
}

// ExpireReservations sweeps unused reservations (24h) to DELETING and
// removes any staged object they left behind. Returns the swept count.
func (s *Service) ExpireReservations(ctx context.Context) (int, error) {
	rows, err := s.q().ExpireStaleReservations(ctx)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if err := s.Store.Delete(r.StorageKey); err != nil {
			// the row is already DELETING; a leftover file stays an orphan
			// candidate for the sweeper to retry — never a silent success
			s.Log.Warn("sweeper: staged object removal failed",
				"asset_id", uuidString(r.ID), "err", err)
		}
	}
	if len(rows) > 0 {
		s.Log.Info("sweeper: expired reservations", "count", len(rows))
	}
	return len(rows), nil
}
