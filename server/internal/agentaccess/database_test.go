package agentaccess

import (
	candb "canopy/server/db"
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCredentialPersistenceAndTenantIsolation(t *testing.T) {
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	schema := fmt.Sprintf("agent_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	_, _ = admin.Exec(ctx, "SET search_path TO "+schema)
	up, _ := candb.AllUp()
	if _, err = admin.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	owner := uuid.NewString()
	ws := ID(uuid.NewString())
	other := ID(uuid.NewString())
	_, err = pool.Exec(ctx, `INSERT INTO owner(id,password_hash) VALUES($1,'fixture')`, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{String(ws), String(other)} {
		_, err = pool.Exec(ctx, `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Fixture','UTC','USD')`, id, owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	m := New(pool)
	in := SaveInput{ID: uuid.NewString(), Name: "Fixture Hermes", Scopes: []string{"garden:read", "logs:write"}, ExpiresAt: time.Now().Add(time.Hour)}
	issued, err := m.Save(ctx, ws, in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Verify(ctx, issued.Token)
	if err != nil || p.Grant.ID != in.ID {
		t.Fatal("verification", err)
	}
	if _, err = m.Get(ctx, other, ID(in.ID)); err == nil {
		t.Fatal("tenant escape")
	}
	if _, err = m.Verify(ctx, issued.Token+"x"); err == nil {
		t.Fatal("invalid token accepted")
	}
	var verifier []byte
	if err = pool.QueryRow(ctx, `SELECT verifier FROM agent_credentials WHERE id=$1`, ID(p.CredentialID)).Scan(&verifier); err != nil || strings.Contains(string(verifier), issued.Token) {
		t.Fatal("plaintext credential persisted")
	}
	rotated, err := m.ChangeCredential(ctx, ws, ID(in.ID), 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Verify(ctx, issued.Token); err == nil {
		t.Fatal("rotated key still works")
	}
	if _, err = m.Verify(ctx, rotated.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ChangeCredential(ctx, ws, ID(in.ID), rotated.Grant.Revision, true); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Verify(ctx, rotated.Token); err == nil {
		t.Fatal("revoked key accepted")
	}
}
