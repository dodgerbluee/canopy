// Package media implements the upload lifecycle (T13): reserveUpload
// claims quota and mints an opaque storage key; the HTTP upload handler
// (transport/http) streams bytes into the ObjectStore; finalizeUpload
// verifies the recorded checksum and transactionally marks PROCESSING
// and enqueues the processor job. No image decoding happens here —
// bytes are only hashed and sniffed.
//
// Failure/orphan states follow 03: a file that reached the store before
// its DB transition is an orphan candidate; a DB row in PROCESSING
// without a file is a recoverable failure. ExpireReservations sweeps
// unused 24h reservations to DELETING and removes their staged objects.
package media

import (
	"canopy/server/internal/agentcontext"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
)

// Limits from 03 "HTTP and upload protocol".
const (
	MaxPhotoBytes   int64 = 25 << 20 // 25 MiB
	MaxReceiptBytes int64 = 20 << 20 // 20 MiB
	DefaultQuota    int64 = 10 << 30 // 10 GiB per workspace
	ReservationTTL        = 24 * time.Hour
)

// Problem codes (mirror the frozen ProblemCode enum).
const (
	CodeValidation          = "VALIDATION"
	CodeConflict            = "CONFLICT"
	CodeNotFound            = "NOT_FOUND"
	CodeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"
	CodeResourceGone        = "RESOURCE_GONE"
)

// ReceiptRetention is how long idempotency receipts live: 30 days
// (03-contracts deletion matrix: op/key/input hash are retained until the
// original TTL even after the stored response becomes RESOURCE_GONE).
const ReceiptRetention = 30 * 24 * time.Hour

// Problem is a payload-level result (same shape as the other services).
type Problem struct {
	Code            string
	Field           string
	Message         string
	CurrentRevision *int32
}

func problem(code, field, msg string) Problem {
	return Problem{Code: code, Field: field, Message: msg}
}

// Actor identifies who is performing a use case.
type Actor struct {
	WorkspaceID pgtype.UUID
	OwnerID     pgtype.UUID
	SessionID   pgtype.UUID
}

// Variant is a produced derivative (empty until the processor runs).
type Variant struct {
	Name     string
	WidthPx  *int32
	HeightPx *int32
}

