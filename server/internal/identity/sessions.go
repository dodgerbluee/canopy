package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// SessionConfig carries the frozen session policy (02-architecture §96-100):
// 12h idle / 7d absolute expiry, HttpOnly Lax cookie, Secure forced in prod.
type SessionConfig struct {
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
	CookieName      string
	CookieSecure    bool
	TrustProxyProto bool
}

func DefaultSessionConfig(secure, trustProxy bool) SessionConfig {
	return SessionConfig{
		IdleTimeout:     12 * time.Hour,
		AbsoluteTimeout: 7 * 24 * time.Hour,
		CookieName:      "canopy_session",
		CookieSecure:    secure,
		TrustProxyProto: trustProxy,
	}
}

// SessionStore is the narrow port over the generated session queries;
// *dbgen.Queries satisfies it, tests can fake it.
type SessionStore interface {
	InsertSession(ctx context.Context, arg dbgen.InsertSessionParams) (dbgen.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (dbgen.Session, error)
	TouchSession(ctx context.Context, id pgtype.UUID) error
	RevokeSession(ctx context.Context, arg dbgen.RevokeSessionParams) (dbgen.Session, error)
	RevokeAllSessions(ctx context.Context, ownerID pgtype.UUID) error
}

var (
	ErrSessionExpired  = errors.New("session expired")
	ErrSessionNotFound = errors.New("session not found for this owner")
)

// IssuedSession returns the plaintext cookie token and CSRF synchronizer
// token exactly once, at creation. The cookie token is never stored — only
// its SHA-256 — and the CSRF token is a server-only DB value returned only
// to authenticated bootstrap (03-contracts).
type IssuedSession struct {
	Token     string
	CSRFToken string
	Session   dbgen.Session
}

type SessionManager struct {
	store SessionStore
	ids   platform.IDs
	clock platform.Clock
	cfg   SessionConfig
	rnd   io.Reader
}

func NewSessionManager(store SessionStore, ids platform.IDs, clock platform.Clock, cfg SessionConfig) *SessionManager {
	return &SessionManager{store: store, ids: ids, clock: clock, cfg: cfg, rnd: rand.Reader}
}

// New issues a fresh session. Every login creates a new row (rotation):
// tokens are never reused across logins.
func (m *SessionManager) New(ctx context.Context, ownerID pgtype.UUID) (IssuedSession, error) {
	return m.NewForUser(ctx, ownerID, pgtype.UUID{})
}

// NewForUser retains garden scoping while identifying the human using it.
func (m *SessionManager) NewForUser(ctx context.Context, ownerID, userID pgtype.UUID) (IssuedSession, error) {
	token, err := randomToken(m.rnd)
	if err != nil {
		return IssuedSession{}, err
	}
	csrf, err := randomToken(m.rnd)
	if err != nil {
		return IssuedSession{}, err
	}
	s, err := m.store.InsertSession(ctx, dbgen.InsertSessionParams{
		ID:        m.ids.New(),
		OwnerID:   ownerID,
		UserID:    userID,
		TokenHash: HashToken(token),
		CsrfToken: csrf,
		ExpiresAt: pgtype.Timestamptz{Time: m.clock.Now().Add(m.cfg.AbsoluteTimeout), Valid: true},
	})
	if err != nil {
		return IssuedSession{}, err
	}
	return IssuedSession{Token: token, CSRFToken: csrf, Session: s}, nil
}

// Verify resolves a cookie token to a live session, enforcing idle expiry
// against the injected clock (the SQL filter covers wall-clock absolute
// expiry) and refreshing last_seen_at.
func (m *SessionManager) Verify(ctx context.Context, token string) (dbgen.Session, error) {
	if token == "" {
		return dbgen.Session{}, ErrSessionExpired
	}
	s, err := m.store.GetSessionByTokenHash(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Session{}, ErrSessionExpired
	}
	if err != nil {
		return dbgen.Session{}, err
	}
	now := m.clock.Now()
	last := s.CreatedAt
	if s.LastSeenAt.Valid {
		last = s.LastSeenAt
	}
	idleOut := now.Sub(last.Time) >= m.cfg.IdleTimeout
	absOut := !s.ExpiresAt.Valid || !now.Before(s.ExpiresAt.Time)
	if idleOut || absOut {
		if idleOut {
			// park the row so it stops being usable even if clocks disagree
			_, _ = m.store.RevokeSession(ctx, dbgen.RevokeSessionParams{ID: s.ID, OwnerID: s.OwnerID})
		}
		return dbgen.Session{}, ErrSessionExpired
	}
	if err := m.store.TouchSession(ctx, s.ID); err != nil {
		return dbgen.Session{}, err
	}
	s.LastSeenAt = pgtype.Timestamptz{Time: now, Valid: true}
	return s, nil
}

// Revoke scopes by owner id: another owner's session id is a no-op error,
// which is what makes cross-workspace principal ids safe.
func (m *SessionManager) Revoke(ctx context.Context, sessionID, ownerID pgtype.UUID) error {
	_, err := m.store.RevokeSession(ctx, dbgen.RevokeSessionParams{ID: sessionID, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionNotFound
	}
	return err
}

// RevokeAll backs password recovery and password change: every session for
// the owner stops validating immediately.
func (m *SessionManager) RevokeAll(ctx context.Context, ownerID pgtype.UUID) error {
	return m.store.RevokeAllSessions(ctx, ownerID)
}

// MatchesCSRF is the constant-time synchronizer check used by the HTTP
// layer for every state-changing request.
func MatchesCSRF(s dbgen.Session, provided string) bool {
	return provided != "" && subtle.ConstantTimeCompare([]byte(s.CsrfToken), []byte(provided)) == 1
}

// HashToken maps a cookie token to its storage/lookup form.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(r io.Reader) (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
