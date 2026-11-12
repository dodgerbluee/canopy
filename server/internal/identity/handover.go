package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrHandover = errors.New("this registration link is invalid, expired or already used")

// PrepareHandover is an OS-operator-only action. It closes old browser access
// and creates a one-use capability, without changing any garden record or ID.
func (s *Users) PrepareHandover(ctx context.Context) (string, error) {
	token, err := randomToken(rand.Reader)
	if err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(190001701)"); err != nil {
		return "", err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM owner").Scan(&count); err != nil {
		return "", err
	}
	if count != 1 {
		return "", errors.New("handover requires exactly one existing garden owner")
	}
	var owner pgtype.UUID
	if err = tx.QueryRow(ctx, "SELECT o.id FROM owner o WHERE EXISTS(SELECT 1 FROM workspace w WHERE w.owner_id=o.id) FOR UPDATE").Scan(&owner); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO registration_handover(owner_id,token_hash,expires_at) VALUES($1,$2,now()+interval '24 hours')
 ON CONFLICT(owner_id) DO UPDATE SET token_hash=$2,expires_at=now()+interval '24 hours',consumed_at=NULL,created_at=now()`, owner, HashToken(token)); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "UPDATE auth_users SET disabled=true WHERE owner_id=$1", owner); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "UPDATE sessions SET revoked_at=now() WHERE owner_id=$1", owner); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "UPDATE oidc_connections SET enabled=false,revision=revision+1 WHERE owner_id=$1", owner); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM oidc_attempts WHERE owner_id=$1", owner); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}
func (s *Users) HandoverAvailable(ctx context.Context, token string) (bool, error) {
	if len(token) < 32 || len(token) > 128 {
		return false, nil
	}
	var available bool
	err := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM registration_handover WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now())", HashToken(token)).Scan(&available)
	return available, err
}

// RegisterExisting preserves every garden identifier, including the primary
// account ID. Only its credentials/label change; previous SSO links are removed.
func (s *Users) RegisterExisting(ctx context.Context, token string, in SetupInput) (User, dbgen.Workspace, error) {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.DisplayName == "" {
		in.DisplayName = in.Username
	}
	if !validUser(UserInput{Username: in.Username, DisplayName: in.DisplayName, Role: "ADMIN"}) {
		return User{}, dbgen.Workspace{}, ErrInput
	}
	if len(token) < 32 || len(token) > 128 {
		return User{}, dbgen.Workspace{}, ErrHandover
	}
	hash, err := s.password(in.Password)
	if err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(190001701)"); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	var owner pgtype.UUID
	if err = tx.QueryRow(ctx, "SELECT owner_id FROM registration_handover WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now() FOR UPDATE", HashToken(token)).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
		return User{}, dbgen.Workspace{}, ErrHandover
	}
	if err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM owner WHERE id=$1 FOR UPDATE", owner); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	var user pgtype.UUID
	if err = tx.QueryRow(ctx, "SELECT id FROM auth_users WHERE owner_id=$1 AND is_primary FOR UPDATE", owner).Scan(&user); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE owner SET password_hash=$2 WHERE id=$1", owner, hash); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE auth_users SET username=$2,display_name=$3,role='ADMIN',disabled=false WHERE id=$1", user, in.Username, in.DisplayName); err != nil {
		return User{}, dbgen.Workspace{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, "DELETE FROM oidc_identities WHERE user_id=$1", user); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE sessions SET revoked_at=now() WHERE owner_id=$1", owner); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE registration_handover SET consumed_at=now() WHERE owner_id=$1", owner); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	ws, err := dbgen.New(tx).GetWorkspace(ctx, owner)
	if err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	u, err := s.ByID(ctx, owner, user)
	return u, ws, err
}
