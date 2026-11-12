package httpx

import (
	"bytes"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentcontext"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"time"
)

// AgentUploads reuses the browser's exact byte validation without accepting
// browser cookies. The caller supplies independently authenticated context.
type AgentUploadPolicy interface {
	AssetAllowed(context.Context, agentaccess.Principal, string) bool
	AgentManager() *agentaccess.Manager
}
type AgentUploads struct {
	API     AgentUploadPolicy
	Uploads *Uploads
	slots   chan struct{}
}

func NewAgentUploads(api AgentUploadPolicy, uploads *Uploads) *AgentUploads {
	return &AgentUploads{api, uploads, make(chan struct{}, 4)}
}
func (h *AgentUploads) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := agentaccess.FromContext(r.Context())
	id := r.PathValue("assetID")
	if !ok || !p.Grant.Has("media:write") || !h.API.AssetAllowed(r.Context(), p, id) {
		http.Error(w, "Upload not found", 404)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		http.Error(w, "Upload server busy", 429)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	input, _ := json.Marshal(map[string]string{"asset_id": id})
	ctx = agentcontext.With(ctx, agentcontext.Origin{GrantID: p.Grant.ID, CredentialID: p.CredentialID, WorkspaceID: p.Grant.WorkspaceID, Revision: p.Grant.Revision, RequestID: uuid.NewString(), Tool: "upload_bytes", OperationID: "bytes:" + id, Scope: "media:write", Input: input})
	tx, err := h.API.AgentManager().Pool.Begin(ctx)
	if err != nil {
		http.Error(w, "Upload unavailable", 503)
		return
	}
	defer tx.Rollback(ctx)
	if err = agentcontext.Enter(ctx, tx); err != nil {
		http.Error(w, "Upload authorization changed", 403)
		return
	}
	buffer := &agentUploadResponse{header: http.Header{}, status: 200}
	r = r.WithContext(WithPrincipal(ctx, Principal{WorkspaceID: agentaccess.ID(p.Grant.WorkspaceID), OwnerID: agentaccess.ID(p.OwnerID)}))
	h.Uploads.Handler()(buffer, r)
	if buffer.status == 200 {
		if err = agentcontext.Complete(ctx, tx, map[string]string{"id": id}); err != nil {
			http.Error(w, "Upload receipt unavailable; retry the same file", 503)
			return
		}
		if err = tx.Commit(ctx); err != nil {
			http.Error(w, "Upload receipt unavailable; retry the same file", 503)
			return
		}
	}
	for k, values := range buffer.header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(buffer.status)
	_, _ = w.Write(buffer.body.Bytes())
}

type agentUploadResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *agentUploadResponse) Header() http.Header         { return w.header }
func (w *agentUploadResponse) WriteHeader(status int)      { w.status = status }
func (w *agentUploadResponse) Write(b []byte) (int, error) { return w.body.Write(b) }

// AgentExportDelivery restricts archives to the requesting credential and reuses
// the existing download's readiness, expiry and workspace privacy checks.
func AgentExportDelivery(policy AgentUploadPolicy, delivery *ExportDelivery) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := agentaccess.FromContext(r.Context())
		if !ok || !p.Grant.Has("exports:request") || len(p.Grant.PlantIDs) > 0 {
			http.NotFound(w, r)
			return
		}
		current, err := policy.AgentManager().Current(r.Context(), p)
		if err != nil {
			http.Error(w, "Export authorization changed", 403)
			return
		}
		var allowed bool
		err = policy.AgentManager().Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM export_jobs WHERE id=$1 AND workspace_id=$2 AND agent_grant_id=$3 AND agent_credential_id=$4)`, r.PathValue("id"), current.Grant.WorkspaceID, current.Grant.ID, current.CredentialID).Scan(&allowed)
		if err != nil || !allowed {
			http.NotFound(w, r)
			return
		}
		delivery.Handler()(w, r.WithContext(WithPrincipal(r.Context(), Principal{WorkspaceID: agentaccess.ID(current.Grant.WorkspaceID), OwnerID: agentaccess.ID(current.OwnerID)})))
	}
}
