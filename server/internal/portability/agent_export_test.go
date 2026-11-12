package portability

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentcontext"
	"context"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"os"
	"testing"
	"time"
)

func TestAgentExportReplayOwnershipAndRevokedWorker(t *testing.T) {
	e := newEnv(t)
	manager := agentaccess.New(e.pool)
	issued, err := manager.Save(e.ctx, e.ws, agentaccess.SaveInput{ID: uuid.NewString(), Name: "Export fixture", Scopes: []string{"garden:read", "photos:read", "purchases:read", "receipts:read", "exports:request"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := manager.Verify(e.ctx, issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	origin := agentcontext.Origin{Revision: p.Grant.Revision, GrantID: p.Grant.ID, CredentialID: p.CredentialID, WorkspaceID: p.Grant.WorkspaceID, RequestID: uuid.NewString(), Tool: "export_request", OperationID: "export-test", Scope: "exports:request", Input: []byte(`{"operation_id":"export-test"}`)}
	ctx := agentcontext.With(context.Background(), origin)
	job, err := e.svc.RequestExport(ctx, e.ws)
	if err != nil {
		t.Fatal(err)
	}
	if uuidString(job.AgentGrantID) != p.Grant.ID || uuidString(job.AgentCredentialID) != p.CredentialID {
		t.Fatal("export lost originating authority")
	}
	replay, err := e.svc.RequestExport(ctx, e.ws)
	if err != nil || replay.ID != job.ID {
		t.Fatal("export replay duplicated job", err)
	}
	var count int
	if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='export:build'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("export queue not deduplicated", err, count)
	}
	if _, err = e.pool.Exec(ctx, `UPDATE agent_credentials SET revoked_at=now() WHERE id=$1`, p.CredentialID); err != nil {
		t.Fatal(err)
	}
	worker := &ExportBuildWorker{Svc: e.svc}
	if err = worker.Work(e.ctx, &river.Job[ExportBuildArgs]{Args: ExportBuildArgs{ExportID: uuidString(job.ID)}}); err != nil {
		t.Fatal(err)
	}
	done, err := e.svc.GetExportJob(e.ctx, uuidString(job.ID), e.ws)
	if err != nil || done.State != dbgen.ExportStateFAILED || done.ErrorCode.String != "REVOKED" {
		t.Fatal("revoked export was not fenced", err, done.State)
	}
	path, _ := ExportArchivePath(e.svc.ExportsDir(), uuidString(job.ID))
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("revoked job published archive", err)
	}
}
