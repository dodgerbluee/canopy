package identity

import (
	"context"
	"errors"
	"testing"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

func newOwnerService(q *dbgen.Queries, clock *fakeClock) (*OwnerService, *SessionManager) {
	sm := newManager(q, clock)
	hasher := NewPasswordHasher(testParams)
	return NewOwnerService(q, sm, hasher, platform.UUIDv4{}, testLog), sm
}

func TestBootstrapOnceNoDefaultPassword(t *testing.T) {
	_, q := testDB(t)
	ctx := context.Background()
	svc, _ := newOwnerService(q, newFakeClock())

	// login before any owner exists: generic failure, no enumeration
	if _, _, err := svc.Login(ctx, "anything"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("pre-bootstrap login: %v", err)
	}
	// empty password bootstrap must fail: no default password path
	if _, _, err := svc.Bootstrap(ctx, BootstrapInput{Password: ""}); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("empty-password bootstrap accepted: %v", err)
	}

	owner, ws, err := svc.Bootstrap(ctx, BootstrapInput{Password: "s3cret-passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	if ws.DisplayName != "My garden" || ws.Timezone != "UTC" || ws.DefaultCurrency != "USD" {
		t.Fatalf("workspace defaults wrong: %+v", ws)
	}
	if _, _, err := svc.Bootstrap(ctx, BootstrapInput{Password: "another"}); !errors.Is(err, ErrOwnerExists) {
		t.Fatalf("second bootstrap accepted: %v", err)
	}
	// hash stored, never the plaintext
	if owner.PasswordHash == "s3cret-passphrase" {
		t.Fatal("plaintext password stored")
	}
}

func TestLoginWrongPasswordGeneric(t *testing.T) {
	_, q := testDB(t)
	ctx := context.Background()
	svc, _ := newOwnerService(q, newFakeClock())
	_, _, err := svc.Bootstrap(ctx, BootstrapInput{Password: "right-pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Login(ctx, "wrong-pw"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong password: %v", err)
	}
	owner, sess, err := svc.Login(ctx, "right-pw")
	if err != nil {
		t.Fatalf("correct password: %v", err)
	}
	if sess.Token == "" || sess.CSRFToken == "" {
		t.Fatal("login must issue cookie token and csrf token")
	}
	if _, err := svc.Workspace(ctx, owner.ID); err != nil {
		t.Fatalf("workspace lookup: %v", err)
	}
}

func TestPasswordChangeRotatesAllSessions(t *testing.T) {
	_, q := testDB(t)
	ctx := context.Background()
	svc, sm := newOwnerService(q, newFakeClock())
	owner, _, _ := svc.Bootstrap(ctx, BootstrapInput{Password: "old-pw"})

	_, first, _ := svc.Login(ctx, "old-pw")
	other, _ := sm.New(ctx, owner.ID) // e.g. a second browser
	if _, _, err := svc.ChangePassword(ctx, owner.ID, "wrong-current", "new-pw"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("bad current password: %v", err)
	}
	if _, _, err := svc.ChangePassword(ctx, owner.ID, "old-pw", ""); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("empty new password accepted: %v", err)
	}
	if _, _, err := svc.ChangePassword(ctx, owner.ID, "old-pw", "new-pw"); err != nil {
		t.Fatal(err)
	}

	// every pre-change session dies, including the caller's old cookie
	for _, tok := range []string{first.Token, other.Token} {
		if _, err := sm.Verify(ctx, tok); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("session survived password change: %v", err)
		}
	}
	// old password no longer works; new one does
	if _, _, err := svc.Login(ctx, "old-pw"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatal("old password still valid")
	}
	if _, _, err := svc.Login(ctx, "new-pw"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestRecoveryRevokesAllAndBumpsPrivacyEpoch(t *testing.T) {
	pool, q := testDB(t)
	ctx := context.Background()
	svc, sm := newOwnerService(q, newFakeClock())
	_, ws, _ := svc.Bootstrap(ctx, BootstrapInput{Password: "old-pw"})

	_, a, _ := svc.Login(ctx, "old-pw")
	_, b, _ := svc.Login(ctx, "old-pw")

	if _, err := svc.Recover(ctx, "forced-new-pw"); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{a.Token, b.Token} {
		if _, err := sm.Verify(ctx, tok); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("session survived recovery: %v", err)
		}
	}
	if _, _, err := svc.Login(ctx, "old-pw"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatal("old password still valid after recovery")
	}
	if _, _, err := svc.Login(ctx, "forced-new-pw"); err != nil {
		t.Fatalf("recovered password rejected: %v", err)
	}
	// private media URL material must rotate too
	var epoch int64
	if err := pool.QueryRow(ctx, "SELECT privacy_epoch FROM workspace WHERE id=$1", ws.ID).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if epoch != 1 {
		t.Fatalf("privacy_epoch = %d, want 1", epoch)
	}
	// recovery with no owner (fresh schema) reports ErrNoOwner
	pool2, q2 := testDB(t)
	defer pool2.Close()
	svc2, _ := newOwnerService(q2, newFakeClock())
	if _, err := svc2.Recover(ctx, "pw"); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("recovery without owner: %v", err)
	}
}
