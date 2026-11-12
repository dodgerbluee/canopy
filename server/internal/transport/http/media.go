// Authorized media delivery (T15): GET /api/media/{assetID}/{variant}.
//
// Rules from 03 "HTTP and upload protocol":
//   - authenticated owner only; cross-workspace, unknown, non-READY and
//     wrong-purpose requests are all plain 404s (no existence leaks);
//   - only the four fixed variant names are ever accepted — storage keys
//     and paths are never client input;
//   - receipt assets are attachment-only (original variant only exists);
//   - safe Content-Type (derivatives are always image/jpeg),
//     Content-Disposition (inline for photo display derivatives,
//     attachment for originals and receipts), nosniff,
//     Cache-Control: private, no-cache, and a deterministic ETag.
package httpx

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
)

// mediaVariants is the frozen derivative set; anything else is a 404.
var mediaVariants = map[string]bool{"thumb": true, "medium": true, "display": true, "original": true}

type MediaDelivery struct {
	DB    *dbgen.Queries
	Store platformmedia.Store
	Log   *slog.Logger
}

// NewMediaDelivery wires the handler from the pool so cmd packages never
// import the generated db layer directly.
func NewMediaDelivery(pool *pgxpool.Pool, store platformmedia.Store, log *slog.Logger) *MediaDelivery {
	return &MediaDelivery{DB: dbgen.New(pool), Store: store, Log: log}
}

// Handler returns the delivery endpoint; mount behind RequirePrincipal.
func (m *MediaDelivery) Handler() http.HandlerFunc { return m.handle }

func (m *MediaDelivery) handle(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		platform.WriteProblem(w, r, m.Log, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	variant := r.PathValue("variant")
	rawID := r.PathValue("assetID")
	id, err := uuid.Parse(rawID)
	if err != nil || !mediaVariants[variant] {
		m.notFound(w, r)
		return
	}

	asset, err := m.DB.GetAssetByID(r.Context(), toUUID(id.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		m.notFound(w, r) // unknown or other-workspace: indistinguishable
		return
	} else if err != nil {
		platform.WriteProblem(w, r, m.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	if p.WorkspaceID != asset.WorkspaceID {
		m.notFound(w, r)
		return
	}
	if asset.State != dbgen.AssetStateREADY {
		m.notFound(w, r) // in-flight, failed and deleting assets serve nothing
		return
	}
	// purpose enforcement: receipts are attachment-only and only ever
	// carry the original variant; image derivatives exist for photos only
	if asset.Purpose == dbgen.AssetPurposeRECEIPT && variant != "original" {
		m.notFound(w, r)
		return
	}

	av, err := m.DB.GetAssetVariant(r.Context(), dbgen.GetAssetVariantParams{AssetID: asset.ID, Name: variant})
	if errors.Is(err, pgx.ErrNoRows) {
		m.notFound(w, r)
		return
	} else if err != nil {
		platform.WriteProblem(w, r, m.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}

	// deterministic strong ETag: retried processing rewrites the same
	// deterministic key with identical bytes, so this stays stable
	etag := `"` + rawID + "-" + variant + "-" + strconv.FormatInt(int64(av.ByteCount), 10) + `"`
	if inm := r.Header.Get("If-None-Match"); inm == etag || inm == "W/"+etag || inm == `W/`+etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	rc, err := m.Store.Open(av.StorageKey)
	if errors.Is(err, platformmedia.ErrNotFound) {
		// row present but object gone: honest server error, never a revive
		m.Log.Error("media object missing", "asset", rawID, "variant", variant)
		platform.WriteProblem(w, r, m.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	} else if err != nil {
		platform.WriteProblem(w, r, m.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	defer rc.Close()

	h := w.Header()
	if variant == "original" {
		ct := "application/octet-stream"
		if asset.MimeType.Valid && asset.MimeType.String != "" {
			ct = asset.MimeType.String
		}
		h.Set("Content-Type", ct)
	} else {
		h.Set("Content-Type", "image/jpeg") // every derivative is re-encoded JPEG
	}
	disposition := "inline"
	if asset.Purpose == dbgen.AssetPurposeRECEIPT || variant == "original" {
		disposition = "attachment" // originals and receipts are private downloads
	}
	h.Set("Content-Disposition", disposition+`; filename="`+downloadName(asset, variant)+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	h.Set("Content-Length", strconv.FormatInt(int64(av.ByteCount), 10))
	if _, err := io.Copy(w, rc); err != nil {
		m.Log.Error("media delivery", "asset", rawID, "variant", variant, "err", err)
	}
}

func (m *MediaDelivery) notFound(w http.ResponseWriter, r *http.Request) {
	platform.WriteProblem(w, r, m.Log, http.StatusNotFound, "NOT_FOUND", "media not found")
}

// downloadName builds a safe display filename; storage keys never appear.
func downloadName(asset dbgen.Asset, variant string) string {
	name := ""
	if asset.DisplayName.Valid {
		name = asset.DisplayName.String
	}
	if variant != "original" {
		stem := strings.TrimSuffix(name, path.Ext(name))
		if stem == "" {
			stem = "photo"
		}
		name = stem + ".jpg"
	}
	if name == "" {
		if u, err := uuid.FromBytes(asset.ID.Bytes[:]); err == nil {
			name = u.String()
		} else {
			name = "asset"
		}
	}
	return name
}
