package identity

import (
	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func accountFixture(t *testing.T) (*Users, User) {
	t.Helper()
	pool, q := testDB(t)
	_, sm := newOwnerService(q, newFakeClock())
	s := NewUsers(pool, sm)
	s.Hasher = NewPasswordHasher(testParams)
	u, _, err := s.Setup(context.Background(), SetupInput{Username: "gardener", DisplayName: "Gardener", Password: "garden-passphrase", WorkspaceName: "My trees", Timezone: "America/Chicago", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	return s, u
}
func TestBrowserSetupAtomicRace(t *testing.T) {
	pool, q := testDB(t)
	_, sm := newOwnerService(q, newFakeClock())
	s := NewUsers(pool, sm)
	s.Hasher = NewPasswordHasher(testParams)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Setup(ctx, SetupInput{Username: "admin", DisplayName: "Admin", Password: "garden-passphrase", WorkspaceName: "Trees", Timezone: "UTC", Currency: "USD"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrOwnerExists) {
			t.Fatalf("unexpected setup error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("setup successes=%d", successes)
	}
	var owners, users, gardens int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM owner),(SELECT count(*) FROM auth_users),(SELECT count(*) FROM workspace)").Scan(&owners, &users, &gardens); err != nil {
		t.Fatal(err)
	}
	if owners != 1 || users != 1 || gardens != 1 {
		t.Fatalf("partial setup: %d %d %d", owners, users, gardens)
	}
}
func TestAccountsPreserveCLIAndLegacySessions(t *testing.T) {
	pool, q := testDB(t)
	svc, sm := newOwnerService(q, newFakeClock())
	ctx := context.Background()
	owner, _, err := svc.Bootstrap(ctx, BootstrapInput{Password: "old-owner-password"})
	if err != nil {
		t.Fatal(err)
	}
	users := NewUsers(pool, sm)
	users.Hasher = NewPasswordHasher(testParams)
	u, sess, err := users.Login(ctx, "", "old-owner-password")
	if err != nil || !u.Primary || u.Role != "ADMIN" {
		t.Fatalf("legacy owner login: %+v %v", u, err)
	}
	legacy, err := sm.New(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = users.ForSession(ctx, legacy.Session); err != nil {
		t.Fatal(err)
	}
	// Recovery restores a disabled/demoted primary and revokes all sessions.
	if _, err = pool.Exec(ctx, "UPDATE auth_users SET disabled=true,role='MEMBER' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Recover(ctx, "recovered-password"); err != nil {
		t.Fatal(err)
	}
	u, _, err = users.Login(ctx, "owner", "recovered-password")
	if err != nil || u.Disabled || u.Role != "ADMIN" {
		t.Fatalf("recovery: %+v %v", u, err)
	}
	for _, token := range []string{sess.Token, legacy.Token} {
		if _, err = sm.Verify(ctx, token); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("recovery left session usable: %v", err)
		}
	}
}
func TestAccountPermissionsLastAdminAndRevocation(t *testing.T) {
	s, admin := accountFixture(t)
	ctx := context.Background()
	owner := parseTestID(admin.ID)
	member, err := s.Edit(ctx, owner, owner, pgtype.UUID{}, UserInput{Username: "helper", DisplayName: "Helper", Role: "MEMBER", Password: "helper-passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	memberID := parseTestID(member.ID)
	_, sess, err := s.Login(ctx, "helper", "helper-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Edit(ctx, owner, memberID, owner, UserInput{Username: "gardener", DisplayName: "Gardener", Role: "MEMBER"}); !errors.Is(err, ErrAccess) {
		t.Fatalf("member escalated: %v", err)
	}
	if _, err = s.Edit(ctx, owner, owner, owner, UserInput{Username: "gardener", DisplayName: "Gardener", Role: "MEMBER"}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin demoted: %v", err)
	}
	if _, err = s.Edit(ctx, owner, owner, owner, UserInput{Username: "gardener", DisplayName: "Gardener", Role: "ADMIN", Disabled: true}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin disabled: %v", err)
	}
	_, err = s.Edit(ctx, owner, owner, memberID, UserInput{Username: "helper", DisplayName: "Helper", Role: "MEMBER", Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sessions.Verify(ctx, sess.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("disabled session survived: %v", err)
	}
	if _, _, err = s.Login(ctx, "helper", "helper-passphrase"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("disabled login accepted: %v", err)
	}
	// Re-enabling never resurrects the old session.
	if _, err = s.Edit(ctx, owner, owner, memberID, UserInput{Username: "helper", DisplayName: "Helper", Role: "MEMBER", Password: "new-helper-passphrase"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sessions.Verify(ctx, sess.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("revoked session resurrected")
	}
	_, fresh, err := s.Login(ctx, "helper", "new-helper-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangePassword(ctx, owner, memberID, "new-helper-passphrase", "next-helper-passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sessions.Verify(ctx, fresh.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("password change kept old session")
	}
	if _, _, err = s.Login(ctx, "gardener", "garden-passphrase"); err != nil {
		t.Fatalf("member change altered owner password: %v", err)
	}
}
func parseTestID(v string) pgtype.UUID { var out pgtype.UUID; _ = out.Scan(v); return out }

func TestUpgradeExistingGardenAndSessionFromVersion16(t *testing.T) {
	pool, q := testDB(t)
	ctx := context.Background()
	migration, err := candb.Migrations.ReadFile("migrations/00017_users_oidc.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	hash, err := NewPasswordHasher(testParams).Hash("original-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: uid(), PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{ID: uid(), OwnerID: owner.ID, DisplayName: "Existing trees", Timezone: "America/Chicago", DefaultCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO sessions(id,owner_id,token_hash,csrf_token,expires_at) VALUES($1,$2,$3,'old-csrf',now()+interval '1 day')", uid(), owner.ID, HashToken("old-cookie")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	_, sm := newOwnerService(q, newFakeClock())
	users := NewUsers(pool, sm)
	user, sess, err := users.Login(ctx, "owner", "original-owner-password")
	if err != nil || user.ID != uuidString(owner.ID) || sess.Session.OwnerID != owner.ID {
		t.Fatalf("upgrade changed identity: %v", err)
	}
	old, err := sm.Verify(ctx, "old-cookie")
	if err != nil || old.UserID != owner.ID {
		t.Fatalf("existing session not preserved: %v", err)
	}
	restored, err := q.GetWorkspace(ctx, owner.ID)
	if err != nil || restored.ID != ws.ID || restored.DisplayName != "Existing trees" {
		t.Fatalf("upgrade changed garden: %v", err)
	}
}

func TestSetupTenCharacterPasswordAndUsernameDisplayDefault(t *testing.T) {
	pool, q := testDB(t)
	_, sm := newOwnerService(q, newFakeClock())
	users := NewUsers(pool, sm)
	users.Hasher = NewPasswordHasher(testParams)
	ctx := context.Background()
	for _, password := range []string{"123456789", "🌱🌱🌱🌱🌱🌱🌱🌱🌱"} {
		if _, err := users.password(password); !errors.Is(err, ErrInput) {
			t.Fatalf("accepted fewer than ten characters: %v", err)
		}
	}
	for _, password := range []string{"1234567890", "🌱🌱🌱🌱🌱🌱🌱🌱🌱🌱"} {
		if _, err := users.password(password); err != nil {
			t.Fatalf("rejected ten characters: %v", err)
		}
	}
	u, _, err := users.Setup(ctx, SetupInput{Username: "gardener", Password: "1234567890", WorkspaceName: "My garden", Timezone: "UTC", Currency: "USD"})
	if err != nil || u.DisplayName != "gardener" {
		t.Fatalf("setup without name: %+v %v", u, err)
	}
	if _, _, err = users.Login(ctx, "gardener", "1234567890"); err != nil {
		t.Fatal(err)
	}
}
