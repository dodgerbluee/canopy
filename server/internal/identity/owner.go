package identity

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

var (
	ErrOwnerExists       = errors.New("owner already exists")
	ErrNoOwner           = errors.New("no owner configured")
	ErrInvalidPassword   = errors.New("password rejected")
	ErrInvalidCredential = errors.New("invalid credentials")
)

// OwnerStore is the narrow port over the generated owner/workspace queries.
type OwnerStore interface {
	CountOwners(ctx context.Context) (int64, error)
	GetOwner(ctx context.Context) (dbgen.Owner, error)
	GetOwnerByID(ctx context.Context, id pgtype.UUID) (dbgen.Owner, error)
	InsertOwner(ctx context.Context, arg dbgen.InsertOwnerParams) (dbgen.Owner, error)
	UpdateOwnerPassword(ctx context.Context, arg dbgen.UpdateOwnerPasswordParams) (dbgen.Owner, error)
	GetWorkspace(ctx context.Context, ownerID pgtype.UUID) (dbgen.Workspace, error)
	InsertWorkspace(ctx context.Context, arg dbgen.InsertWorkspaceParams) (dbgen.Workspace, error)
	BumpPrivacyEpoch(ctx context.Context, id pgtype.UUID) (int64, error)
}

type OwnerService struct {
	store    OwnerStore
	sessions *SessionManager
	hasher   *PasswordHasher
	ids      platform.IDs
	log      *slog.Logger
}

func NewOwnerService(store OwnerStore, sessions *SessionManager, hasher *PasswordHasher, ids platform.IDs, log *slog.Logger) *OwnerService {
	return &OwnerService{store: store, sessions: sessions, hasher: hasher, ids: ids, log: log}
}

type BootstrapInput struct {
	Password      string
	WorkspaceName string
	Timezone      string
	Currency      string
}

// Bootstrap is the only way an owner comes to exist: there is no signup
// endpoint and no default password. A second bootstrap is refused.
func (s *OwnerService) Bootstrap(ctx context.Context, in BootstrapInput) (dbgen.Owner, dbgen.Workspace, error) {
	n, err := s.store.CountOwners(ctx)
	if err != nil {
		return dbgen.Owner{}, dbgen.Workspace{}, err
	}
	if n > 0 {
		return dbgen.Owner{}, dbgen.Workspace{}, ErrOwnerExists
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return dbgen.Owner{}, dbgen.Workspace{}, ErrInvalidPassword
	}
	owner, err := s.store.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: s.ids.New(), PasswordHash: hash})
	if err != nil {
		return dbgen.Owner{}, dbgen.Workspace{}, err
	}
	ws, err := s.store.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID:              s.ids.New(),
		OwnerID:         owner.ID,
		DisplayName:     defaultStr(in.WorkspaceName, "My garden"),
		Timezone:        defaultStr(in.Timezone, "UTC"),
		DefaultCurrency: defaultStr(in.Currency, "USD"),
	})
	if err != nil {
		return dbgen.Owner{}, dbgen.Workspace{}, err
	}
	// safe audit: event + ids only; the password never reaches the log
	s.log.Info("auth.owner_bootstrap", "owner_id", uuidString(owner.ID), "workspace_id", uuidString(ws.ID))
	return owner, ws, nil
}

// Login verifies the single owner's password and rotates in a fresh
// session. Unknown owner and wrong password return the identical error so
// the transport can answer generically (no account enumeration).
func (s *OwnerService) Login(ctx context.Context, password string) (dbgen.Owner, IssuedSession, error) {
	owner, err := s.store.GetOwner(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Owner{}, IssuedSession{}, ErrInvalidCredential
	}
	if err != nil {
		return dbgen.Owner{}, IssuedSession{}, err
	}
	if !VerifyPassword(owner.PasswordHash, password) {
		return dbgen.Owner{}, IssuedSession{}, ErrInvalidCredential
	}
	sess, err := s.sessions.New(ctx, owner.ID)
	if err != nil {
		return dbgen.Owner{}, IssuedSession{}, err
	}
	s.log.Info("auth.login_success", "owner_id", uuidString(owner.ID), "session_id", uuidString(sess.Session.ID))
	return owner, sess, nil
}

// LogLoginFailure records a failed attempt with source only — never the
// attempted password (05-verification: logs use ids and bounded codes).
func (s *OwnerService) LogLoginFailure(source string) {
	s.log.Warn("auth.login_failure", "source", source)
}

// Workspace resolves the owner's workspace for the request principal.
func (s *OwnerService) Workspace(ctx context.Context, ownerID pgtype.UUID) (dbgen.Workspace, error) {
	return s.store.GetWorkspace(ctx, ownerID)
}

// ChangePassword verifies the current password (constant-time) and
// rotates: all existing sessions are revoked and a fresh one is issued,
// so a stolen cookie cannot outlive a password change.
func (s *OwnerService) ChangePassword(ctx context.Context, ownerID pgtype.UUID, current, next string) (dbgen.Owner, IssuedSession, error) {
	owner, err := s.store.GetOwnerByID(ctx, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Owner{}, IssuedSession{}, ErrInvalidCredential
	}
	if err != nil {
		return dbgen.Owner{}, IssuedSession{}, err
	}
	if !VerifyPassword(owner.PasswordHash, current) {
		s.log.Warn("auth.password_change_bad_current", "owner_id", uuidString(ownerID))
		return dbgen.Owner{}, IssuedSession{}, ErrInvalidCredential
	}
	hash, err := s.hasher.Hash(next)
	if err != nil {
		return dbgen.Owner{}, IssuedSession{}, ErrInvalidPassword
	}
	updated, err := s.store.UpdateOwnerPassword(ctx, dbgen.UpdateOwnerPasswordParams{ID: owner.ID, PasswordHash: hash})
	if err != nil {
		return dbgen.Owner{}, IssuedSession{}, err
	}
	if err := s.sessions.RevokeAll(ctx, owner.ID); err != nil {
		return dbgen.Owner{}, IssuedSession{}, err
	}
	sess, err := s.sessions.New(ctx, owner.ID)
	if err != nil {
		return dbgen.Owner{}, IssuedSession{}, err
	}
	s.log.Info("auth.password_change", "owner_id", uuidString(owner.ID), "session_id", uuidString(sess.Session.ID))
	return updated, sess, nil
}

// Recover is the local admin path (no public reset): it sets a new
// password without the old one, revokes every session and bumps the
// privacy epoch so previously minted private media URLs stop validating.
func (s *OwnerService) Recover(ctx context.Context, password string) (dbgen.Owner, error) {
	owner, err := s.store.GetOwner(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Owner{}, ErrNoOwner
	}
	if err != nil {
		return dbgen.Owner{}, err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return dbgen.Owner{}, ErrInvalidPassword
	}
	updated, err := s.store.UpdateOwnerPassword(ctx, dbgen.UpdateOwnerPasswordParams{ID: owner.ID, PasswordHash: hash})
	if err != nil {
		return dbgen.Owner{}, err
	}
	if err := s.sessions.RevokeAll(ctx, owner.ID); err != nil {
		return dbgen.Owner{}, err
	}
	if ws, err := s.store.GetWorkspace(ctx, owner.ID); err == nil {
		if _, err := s.store.BumpPrivacyEpoch(ctx, ws.ID); err != nil {
			return dbgen.Owner{}, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Owner{}, err
	}
	s.log.Info("auth.owner_recovery", "owner_id", uuidString(owner.ID))
	return updated, nil
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
