package insights

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentcontext"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestAgentAuthoritySurvivesWorkerClaimAndRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		name := "active"
		if revoke {
			name = "revoked"
		}
		t.Run(name, func(t *testing.T) {
			ctx, pool := scratch(t)
			world := seedWorld(t, ctx, pool)
			fake := newFakePort()
			service := newSvc(pool, defaultLimits(), fake)
			manager := agentaccess.New(pool)
			issued, err := manager.Save(ctx, world.WorkspaceID, agentaccess.SaveInput{ID: uuid.NewString(), Name: "Worker authority fixture", Scopes: []string{"garden:read", "analysis:run"}, ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			principal, err := manager.Verify(ctx, issued.Token)
			if err != nil {
				t.Fatal(err)
			}
			origin := agentcontext.Origin{Revision: principal.Grant.Revision, GrantID: principal.Grant.ID, CredentialID: principal.CredentialID, WorkspaceID: principal.Grant.WorkspaceID, RequestID: uuid.NewString(), Tool: "analysis_request", OperationID: "worker-test", Scope: "analysis:run", Input: []byte(`{"operation_id":"worker-test"}`)}
			run, problems, err := service.Request(agentcontext.With(ctx, origin), world.Actor, textReq(world), nil)
			noProbs(t, problems, err)
			if uuidString(run.AgentGrantID) != principal.Grant.ID {
				t.Fatal("analysis lost originating connection on insert")
			}
			if revoke {
				if _, err = manager.ChangeCredential(ctx, world.WorkspaceID, run.AgentGrantID, 1, true); err != nil {
					t.Fatal(err)
				}
			}
			if err = service.ProcessRun(ctx, jobFor(run.ID)); err != nil {
				t.Fatal(err)
			}
			done := getRun(t, ctx, pool, run.ID)
			if uuidString(done.AgentGrantID) != principal.Grant.ID {
				t.Fatal("worker claim erased originating connection")
			}
			if revoke {
				if fake.count() != 0 || done.State != dbgen.AnalysisRunStateFAILED {
					t.Fatal("revoked agent billed a provider", fake.count(), done.State)
				}
			} else if fake.count() != 1 || done.State != dbgen.AnalysisRunStateSUCCEEDED {
				t.Fatal("active agent job failed", fake.count(), done.State)
			}
		})
	}
}