// AssetView is the transport-neutral asset projection used by payloads
// and idempotency receipts.
type AssetView struct {
	ID          string     `json:"id"`
	Purpose     string     `json:"purpose"`
	State       string     `json:"state"`
	ByteCount   int64      `json:"byteCount"`
	MimeType    *string    `json:"mimeType,omitempty"`
	Sha256      string     `json:"sha256"`
	DisplayName *string    `json:"displayName,omitempty"`
	ErrorCode   *string    `json:"errorCode,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	Variants    []Variant  `json:"variants"`
}

// ProcessAssetArgs is the queue link: inserted in the same transaction
// that marks an asset PROCESSING, so a rollback leaves neither.
type ProcessAssetArgs struct {
	AssetID string `json:"asset_id"`
}

func (ProcessAssetArgs) Kind() string { return "media:process_asset" }

// Service owns the lifecycle. Queue may be nil only in unit contexts;
// production wiring always supplies the River client.
type Service struct {
	Pool       *pgxpool.Pool
	Log        *slog.Logger
	Store      platformmedia.Store
	Queue      *platform.RiverClient
	QuotaBytes int64

	uuid  platform.UUIDv4
	clock platform.SystemClock
}

func New(pool *pgxpool.Pool, log *slog.Logger, store platformmedia.Store, queue *platform.RiverClient, quota int64) *Service {
	if quota <= 0 {
		quota = DefaultQuota
	}
	return &Service{Pool: pool, Log: log, Store: store, Queue: queue, QuotaBytes: quota}
}

func (s *Service) q() *dbgen.Queries { return dbgen.New(s.Pool) }

// mutate runs fn inside one transaction with the same advisory-lock +
// mutation_receipts protocol as the collection/purchases services, kept
// package-local so domain packages stay acyclic. fn also receives the
// tx so it can enqueue queue jobs atomically.
func mutate[T any](s *Service, ctx context.Context, a Actor, op string, cmid *string, input any,
	fn func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (T, []Problem, bool),
) (T, []Problem, error) {
	var zero T
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
			if rec.Gone {
				return zero, []Problem{problem(CodeResourceGone, "clientMutationId",
					"the resource this stored response referred to has been purged.")}, nil
			}
			var replay T
			if err := json.Unmarshal(rec.Result, &replay); err != nil {
				return zero, nil, fmt.Errorf("receipt decode: %w", err)
			}
			return replay, nil, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return zero, nil, err
		}
	}

	res, problems, keep := fn(ctx, q, tx)
	if len(problems) > 0 {
		// keep lets a fn persist an honest state transition (e.g.
		// FAILED/OBJECT_MISSING) alongside a problem response
		if keep {
			if err := tx.Commit(ctx); err != nil {
				return zero, nil, err
			}
		}
		return zero, problems, nil
	}

	if key != "" {
		resultJSON, err := json.Marshal(res)
		if err != nil {
			return zero, nil, err
		}
		if _, err := q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{
			ID:             s.uuid.New(),
			WorkspaceID:    a.WorkspaceID,
			Operation:      op,
			IdempotencyKey: key,
			RequestHash:    requestHash(input),
			Result:         resultJSON,
			ExpiresAt:      pgtype.Timestamptz{Time: s.clock.Now().Add(ReceiptRetention), Valid: true},
		}); err != nil {
			return zero, nil, err
		}
	}
	if err := agentcontext.Complete(ctx, tx, res); err != nil {
		return zero, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, nil, err
	}
	return res, nil, nil
}

func requestHash(input any) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(input)
	return hex.EncodeToString(h.Sum(nil))
}

// ---- reserve ---------------------------------------------------------------

type ReserveInput struct {
	Purpose      string
	Filename     string
	ClaimedBytes int64
}

// Reserve claims quota and creates the RESERVED row with an opaque
// storage key. Quota accounting counts the claim itself: usage plus the
// new claim must fit while the workspace row is locked, so concurrent
// reservations can never collectively exceed quota.
func (s *Service) Reserve(ctx context.Context, a Actor, in ReserveInput, cmid *string) (AssetView, []Problem, error) {
	return mutate(s, ctx, a, "reserveUpload", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, _ pgx.Tx) (AssetView, []Problem, bool) {
			if in.Purpose != "PHOTO" && in.Purpose != "RECEIPT" {
				return AssetView{}, []Problem{problem(CodeValidation, "input.purpose",
					"purpose must be PHOTO or RECEIPT")}, false
			}
			max := MaxPhotoBytes
			if in.Purpose == "RECEIPT" {
				max = MaxReceiptBytes
			}
			if in.ClaimedBytes <= 0 || in.ClaimedBytes > max {
				return AssetView{}, []Problem{problem(CodeValidation, "input.claimedBytes",
					fmt.Sprintf("claimedBytes must be between 1 and %d for %s", max, in.Purpose))}, false
			}

			if _, err := q.LockWorkspaceForQuota(ctx, a.WorkspaceID); err != nil {
				return AssetView{}, []Problem{s.internalProblem(err)}, false
			}
			used, err := q.WorkspaceStorageUsed(ctx, a.WorkspaceID)
			if err != nil {
				return AssetView{}, []Problem{s.internalProblem(err)}, false
			}
			if used+in.ClaimedBytes > s.QuotaBytes {
				return AssetView{}, []Problem{problem(CodeValidation, "input.claimedBytes",
					fmt.Sprintf("storage quota exceeded: workspace holds %d of %d bytes; this upload claims %d",
						used, s.QuotaBytes, in.ClaimedBytes))}, false
			}

			id := s.uuid.New()
			key := s.uuid.New() // opaque storage key; never derived from input
			asset, err := q.InsertAsset(ctx, dbgen.InsertAssetParams{
				ID:          id,
				WorkspaceID: a.WorkspaceID,
				Purpose:     dbgen.AssetPurpose(in.Purpose),
				ByteCount:   int32(in.ClaimedBytes),
				Sha256:      placeholderSha(), // replaced by the real digest on upload
				StorageKey:  uuidString(key),
				DisplayName: textPtr(SanitizeFilename(in.Filename)),
				ExpiresAt:   pgtype.Timestamptz{Time: s.clock.Now().Add(ReservationTTL), Valid: true},
			})
			if err != nil {
				return AssetView{}, []Problem{s.internalProblem(err)}, false
			}
			return viewOf(asset, nil), nil, false
		})
}

// placeholderSha satisfies the NOT NULL checksum column while no bytes
// exist yet; MarkAssetUploaded replaces it with the real digest.
func placeholderSha() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func viewOf(a dbgen.Asset, variants []dbgen.AssetVariant) AssetView {
	v := AssetView{
		ID:        uuidString(a.ID),
		Purpose:   string(a.Purpose),
		State:     string(a.State),
		ByteCount: int64(a.ByteCount),
		Sha256:    a.Sha256,
		Variants:  []Variant{},
	}
	if a.MimeType.Valid {
		v.MimeType = &a.MimeType.String
	}
	if a.DisplayName.Valid {
		v.DisplayName = &a.DisplayName.String
	}
	if a.ErrorCode.Valid {
		v.ErrorCode = &a.ErrorCode.String
	}
	if a.ExpiresAt.Valid {
		v.ExpiresAt = &a.ExpiresAt.Time
	}
	if a.CreatedAt.Valid {
		v.CreatedAt = a.CreatedAt.Time
	}
	if a.UpdatedAt.Valid {
		v.UpdatedAt = a.UpdatedAt.Time
	}
	for _, vr := range variants {
		var w, h *int32
		if vr.WidthPx.Valid {
			w = &vr.WidthPx.Int32
		}
		if vr.HeightPx.Valid {
			h = &vr.HeightPx.Int32
		}
		v.Variants = append(v.Variants, Variant{Name: vr.Name, WidthPx: w, HeightPx: h})
	}
	return v
}

func (s *Service) internalProblem(err error) Problem {
	s.Log.Error("media mutation", "err", err)
	return Problem{Code: CodeConflict, Field: "", Message: "upload operation failed"}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	h := hex.EncodeToString(u.Bytes[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func parseUUID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}

func textPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
