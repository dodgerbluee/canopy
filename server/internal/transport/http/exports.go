// Authorized export download (T38.a): GET /api/exports/{id}/download.
//
// Rules from 03 "deletion matrix" and 05: owner-scoped by job row, only
// READY jobs with bytes are streamed, expiry is enforced lazily at the
// gate (job flips to EXPIRED and the archive file is deleted), revoked
// and failed jobs answer honestly with their code, and the id is a
// canonical UUID — never a path.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	"canopy/server/internal/portability"
)

type ExportDelivery struct {
	DB         *dbgen.Queries
	ExportsDir string
	Log        *slog.Logger
}

func NewExportDelivery(pool *pgxpool.Pool, exportsDir string, log *slog.Logger) *ExportDelivery {
	return &ExportDelivery{DB: dbgen.New(pool), ExportsDir: exportsDir, Log: log}
}

// Handler returns the download endpoint; mount behind RequirePrincipal.
func (e *ExportDelivery) Handler() http.HandlerFunc { return e.handle }

func (e *ExportDelivery) handle(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		platform.WriteProblem(w, r, e.Log, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		e.problem(w, r, http.StatusNotFound, "NOT_FOUND", "No such export")
		return
	}
	uid := toUUID(id.String())
	job, err := e.DB.GetExportJob(r.Context(), dbgen.GetExportJobParams{ID: uid, WorkspaceID: p.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		e.problem(w, r, http.StatusNotFound, "NOT_FOUND", "No such export")
		return
	} else if err != nil {
		platform.WriteProblem(w, r, e.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}

	switch job.State {
	case dbgen.ExportStateQUEUED, dbgen.ExportStateRUNNING:
		e.json(w, http.StatusAccepted, map[string]any{"state": string(job.State)})
		return
	case dbgen.ExportStateFAILED:
		code := "EXPORT_FAILED"
		if job.ErrorCode.Valid {
			code = job.ErrorCode.String
		}
		e.json(w, http.StatusGone, map[string]any{"state": string(job.State), "code": code})
		return
	case dbgen.ExportStateEXPIRED:
		e.json(w, http.StatusGone, map[string]any{"state": string(job.State), "code": "EXPORT_EXPIRED"})
		return
	}

	// READY: the operator-policy TTL is enforced lazily here
	if job.ExpiresAt.Valid && !job.ExpiresAt.Time.After(platform.SystemClock{}.Now()) {
		if _, err := e.DB.UpdateExportJobState(r.Context(), dbgen.UpdateExportJobStateParams{
			State: dbgen.ExportStateEXPIRED, ID: uid, WorkspaceID: p.WorkspaceID,
		}); err != nil {
			e.Log.Error("export expiry flip failed", "export", id, "err", err)
		}
		e.removeArchive(id.String())
		e.json(w, http.StatusGone, map[string]any{"state": "EXPIRED", "code": "EXPORT_EXPIRED"})
		return
	}

	path, err := portability.ExportArchivePath(e.ExportsDir, id.String())
	if err != nil {
		e.problem(w, r, http.StatusNotFound, "NOT_FOUND", "No such export")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// bytes vanished under a READY job: flip honestly, never 404-lie
			if _, err := e.DB.UpdateExportJobState(r.Context(), dbgen.UpdateExportJobStateParams{
				State: dbgen.ExportStateFAILED, ErrorCode: pgtext("ARCHIVE_MISSING"),
				ID: uid, WorkspaceID: p.WorkspaceID,
			}); err != nil {
				e.Log.Error("export archive-missing flip failed", "export", id, "err", err)
			}
			e.json(w, http.StatusGone, map[string]any{"state": "FAILED", "code": "ARCHIVE_MISSING"})
			return
		}
		platform.WriteProblem(w, r, e.Log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", `attachment; filename="canopy-export-`+id.String()+`.tar"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if job.ManifestSha256.Valid {
		w.Header().Set("ETag", `"`+job.ManifestSha256.String+`"`)
	}
	if _, err := io.Copy(w, f); err != nil {
		e.Log.Error("export download interrupted", "export", id, "err", err)
	}
}

func (e *ExportDelivery) removeArchive(idStr string) {
	path, err := portability.ExportArchivePath(e.ExportsDir, idStr)
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		e.Log.Error("export archive delete failed", "export", idStr, "err", err)
	}
}

func (e *ExportDelivery) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (e *ExportDelivery) problem(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	platform.WriteProblem(w, r, e.Log, status, code, msg)
}
