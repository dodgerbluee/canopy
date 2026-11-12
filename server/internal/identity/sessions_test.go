package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

func newManager(q *dbgen.Queries, clock *fakeClock) *SessionManager {
	return NewSessionManager(q, platform.UUIDv4{}, clock, DefaultSessionConfig(false, false))
}

func insertOwner(t *testing.T, q *dbgen.Queries) dbgen.Owner {
	t.Helper()
	o, err := q.InsertOwner(context.Background(), dbgen.InsertOwnerParams{
		ID: uid(), PasswordHash: "$argon2id$v=19$m=64,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAA",
	})
	if err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	return o
}

func TestSessionRoundTripAndTokenHashing(t *testing.T) {
	pool, q := testDB(t)
	ctx := context.Background()
	owner := insertOwner(t, q)
	m := newManager(q, newFakeClock())

	issued, err := m.New(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(issued.Token) < 40 || len(issued.CSRFToken) < 40 {
		t.Fatalf("tokens too short: %d %d", len(issued.Token), len(issued.CSRFToken))
	}

	// the cookie token itself must never be stored
	var tokenHash, csrf string
	if err := pool.QueryRow(ctx, "SELECT token_hash, csrf_token FROM sessions WHERE id=$1", issued.Session.ID).
		Scan(&tokenHash, &csrf); err != nil {
		t.Fatal(err)
	}
	if tokenHash == issued.Token || strings.Contains(tokenHash, issued.Token) {
		t.Fatal("plaintext cookie token stored")
	}
	if csrf != issued.CSRFToken {
		t.Fatal("csrf synchronizer token must be retrievable server-side")
	}

	got, err := m.Verify(ctx, issued.Token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.ID != issued.Session.ID || got.OwnerID != owner.ID {
		t.Fatal("verify resolved wrong session")
	}
	if !got.LastSeenAt.Valid {
		t.Fatal("verify must touch last_seen_at")
	}
	if _, err := m.Verify(ctx, "forged-token"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("forged token: %v", err)
	}
	if _, err := m.Verify(ctx, ""); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("empty token: %v", err)
	}
}

func TestIdleExpiryRevokesRow(t *testing.T) {
	pool, q := testDB(t)
	ctx := context.Background()
	owner := insertOwner(t, q)
	clock := newFakeClock()
	m := newManager(q, clock)

	issued, _ := m.New(ctx, owner.ID)
	if _, err := m.Verify(ctx, issued.Token); err != nil {
		t.Fatal(err)
	}
	clock.Advance(12*time.Hour + time.Minute)
	if _, err := m.Verify(ctx, issued.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("idle session accepted: %v", err)
	}
	var revoked bool
	if err := pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$1", issued.Session.ID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Fatal("idle-expired session must be parked as revoked")
	}
}

func TestAbsoluteExpiry(t *testing.T) {
	_, q := testDB(t)
	ctx := context.Background()
	owner := insertOwner(t, q)
	clock := newFakeClock()
	m := newManager(q, clock)

	issued, _ := m.New(ctx, owner.ID)
	clock.Advance(7*24*time.Hour + time.Minute)
	if _, err := m.Verify(ctx, issued.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("absolutely expired session accepted: %v", err)
	}
}

func TestRevokeScopedToOwner(t *testing.T) {
	_, q := testDB(t)
	ctx := context.Background()
	ownerA := insertOwner(t, q)
	ownerB := insertOwner(t, q)
	m := newManager(q, newFakeClock())

	issued, _ := m.New(ctx, ownerA.ID)

	// cross-owner revoke attempt must be a no-op (cross-workspace safety)
	if err := m.Revoke(ctx, issued.Session.ID, ownerB.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-owner revoke: %v", err)
	}
	if _, err := m.Verify(ctx, issued.Token); err != nil {
		t.Fatalf("session revoked by another owner: %v", err)
	}
	if err := m.Revoke(ctx, issued.Session.ID, ownerA.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if _, err := m.Verify(ctx, issued.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("revoked session still verifies: %v", err)
	}
}

func TestRevokeAllAndRotation(t *testing.T) {
	_, q := testDB(t)
	ctx := context.Background()
	owner := insertOwner(t, q)
	m := newManager(q, newFakeClock())

	a, _ := m.New(ctx, owner.ID)
	b, _ := m.New(ctx, owner.ID)
	if a.Token == b.Token || a.Session.ID == b.Session.ID {
		t.Fatal("sessions must rotate: tokens/ids reused")
	}
	if err := m.RevokeAll(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{a.Token, b.Token} {
		if _, err := m.Verify(ctx, tok); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("session survived RevokeAll: %v", err)
		}
	}
}

func TestMatchesCSRF(t *testing.T) {
	s := dbgen.Session{CsrfToken: "expected-token"}
	if !MatchesCSRF(s, "expected-token") {
		t.Fatal("valid csrf rejected")
	}
	if MatchesCSRF(s, "") || MatchesCSRF(s, "wrong") {
		t.Fatal("invalid csrf accepted")
	}
}
