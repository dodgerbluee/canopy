package agentcontext

import (
	candb "canopy/server/db"
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"os"
	"testing"
	"time"
)

func TestAtomicOperationAndGrantFence(t *testing.T) {
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN required")
	}
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	schema := fmt.Sprintf("agentctx_%d", time.Now().UnixNano())
	_, err = c.Exec(ctx, "CREATE SCHEMA "+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	_, _ = c.Exec(ctx, "SET search_path TO "+schema)
	up, _ := candb.AllUp()
	if _, err = c.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	owner, ws, grant, credential := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = c.Exec(ctx, `INSERT INTO owner(id,password_hash) VALUES($1,'fixture')`, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Exec(ctx, `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Fixture','UTC','USD')`, ws, owner)
	_, _ = c.Exec(ctx, `INSERT INTO agent_grants(id,workspace_id,name,scopes,expires_at) VALUES($1,$2,'Fixture',ARRAY['logs:write'],now()+interval '1 hour')`, grant, ws)
	_, _ = c.Exec(ctx, `INSERT INTO agent_credentials(id,workspace_id,grant_id,verifier) VALUES($1,$2,$3,'fixture')`, credential, ws, grant)
	o := Origin{Revision: 1, GrantID: grant, CredentialID: credential, WorkspaceID: ws, RequestID: uuid.NewString(), Tool: "log_watered", OperationID: "first", Scope: "logs:write", Input: []byte(`{"plant_id":"fixture"}`)}
	ctx = With(ctx, o)
	tx, _ := c.Begin(ctx)
	if err = Enter(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = Complete(ctx, tx, map[string]any{"id": "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	_ = c.QueryRow(ctx, `SELECT state FROM agent_operations WHERE grant_id=$1`, grant).Scan(&state)
	if state != "APPLIED" {
		t.Fatal(state)
	}
	if !JobAllowed(ctx, c, grant, credential, ws, "logs:write", "") {
		t.Fatal("active originating authority rejected")
	}
	if JobAllowed(ctx, c, grant, credential, ws, "analysis:run", "") || JobAllowed(ctx, c, grant, credential, uuid.NewString(), "logs:write", "") {
		t.Fatal("job authority crossed scope or workspace")
	}
	if !JobAllowed(ctx, c, "", "", ws, "analysis:run", "") {
		t.Fatal("browser job behavior changed")
	}
	o.Input = []byte(`{"plant_id":"different"}`)
	tx, _ = c.Begin(ctx)
	if Enter(With(ctx, o), tx) == nil {
		t.Fatal("operation identity accepted different input")
	}
	_ = tx.Rollback(ctx)
	_, _ = c.Exec(ctx, `UPDATE agent_grants SET revoked_at=now() WHERE id=$1`, grant)
	if JobAllowed(ctx, c, grant, credential, ws, "logs:write", "") {
		t.Fatal("revoked job authority remained active")
	}
	tx, _ = c.Begin(ctx)
	if Enter(ctx, tx) == nil {
		t.Fatal("revoked grant entered domain transaction")
	}
	_ = tx.Rollback(ctx)
}
