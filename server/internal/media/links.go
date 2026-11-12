// Attachment use cases (T15): linking processed (READY) assets into the
// domain and detaching them again.
//
// Rules enforced here (03 "Media and attachments"):
//   - Only READY assets, correct purpose, same workspace. Cross-workspace
//     ids are indistinguishable from unknown ones (NOT_FOUND).
//   - Photo assets never implicitly share: one live plant_photos link per
//     photo asset. The check runs under the asset row lock, so two
//     concurrent attaches of the same asset serialize.
//   - Receipt assets may document many purchases; the asset is preserved
//     until the last live link is removed.
//   - At most one cover per plant: the partial unique index is the arbiter;
//     a concurrent cover win surfaces as CONFLICT.
//   - Capture-date semantics: USER wins and requires an explicit instant;
//     EXIF may stay null transiently (naive timestamp awaiting timezone);
//     UPLOAD_FALLBACK never invents silently — it labels the asset's
//     upload instant.
//   - Removing the last live link marks the asset DELETING (delivery stops
//     immediately) and schedules the purge job after the grace period;
//     the job re-checks references at run time.
package media

import (
	"canopy/server/internal/media/photodate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/journal"
)

// PurgeGrace is how long an unreferenced DELETING asset's bytes survive
// (03: unreferenced assets are collected after a 7-day grace).
const PurgeGrace = 7 * 24 * time.Hour

