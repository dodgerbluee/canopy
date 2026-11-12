package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
)

// Uploads serves the byte side of the upload lifecycle:
// PUT /api/uploads/{assetID} behind RequirePrincipal (session + CSRF +
// Origin). Bytes are staged, hashed and size-checked against the
// reservation, MIME-sniffed from magic bytes, then atomically committed
// to the ObjectStore before the DB transition. No image decoding here.
type Uploads struct {
	DB    *dbgen.Queries
	Store platformmedia.Store
	Log   *slog.Logger

	// Maintenance, when set and true, refuses uploads with 503 while
	// the stack is quiesced for a backup (T38.a).
	Maintenance func(ctx context.Context) bool
}

// NewUploads wires the handler from the pool so cmd packages never
// import the generated db layer directly.
func NewUploads(pool *pgxpool.Pool, store platformmedia.Store, log *slog.Logger) *Uploads {
	return &Uploads{DB: dbgen.New(pool), Store: store, Log: log}
}

// uploadResponse is the JSON receipt the client needs to finalize.
type uploadResponse struct {
	AssetID   string `json:"assetId"`
	State     string `json:"state"`
	ByteCount int64  `json:"byteCount"`
	SHA256    string `json:"sha256"`
	MIMEType  string `json:"mimeType"`
}

// Handler returns the upload endpoint; callers mount it behind
// auth.RequirePrincipal so session, CSRF and Origin checks all apply.
func (u *Uploads) Handler() http.HandlerFunc { return u.handle }

func (u *Uploads) handle(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		platform.WriteProblem(w, r, u.Log, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	if u.Maintenance != nil && u.Maintenance(r.Context()) {
		platform.WriteProblem(w, r, u.Log, http.StatusServiceUnavailable, "MAINTENANCE",
			"The system is in maintenance mode; uploads are paused")
		return
	}
	rawID := r.PathValue("assetID")
	if _, err := uuid.Parse(rawID); err != nil {
		platform.WriteProblem(w, r, u.Log, http.StatusNotFound, "NOT_FOUND", "upload not found")
		return
	}
	asset, err := u.DB.GetAsset(r.Context(), dbgen.GetAssetParams{ID: toUUID(rawID), WorkspaceID: p.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		// cross-owner and unknown ids are indistinguishable on purpose
		platform.WriteProblem(w, r, u.Log, http.StatusNotFound, "NOT_FOUND", "upload not found")
		return
	} else if err != nil {
		platform.WriteProblem(w, r, u.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}

	switch asset.State {
	case dbgen.AssetStateRESERVED:
		u.acceptBytes(w, r, p, asset)
	case dbgen.AssetStateUPLOADED:
		u.replayCheck(w, r, asset)
	default:
		platform.WriteProblem(w, r, u.Log, http.StatusConflict, "CONFLICT",
			"this upload is no longer accepting bytes")
	}
}

// acceptBytes streams a fresh reservation: exact size, sniffed type,
// fsynced staging, atomic rename, then the guarded DB transition.
func (u *Uploads) acceptBytes(w http.ResponseWriter, r *http.Request, p Principal, asset dbgen.Asset) {
	claimed := int64(asset.ByteCount)
	staged, err := u.Store.Stage(asset.StorageKey, r.Body, claimed)
	switch {
	case errors.Is(err, platformmedia.ErrTooLarge):
		_ = u.Store.Discard(asset.StorageKey)
		platform.WriteProblem(w, r, u.Log, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE",
			"received more than the claimed byte count")
		return
	case err != nil:
		// client disconnected mid-stream: staged slot removed, the
		// reservation stays RESERVED and can be retried until expiry
		_ = u.Store.Discard(asset.StorageKey)
		if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
			u.Log.Info("upload interrupted by client", "asset_id", asset.ID)
			return
		}
		platform.WriteProblem(w, r, u.Log, http.StatusBadRequest, "UPLOAD_INTERRUPTED",
			"request body could not be read")
		return
	}
	if staged.Size != claimed {
		_ = u.Store.Discard(asset.StorageKey)
		platform.WriteProblem(w, r, u.Log, http.StatusBadRequest, "UPLOAD_TRUNCATED",
			"received fewer bytes than claimed")
		return
	}
	mime := media.SniffMIME(staged.Head)
	if !media.MIMEAllowed(string(asset.Purpose), mime) {
		_ = u.Store.Discard(asset.StorageKey)
		if _, dbErr := u.DB.MarkAssetState(r.Context(), dbgen.MarkAssetStateParams{
			ID: asset.ID, WorkspaceID: p.WorkspaceID,
			State:     dbgen.AssetStateFAILED,
			ErrorCode: pgtext("UNSUPPORTED_MEDIA_TYPE"),
		}); dbErr != nil {
			u.Log.Error("mark failed after sniff", "err", dbErr)
		}
		platform.WriteProblem(w, r, u.Log, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
			"content type is not allowed for this purpose")
		return
	}

	if err := u.Store.Commit(asset.StorageKey); err != nil {
		_ = u.Store.Discard(asset.StorageKey)
		platform.WriteProblem(w, r, u.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}

	updated, err := u.DB.MarkAssetUploaded(r.Context(), dbgen.MarkAssetUploadedParams{
		ID: asset.ID, WorkspaceID: p.WorkspaceID,
		ByteCount: int32(staged.Size), MimeType: pgtext(mime), Sha256: staged.SHA256,
	})
	if err != nil {
		// file-before-DB boundary: the committed object is an orphan
		// candidate; the reservation is still RESERVED so a retry
		// re-streams over the same key, and the sweeper removes it if
		// the reservation expires first.
		u.Log.Error("upload DB transition failed after commit (orphan candidate)",
			"asset_id", asset.ID, "err", err)
		platform.WriteProblem(w, r, u.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	writeUploadOK(w, updated)
}

// replayCheck handles a second PUT to an already-UPLOADED asset:
// identical checksum replays the success, different bytes conflict.
// The stored object is never overwritten.
func (u *Uploads) replayCheck(w http.ResponseWriter, r *http.Request, asset dbgen.Asset) {
	staged, err := u.Store.Stage(asset.StorageKey, r.Body, int64(asset.ByteCount))
	if err != nil {
		_ = u.Store.Discard(asset.StorageKey)
		if errors.Is(err, platformmedia.ErrTooLarge) {
			platform.WriteProblem(w, r, u.Log, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE",
				"received more than the claimed byte count")
			return
		}
		platform.WriteProblem(w, r, u.Log, http.StatusBadRequest, "UPLOAD_INTERRUPTED",
			"request body could not be read")
		return
	}
	_ = u.Store.Discard(asset.StorageKey) // staged copy is only hashed, never committed
	if staged.SHA256 == asset.Sha256 {
		writeUploadOK(w, asset)
		return
	}
	platform.WriteProblem(w, r, u.Log, http.StatusConflict, "CONFLICT",
		"this upload already holds different bytes")
}

func writeUploadOK(w http.ResponseWriter, a dbgen.Asset) {
	mime := ""
	if a.MimeType.Valid {
		mime = a.MimeType.String
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(uploadResponse{
		AssetID:   uuidString(a.ID),
		State:     string(a.State),
		ByteCount: int64(a.ByteCount),
		SHA256:    a.Sha256,
		MIMEType:  mime,
	})
}

func toUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}

func pgtext(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}