// PhotoView is the transport-neutral photo projection.
type PhotoView struct {
	ID         string     `json:"id"`
	PlantID    string     `json:"plantId"`
	Asset      AssetView  `json:"asset"`
	TakenAt    *time.Time `json:"takenAt,omitempty"`
	DateSource string     `json:"dateSource"`
	Timezone   *string    `json:"timezone,omitempty"`
	Caption    *string    `json:"caption,omitempty"`
	Viewpoint  string     `json:"viewpoint"`
	IsCover    bool       `json:"isCover"`
	Revision   int32      `json:"revision"`
	DeletedAt  *time.Time `json:"deletedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// ReceiptView is one purchase<->receipt-asset link.
type ReceiptView struct {
	ID         string    `json:"id"`
	PurchaseID string    `json:"purchaseId"`
	Asset      AssetView `json:"asset"`
}

type AttachPhotoInput struct {
	PreferCaptureDate bool
	PlantID           string
	AssetID           string
	TakenAt           *time.Time
	DateSource        string
	Timezone          *string
	Caption           *string
	Viewpoint         string
	IsCover           bool
}

type UpdatePhotoInput struct {
	ID               string
	ExpectedRevision int32
	TakenAt          *time.Time
	DateSource       *string
	Timezone         *string
	Caption          *string
	Viewpoint        *string
	IsCover          *bool
}

type AttachReceiptInput struct {
	PurchaseID string
	AssetID    string
}

type DetachReceiptInput struct {
	PurchaseID       string
	ReceiptID        string
	ExpectedRevision int32
}

var viewpoints = map[string]bool{"WHOLE": true, "LEAF": true, "FRUIT": true, "OTHER": true}
var dateSources = map[string]bool{"USER": true, "EXIF": true, "UPLOAD_FALLBACK": true}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// audit writes one audit_revisions row inside the mutation's tx, mirroring
// the collection/purchases services (bounded prior values, no secrets).
func (s *Service) audit(ctx context.Context, q *dbgen.Queries, a Actor, action, aggType string, aggID pgtype.UUID, revision int32, prior any) error {
	var priorJSON []byte
	if prior != nil {
		b, err := json.Marshal(prior)
		if err != nil {
			return err
		}
		priorJSON = b
	}
	_, err := q.InsertAuditRevision(ctx, dbgen.InsertAuditRevisionParams{
		ID:            s.uuid.New(),
		WorkspaceID:   a.WorkspaceID,
		ActorID:       a.OwnerID,
		Action:        action,
		AggregateType: aggType,
		AggregateID:   aggID,
		Revision:      revision,
		RequestID:     uuidString(a.SessionID),
		PriorValues:   priorJSON,
	})
	return err
}

// ---- attachPhoto -----------------------------------------------------------

// AttachPhoto links a READY PHOTO asset to a plant in the actor's
// workspace. The asset row is locked for the whole decision, which makes
// the no-implicit-sharing check race-free.
func (s *Service) AttachPhoto(ctx context.Context, a Actor, in AttachPhotoInput, cmid *string) (PhotoView, []Problem, error) {
	return mutate(s, ctx, a, "attachPhoto", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (PhotoView, []Problem, bool) {
			plantID, ok := parseUUID(in.PlantID)
			if !ok {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.plantId", "plantId must be a UUID")}, false
			}
			assetID, ok := parseUUID(in.AssetID)
			if !ok {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.assetId", "assetId must be a UUID")}, false
			}
			if !viewpoints[in.Viewpoint] {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.viewpoint", "viewpoint must be WHOLE, LEAF, FRUIT or OTHER")}, false
			}
			if !dateSources[in.DateSource] {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.dateSource", "dateSource must be USER, EXIF or UPLOAD_FALLBACK")}, false
			}
			if in.Caption != nil && len(*in.Caption) > 20000 {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.caption", "caption too long")}, false
			}
			if in.Timezone != nil && *in.Timezone != "" {
				if _, err := time.LoadLocation(*in.Timezone); err != nil {
					return PhotoView{}, []Problem{problem(CodeValidation, "input.timezone", "timezone must be an IANA zone")}, false
				}
			}

			if _, err := q.LockPlantForPhotoChange(ctx, dbgen.LockPlantForPhotoChangeParams{ID: plantID, WorkspaceID: a.WorkspaceID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return PhotoView{}, []Problem{problem(CodeNotFound, "input.plantId", "plant not found")}, false
				}
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}

			asset, err := q.LockAsset(ctx, dbgen.LockAssetParams{ID: assetID, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				// cross-workspace and unknown assets are indistinguishable
				return PhotoView{}, []Problem{problem(CodeNotFound, "input.assetId", "asset not found")}, false
			} else if err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			if asset.Purpose != dbgen.AssetPurposePHOTO {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.assetId",
					"wrong purpose: plant photos require a PHOTO asset")}, false
			}
			if asset.State != dbgen.AssetStateREADY {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.assetId",
					fmt.Sprintf("asset is not ready (state %s)", asset.State))}, false
			}
			if _, err := q.GetLivePhotoLinkByAsset(ctx, dbgen.GetLivePhotoLinkByAssetParams{AssetID: assetID, WorkspaceID: a.WorkspaceID}); err == nil {
				return PhotoView{}, []Problem{problem(CodeConflict, "input.assetId",
					"this photo asset is already attached; photos never implicitly share")}, false
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}

			if in.PreferCaptureDate && in.TakenAt == nil {
				if s.Store == nil {
					return PhotoView{}, []Problem{s.internalProblem(fmt.Errorf("photo metadata store unavailable"))}, false
				}
				original, err := s.Store.Open(asset.StorageKey)
				if err != nil {
					return PhotoView{}, []Problem{s.internalProblem(err)}, false
				}
				data, err := io.ReadAll(io.LimitReader(original, MaxPhotoBytes+1))
				original.Close()
				if err != nil || int64(len(data)) > MaxPhotoBytes {
					return PhotoView{}, []Problem{s.internalProblem(fmt.Errorf("photo metadata read failed"))}, false
				}
				zone := ""
				if in.Timezone != nil {
					zone = *in.Timezone
				}
				date := photodate.Read(data, zone)
				if date == nil {
					in.DateSource = "UPLOAD_FALLBACK"
					in.Timezone = nil
				} else {
					in.DateSource = "EXIF"
					in.TakenAt = date.Instant
					if date.OffsetKnown {
						in.Timezone = nil
					}
				}
			}
			takenAt, p := resolveTakenAt(in.DateSource, in.TakenAt, asset.CreatedAt)
			if p != nil {
				return PhotoView{}, []Problem{*p}, false
			}

			if !in.IsCover {
				automatic, err := automaticCover(ctx, q, a.WorkspaceID, plantID, asset, in.Viewpoint, takenAt, in.DateSource)
				if err != nil {
					return PhotoView{}, []Problem{s.internalProblem(err)}, false
				}
				in.IsCover = automatic
			}
			if in.IsCover {
				if err := q.ClearCoverForPlant(ctx, dbgen.ClearCoverForPlantParams{PlantID: plantID, WorkspaceID: a.WorkspaceID}); err != nil {
					return PhotoView{}, []Problem{s.internalProblem(err)}, false
				}
			}
			photo, err := q.InsertPhoto(ctx, dbgen.InsertPhotoParams{
				ID:          s.uuid.New(),
				WorkspaceID: a.WorkspaceID,
				PlantID:     plantID,
				AssetID:     assetID,
				TakenAt:     takenAt,
				DateSource:  dbgen.PhotoDateSource(in.DateSource),
				Timezone:    textPtr(in.Timezone),
				Caption:     textPtr(in.Caption),
				Viewpoint:   dbgen.PhotoViewpoint(in.Viewpoint),
				IsCover:     in.IsCover,
			})
			if isUnique(err) {
				// cover index lost a race with a concurrent cover set
				return PhotoView{}, []Problem{problem(CodeConflict, "input.isCover",
					"another cover was set concurrently")}, false
			} else if isFKViolation(err) {
				return PhotoView{}, []Problem{problem(CodeNotFound, "input.plantId", "plant not found")}, false
			} else if err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}

			if err := s.audit(ctx, q, a, "attachPhoto", "plant_photo", photo.ID, photo.Revision, nil); err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			// journal projection (T16), same tx as the photo row
			if err := journal.ProjectPhoto(ctx, q, s.uuid.New(), a.WorkspaceID, plantID, photo.ID,
				entryOccurredAt(photo.TakenAt, asset.CreatedAt),
				journal.PhotoSummary(photo.Caption, photo.Viewpoint)); err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			return s.photoOut(ctx, q, photo)
		})
}

// resolveTakenAt applies the frozen date-source semantics.
func resolveTakenAt(dateSource string, in *time.Time, uploaded pgtype.Timestamptz) (pgtype.Timestamptz, *Problem) {
	switch dateSource {
	case "USER":
		if in == nil {
			return pgtype.Timestamptz{}, &Problem{Code: CodeValidation, Field: "input.takenAt",
				Message: "dateSource USER requires an explicit takenAt"}
		}
		return pgtype.Timestamptz{Time: *in, Valid: true}, nil
	case "UPLOAD_FALLBACK":
		if in != nil {
			return pgtype.Timestamptz{Time: *in, Valid: true}, nil
		}
		// never a silent invention: the label says this is the upload instant
		return uploaded, nil
	default: // EXIF: null only transiently until a naive timestamp's timezone is resolved
		if in == nil {
			return pgtype.Timestamptz{}, nil
		}
		return pgtype.Timestamptz{Time: *in, Valid: true}, nil
	}
}

// ---- updatePhoto -----------------------------------------------------------

func (s *Service) UpdatePhoto(ctx context.Context, a Actor, in UpdatePhotoInput, cmid *string) (PhotoView, []Problem, error) {
	return mutate(s, ctx, a, "updatePhoto", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (PhotoView, []Problem, bool) {
			photoID, ok := parseUUID(in.ID)
			if !ok {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.id", "id must be a UUID")}, false
			}
			if in.Viewpoint != nil && !viewpoints[*in.Viewpoint] {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.viewpoint", "viewpoint must be WHOLE, LEAF, FRUIT or OTHER")}, false
			}
			if in.DateSource != nil && !dateSources[*in.DateSource] {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.dateSource", "dateSource must be USER, EXIF or UPLOAD_FALLBACK")}, false
			}
			if in.Caption != nil && len(*in.Caption) > 20000 {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.caption", "caption too long")}, false
			}
			if in.Timezone != nil && *in.Timezone != "" {
				if _, err := time.LoadLocation(*in.Timezone); err != nil {
					return PhotoView{}, []Problem{problem(CodeValidation, "input.timezone", "timezone must be an IANA zone")}, false
				}
			}

			cur, err := q.GetPhoto(ctx, dbgen.GetPhotoParams{ID: photoID, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return PhotoView{}, []Problem{problem(CodeNotFound, "input.id", "photo not found")}, false
			} else if err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			if _, err := q.LockPlantForPhotoChange(ctx, dbgen.LockPlantForPhotoChangeParams{ID: cur.PlantID, WorkspaceID: a.WorkspaceID}); err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			// switching to USER without an instant is only honest when the
			// photo already carries one
			if in.DateSource != nil && *in.DateSource == "USER" && in.TakenAt == nil && !cur.TakenAt.Valid {
				return PhotoView{}, []Problem{problem(CodeValidation, "input.takenAt",
					"dateSource USER requires an explicit takenAt")}, false
			}

			params := dbgen.UpdatePhotoParams{
				ID: photoID, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				TakenAt:    tsNarg(in.TakenAt),
				DateSource: nullDateSource(in.DateSource),
				Timezone:   textNarg(in.Timezone),
				Caption:    textNarg(in.Caption),
				Viewpoint:  nullViewpoint(in.Viewpoint),
				IsCover:    pgtype.Bool{Bool: boolVal(in.IsCover), Valid: in.IsCover != nil},
			}
			if in.IsCover != nil && *in.IsCover && !cur.IsCover {
				if err := q.ClearCoverForPlant(ctx, dbgen.ClearCoverForPlantParams{PlantID: cur.PlantID, WorkspaceID: a.WorkspaceID}); err != nil {
					return PhotoView{}, []Problem{s.internalProblem(err)}, false
				}
			}
			updated, err := q.UpdatePhoto(ctx, params)
			if errors.Is(err, pgx.ErrNoRows) {
				fresh, ferr := q.GetPhoto(ctx, dbgen.GetPhotoParams{ID: photoID, WorkspaceID: a.WorkspaceID})
				if ferr != nil {
					return PhotoView{}, []Problem{problem(CodeNotFound, "input.id", "photo not found")}, false
				}
				return PhotoView{}, []Problem{problemWithRevision(problem(CodeConflict, "input.expectedRevision",
					"photo was modified by another request"), fresh.Revision)}, false
			} else if isUnique(err) {
				return PhotoView{}, []Problem{problem(CodeConflict, "input.isCover",
					"another cover was set concurrently")}, false
			} else if err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}

			if err := s.audit(ctx, q, a, "updatePhoto", "plant_photo", updated.ID, updated.Revision,
				map[string]any{"takenAt": cur.TakenAt.Time, "isCover": cur.IsCover, "caption": cur.Caption.String}); err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			// journal projection (T16): a capture-date correction moves the
			// timeline entry in the same tx as the photo row — never one
			// without the other
			asset, err := q.GetAssetByID(ctx, updated.AssetID)
			if err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			if err := journal.ProjectPhoto(ctx, q, s.uuid.New(), a.WorkspaceID, updated.PlantID, updated.ID,
				entryOccurredAt(updated.TakenAt, asset.CreatedAt),
				journal.PhotoSummary(updated.Caption, updated.Viewpoint)); err != nil {
				return PhotoView{}, []Problem{s.internalProblem(err)}, false
			}
			return s.photoOut(ctx, q, updated)
		})
}

// ---- deletePhoto -----------------------------------------------------------

func (s *Service) DeletePhoto(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (string, []Problem, error) {
	type out struct {
		DeletedPhotoID string `json:"deletedPhotoId"`
	}
	res, problems, err := mutate(s, ctx, a, "deletePhoto", cmid, map[string]any{"id": id, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (out, []Problem, bool) {
			photoID, ok := parseUUID(id)
			if !ok {
				return out{}, []Problem{problem(CodeValidation, "input.id", "id must be a UUID")}, false
			}
			target, err := q.GetPhoto(ctx, dbgen.GetPhotoParams{ID: photoID, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return out{}, []Problem{problem(CodeNotFound, "input.id", "photo not found")}, false
			}
			if err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			if _, err = q.LockPlantForPhotoChange(ctx, dbgen.LockPlantForPhotoChangeParams{ID: target.PlantID, WorkspaceID: a.WorkspaceID}); err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			_, err = q.DeletePhoto(ctx, dbgen.DeletePhotoParams{ID: photoID, WorkspaceID: a.WorkspaceID, Revision: expectedRevision})
			if errors.Is(err, pgx.ErrNoRows) {
				fresh, ferr := q.GetPhoto(ctx, dbgen.GetPhotoParams{ID: photoID, WorkspaceID: a.WorkspaceID})
				if ferr != nil {
					return out{}, []Problem{problem(CodeNotFound, "input.id", "photo not found")}, false
				}
				return out{}, []Problem{problemWithRevision(problem(CodeConflict, "input.expectedRevision",
					"photo was modified by another request"), fresh.Revision)}, false
			} else if err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			photo, err := q.GetPhotoAny(ctx, dbgen.GetPhotoAnyParams{ID: photoID, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			if err := s.releaseIfUnreferenced(ctx, q, tx, a, photo.AssetID); err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			// journal tombstone (T16): the entry leaves active timelines but
			// stays attributable with includeTombstoned
			if err := journal.TombstonePhoto(ctx, q, a.WorkspaceID, photoID); err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "deletePhoto", "plant_photo", photoID, photo.Revision, nil); err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			return out{DeletedPhotoID: uuidString(photoID)}, nil, false
		})
	return res.DeletedPhotoID, problems, err
}

// releaseIfUnreferenced implements the cleanup reference rule: when the
// last live link (photo or receipt) to an asset is removed, the asset
// goes to DELETING immediately (delivery stops) and a purge job runs
// after the grace period; the job re-checks references at run time.
func (s *Service) releaseIfUnreferenced(ctx context.Context, q *dbgen.Queries, tx pgx.Tx, a Actor, assetID pgtype.UUID) error {
	photos, err := q.CountLivePhotoLinksByAsset(ctx, dbgen.CountLivePhotoLinksByAssetParams{AssetID: assetID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return err
	}
	receipts, err := q.CountReceiptLinksByAsset(ctx, dbgen.CountReceiptLinksByAssetParams{AssetID: assetID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return err
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
		return err
	}
	_, err = s.Queue.InsertTx(ctx, tx, PurgeAssetArgs{AssetID: uuidString(assetID)},
		&river.InsertOpts{ScheduledAt: s.clock.Now().Add(PurgeGrace)})
	return err
}

// ---- attachReceipt / detachReceipt -----------------------------------------

func (s *Service) AttachReceipt(ctx context.Context, a Actor, in AttachReceiptInput, cmid *string) (ReceiptView, []Problem, error) {
	return mutate(s, ctx, a, "attachReceipt", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (ReceiptView, []Problem, bool) {
			purchaseID, ok := parseUUID(in.PurchaseID)
			if !ok {
				return ReceiptView{}, []Problem{problem(CodeValidation, "input.purchaseId", "purchaseId must be a UUID")}, false
			}
			assetID, ok := parseUUID(in.AssetID)
			if !ok {
				return ReceiptView{}, []Problem{problem(CodeValidation, "input.assetId", "assetId must be a UUID")}, false
			}
			if _, err := q.GetPurchase(ctx, dbgen.GetPurchaseParams{ID: purchaseID, WorkspaceID: a.WorkspaceID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ReceiptView{}, []Problem{problem(CodeNotFound, "input.purchaseId", "purchase not found")}, false
				}
				return ReceiptView{}, []Problem{s.internalProblem(err)}, false
			}
			asset, err := q.LockAsset(ctx, dbgen.LockAssetParams{ID: assetID, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return ReceiptView{}, []Problem{problem(CodeNotFound, "input.assetId", "asset not found")}, false
			} else if err != nil {
				return ReceiptView{}, []Problem{s.internalProblem(err)}, false
			}
			if asset.Purpose != dbgen.AssetPurposeRECEIPT {
				return ReceiptView{}, []Problem{problem(CodeValidation, "input.assetId",
					"wrong purpose: purchases require a RECEIPT asset")}, false
			}
			if asset.State != dbgen.AssetStateREADY {
				return ReceiptView{}, []Problem{problem(CodeValidation, "input.assetId",
					fmt.Sprintf("asset is not ready (state %s)", asset.State))}, false
			}
			if _, err := q.GetReceiptByPurchaseAsset(ctx, dbgen.GetReceiptByPurchaseAssetParams{PurchaseID: purchaseID, AssetID: assetID, WorkspaceID: a.WorkspaceID}); err == nil {
				return ReceiptView{}, []Problem{problem(CodeConflict, "input.assetId",
					"receipt already attached to this purchase")}, false
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return ReceiptView{}, []Problem{s.internalProblem(err)}, false
			}

			rec, err := q.InsertReceipt(ctx, dbgen.InsertReceiptParams{
				ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, PurchaseID: purchaseID, AssetID: assetID,
			})
			if isFKViolation(err) {
				return ReceiptView{}, []Problem{problem(CodeNotFound, "input.purchaseId", "purchase not found")}, false
			} else if err != nil {
				return ReceiptView{}, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "attachReceipt", "purchase_receipt", rec.ID, 1, nil); err != nil {
				return ReceiptView{}, []Problem{s.internalProblem(err)}, false
			}
			variants, err := q.ListAssetVariants(ctx, []pgtype.UUID{assetID})
			if err != nil {
				return ReceiptView{}, []Problem{s.internalProblem(err)}, false
			}
			return ReceiptView{ID: uuidString(rec.ID), PurchaseID: uuidString(rec.PurchaseID),
				Asset: viewOf(asset, variantsByAsset(variants)[uuidString(assetID)])}, nil, false
		})
}

func (s *Service) DetachReceipt(ctx context.Context, a Actor, in DetachReceiptInput, cmid *string) (string, []Problem, error) {
	type out struct {
		DetachedReceiptID string `json:"detachedReceiptId"`
	}
	res, problems, err := mutate(s, ctx, a, "detachReceipt", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (out, []Problem, bool) {
			purchaseID, ok := parseUUID(in.PurchaseID)
			if !ok {
				return out{}, []Problem{problem(CodeValidation, "input.purchaseId", "purchaseId must be a UUID")}, false
			}
			receiptID, ok := parseUUID(in.ReceiptID)
			if !ok {
				return out{}, []Problem{problem(CodeValidation, "input.receiptId", "receiptId must be a UUID")}, false
			}
			purchase, err := q.GetPurchase(ctx, dbgen.GetPurchaseParams{ID: purchaseID, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return out{}, []Problem{problem(CodeNotFound, "input.purchaseId", "purchase not found")}, false
			} else if err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			if purchase.Revision != in.ExpectedRevision {
				return out{}, []Problem{problemWithRevision(problem(CodeConflict, "input.expectedRevision",
					"purchase was modified by another request"), purchase.Revision)}, false
			}
			rec, err := q.GetReceiptByID(ctx, dbgen.GetReceiptByIDParams{ID: receiptID, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && rec.PurchaseID != purchaseID) {
				return out{}, []Problem{problem(CodeNotFound, "input.receiptId", "receipt not found on this purchase")}, false
			} else if err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			if _, err := q.DeleteReceipt(ctx, dbgen.DeleteReceiptParams{ID: receiptID, PurchaseID: purchaseID, WorkspaceID: a.WorkspaceID}); err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			if err := s.releaseIfUnreferenced(ctx, q, tx, a, rec.AssetID); err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "detachReceipt", "purchase_receipt", rec.ID, purchase.Revision, nil); err != nil {
				return out{}, []Problem{s.internalProblem(err)}, false
			}
			return out{DetachedReceiptID: uuidString(rec.ID)}, nil, false
		})
	return res.DetachedReceiptID, problems, err
}

// ---- shared projections ------------------------------------------------------

func photoViewOf(p dbgen.PlantPhoto, a dbgen.Asset, variants []dbgen.AssetVariant) PhotoView {
	v := PhotoView{
		ID: uuidString(p.ID), PlantID: uuidString(p.PlantID),
		Asset:      viewOf(a, variants),
		DateSource: string(p.DateSource), Viewpoint: string(p.Viewpoint),
		IsCover: p.IsCover, Revision: p.Revision,
	}
	if p.TakenAt.Valid {
		t := p.TakenAt.Time
		v.TakenAt = &t
	}
	if p.Timezone.Valid {
		v.Timezone = &p.Timezone.String
	}
	if p.Caption.Valid {
		v.Caption = &p.Caption.String
	}
	if p.DeletedAt.Valid {
		d := p.DeletedAt.Time
		v.DeletedAt = &d
	}
	if p.CreatedAt.Valid {
		v.CreatedAt = p.CreatedAt.Time
	}
	if p.UpdatedAt.Valid {
		v.UpdatedAt = p.UpdatedAt.Time
	}
	return v
}

// photoOut assembles one photo with its asset and variants inside the tx.
func (s *Service) photoOut(ctx context.Context, q *dbgen.Queries, p dbgen.PlantPhoto) (PhotoView, []Problem, bool) {
	asset, err := q.GetAssetByID(ctx, p.AssetID)
	if err != nil {
		return PhotoView{}, []Problem{s.internalProblem(err)}, false
	}
	variants, err := q.ListAssetVariants(ctx, []pgtype.UUID{p.AssetID})
	if err != nil {
		return PhotoView{}, []Problem{s.internalProblem(err)}, false
	}
	return photoViewOf(p, asset, variantsByAsset(variants)[uuidString(p.AssetID)]), nil, false
}

func variantsByAsset(vs []dbgen.AssetVariant) map[string][]dbgen.AssetVariant {
	m := map[string][]dbgen.AssetVariant{}
	for _, v := range vs {
		k := uuidString(v.AssetID)
		m[k] = append(m[k], v)
	}
	return m
}

// ---- read helpers for resolvers ---------------------------------------------

// CoverPhotos batch-loads live cover photos (with assets) for plants.
func (s *Service) CoverPhotos(ctx context.Context, ws pgtype.UUID, plantIDs []pgtype.UUID) (map[string]PhotoView, error) {
	out := map[string]PhotoView{}
	if len(plantIDs) == 0 {
		return out, nil
	}
	rows, err := s.q().ListCoverPhotos(ctx, dbgen.ListCoverPhotosParams{WorkspaceID: ws, PlantIds: plantIDs})
	if err != nil {
		return out, err
	}
	assets, variants, err := s.assetsFor(ctx, assetIDsOfPhotos(rows))
	if err != nil {
		return out, err
	}
	for _, p := range rows {
		a, ok := assets[uuidString(p.AssetID)]
		if !ok {
			continue
		}
		out[uuidString(p.PlantID)] = photoViewOf(p, a, variants[uuidString(p.AssetID)])
	}
	return out, nil
}

// ReceiptsForPurchases batch-loads receipt links (with assets) for purchases.
func (s *Service) ReceiptsForPurchases(ctx context.Context, ws pgtype.UUID, purchaseIDs []pgtype.UUID) (map[string][]ReceiptView, error) {
	out := map[string][]ReceiptView{}
	if len(purchaseIDs) == 0 {
		return out, nil
	}
	rows, err := s.q().ListReceiptsByPurchases(ctx, dbgen.ListReceiptsByPurchasesParams{WorkspaceID: ws, PurchaseIds: purchaseIDs})
	if err != nil {
		return out, err
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.AssetID)
	}
	assets, variants, err := s.assetsFor(ctx, ids)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		a, ok := assets[uuidString(r.AssetID)]
		if !ok {
			continue
		}
		out[uuidString(r.PurchaseID)] = append(out[uuidString(r.PurchaseID)],
			ReceiptView{ID: uuidString(r.ID), PurchaseID: uuidString(r.PurchaseID),
				Asset: viewOf(a, variants[uuidString(r.AssetID)])})
	}
	return out, nil
}

func (s *Service) assetsFor(ctx context.Context, ids []pgtype.UUID) (map[string]dbgen.Asset, map[string][]dbgen.AssetVariant, error) {
	assets := map[string]dbgen.Asset{}
	variants := map[string][]dbgen.AssetVariant{}
	if len(ids) == 0 {
		return assets, variants, nil
	}
	rows, err := s.q().ListAssetsByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	for _, a := range rows {
		assets[uuidString(a.ID)] = a
	}
	vs, err := s.q().ListAssetVariants(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range vs {
		k := uuidString(v.AssetID)
		variants[k] = append(variants[k], v)
	}
	return assets, variants, nil
}

func assetIDsOfPhotos(ps []dbgen.PlantPhoto) []pgtype.UUID {
	ids := make([]pgtype.UUID, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.AssetID)
	}
	return ids
}

// entryOccurredAt orders a photo's timeline entry: an unknown capture date
// uses the upload instant, visibly labeled via dateSource — never an
// invented instant (03 "Media and attachments").
func entryOccurredAt(takenAt, uploaded pgtype.Timestamptz) pgtype.Timestamptz {
	if takenAt.Valid {
		return takenAt
	}
	return uploaded
}

// PhotosByIDs batch-loads photos with assets and variants, including
// soft-deleted rows: tombstoned journal entries still reference their
// photo (photo.deletedAt is part of the frozen PhotoFields shape).
func (s *Service) PhotosByIDs(ctx context.Context, ws pgtype.UUID, ids []pgtype.UUID) (map[string]PhotoView, error) {
	out := map[string]PhotoView{}
	if len(ids) == 0 {
		return out, nil
	}
	// single join query (photo+asset+variants): keeps journal page
	// hydration inside the T18 query budget.
	rows, err := s.q().ListPhotosWithAssetsVariantsByIDs(ctx, dbgen.ListPhotosWithAssetsVariantsByIDsParams{WorkspaceID: ws, Ids: ids})
	if err != nil {
		return out, err
	}
	photos := map[string]dbgen.PlantPhoto{}
	assets := map[string]dbgen.Asset{}
	variants := map[string][]dbgen.AssetVariant{}
	for _, r := range rows {
		pk := uuidString(r.PID)
		if _, seen := photos[pk]; !seen {
			photos[pk] = dbgen.PlantPhoto{
				ID: r.PID, WorkspaceID: r.PWorkspaceID, PlantID: r.PPlantID, AssetID: r.PAssetID,
				TakenAt: r.PTakenAt, DateSource: r.PDateSource, Timezone: r.PTimezone, Caption: r.PCaption,
				Viewpoint: r.PViewpoint, IsCover: r.PIsCover, Revision: r.PRevision, DeletedAt: r.PDeletedAt,
				CreatedAt: r.PCreatedAt, UpdatedAt: r.PUpdatedAt,
			}
			assets[pk] = dbgen.Asset{
				ID: r.AID, WorkspaceID: r.AWorkspaceID, Purpose: r.APurpose, State: r.AState,
				ByteCount: r.AByteCount, MimeType: r.AMimeType, Sha256: r.ASha256, StorageKey: r.AStorageKey,
				DisplayName: r.ADisplayName, ErrorCode: r.AErrorCode, ExpiresAt: r.AExpiresAt,
				CreatedAt: r.ACreatedAt, UpdatedAt: r.AUpdatedAt,
			}
		}
		if r.VName.Valid {
			variants[pk] = append(variants[pk], dbgen.AssetVariant{
				AssetID: r.AID, Name: r.VName.String, WidthPx: r.VWidthPx, HeightPx: r.VHeightPx,
				StorageKey: r.VStorageKey.String, ByteCount: r.VByteCount.Int32,
			})
		}
	}
	for pk, p := range photos {
		out[pk] = photoViewOf(p, assets[pk], variants[pk])
	}
	return out, nil
}

// ---- small narg helpers -------------------------------------------------------

func tsNarg(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func textNarg(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func nullDateSource(s *string) dbgen.NullPhotoDateSource {
	if s == nil {
		return dbgen.NullPhotoDateSource{}
	}
	return dbgen.NullPhotoDateSource{PhotoDateSource: dbgen.PhotoDateSource(strings.ToUpper(*s)), Valid: true}
}

func nullViewpoint(s *string) dbgen.NullPhotoViewpoint {
	if s == nil {
		return dbgen.NullPhotoViewpoint{}
	}
	return dbgen.NullPhotoViewpoint{PhotoViewpoint: dbgen.PhotoViewpoint(strings.ToUpper(*s)), Valid: true}
}

func boolVal(b *bool) bool { return b != nil && *b }

func problemWithRevision(p Problem, rev int32) Problem {
	p.CurrentRevision = &rev
	return p
}
