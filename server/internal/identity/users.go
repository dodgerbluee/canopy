package identity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAccess = errors.New("administrator access required")
var ErrConflict = errors.New("account changed or username already exists")
var ErrLastAdmin = errors.New("keep at least one active administrator")
var ErrInput = errors.New("invalid account details")

const MinPasswordLength = 10

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,63}$`)

type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	Primary     bool   `json:"primary"`
	HasPassword bool   `json:"hasPassword"`
	OIDCLinked  bool   `json:"oidcLinked"`
}
type UserInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	Password    string `json:"password"`
}
type SetupInput struct {
	Username      string `json:"username"`
	DisplayName   string `json:"displayName"`
	Password      string `json:"password"`
	WorkspaceName string `json:"workspaceName"`
	Timezone      string `json:"timezone"`
	Currency      string `json:"currency"`
}
type Users struct {
	Pool     *pgxpool.Pool
	Hasher   *PasswordHasher
	Sessions *SessionManager
}

func NewUsers(pool *pgxpool.Pool, sessions *SessionManager) *Users {
	return &Users{Pool: pool, Hasher: NewPasswordHasher(ProductionParams), Sessions: sessions}
}
func validUser(in UserInput) bool {
	return usernamePattern.MatchString(in.Username) && len(in.DisplayName) > 0 && len(in.DisplayName) <= 160 && (in.Role == "ADMIN" || in.Role == "MEMBER")
}
func (s *Users) password(v string) (string, error) {
	if utf8.RuneCountInString(v) < MinPasswordLength || len(v) > MaxPasswordLen {
		return "", fmt.Errorf("%w: use at least %d characters and at most %d bytes", ErrInput, MinPasswordLength, MaxPasswordLen)
	}
	return s.Hasher.Hash(v)
}
func (s *Users) SetupAvailable(ctx context.Context) (bool, error) {
	var n int
	err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM owner").Scan(&n)
	return n == 0, err
}

// Setup's transaction lock serializes first registration across API processes.
// Owner, primary user and garden are committed together or not at all.
func (s *Users) Setup(ctx context.Context, in SetupInput) (User, dbgen.Workspace, error) {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.DisplayName == "" {
		in.DisplayName = in.Username
	}
	if !validUser(UserInput{Username: in.Username, DisplayName: in.DisplayName, Role: "ADMIN"}) || len(strings.TrimSpace(in.WorkspaceName)) == 0 || len(in.WorkspaceName) > 160 {
		return User{}, dbgen.Workspace{}, ErrInput
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return User{}, dbgen.Workspace{}, ErrInput
	}
	if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(in.Currency) {
		return User{}, dbgen.Workspace{}, ErrInput
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
	var n int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM owner").Scan(&n); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if n > 0 {
		return User{}, dbgen.Workspace{}, ErrOwnerExists
	}
	q := dbgen.New(tx)
	ids := platform.UUIDv4{}
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: hash})
	if err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE auth_users SET username=$2,display_name=$3 WHERE id=$1", owner.ID, in.Username, in.DisplayName); err != nil {
		return User{}, dbgen.Workspace{}, ErrConflict
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{ID: ids.New(), OwnerID: owner.ID, DisplayName: in.WorkspaceName, Timezone: in.Timezone, DefaultCurrency: in.Currency})
	if err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, dbgen.Workspace{}, err
	}
	u, err := s.ByID(ctx, owner.ID, owner.ID)
	return u, ws, err
}

const userColumns = `u.id::text,u.username,u.display_name,u.role,u.disabled,u.is_primary,(u.is_primary OR u.password_hash<>''),EXISTS(SELECT 1 FROM oidc_identities i WHERE i.user_id=u.id)`

func readUser(row pgx.Row) (u User, err error) {
	err = row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Disabled, &u.Primary, &u.HasPassword, &u.OIDCLinked)
	return
}
func (s *Users) ByID(ctx context.Context, owner, id pgtype.UUID) (User, error) {
	return readUser(s.Pool.QueryRow(ctx, "SELECT "+userColumns+" FROM auth_users u WHERE u.owner_id=$1 AND u.id=$2", owner, id))
}
func (s *Users) ForSession(ctx context.Context, sess dbgen.Session) (User, error) {
	id := sess.UserID
	if !id.Valid {
		id = sess.OwnerID
	}
	return s.ByID(ctx, sess.OwnerID, id)
}
func (s *Users) Login(ctx context.Context, username, password string) (User, IssuedSession, error) {
	var id, owner pgtype.UUID
	var hash string
	// Password-only clients retain the original owner's login, even if renamed.
	err := s.Pool.QueryRow(ctx, `SELECT u.id,u.owner_id,CASE WHEN u.is_primary THEN o.password_hash ELSE u.password_hash END
 FROM auth_users u JOIN owner o ON o.id=u.owner_id WHERE NOT u.disabled AND
 (($1='' AND u.is_primary) OR u.username=$1) ORDER BY o.created_at LIMIT 1`, strings.ToLower(strings.TrimSpace(username))).Scan(&id, &owner, &hash)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !VerifyPassword(hash, password)) {
		return User{}, IssuedSession{}, ErrInvalidCredential
	}
	if err != nil {
		return User{}, IssuedSession{}, err
	}
	u, err := s.ByID(ctx, owner, id)
	if err != nil {
		return User{}, IssuedSession{}, err
	}
	sess, err := s.Sessions.NewForUser(ctx, owner, id)
	return u, sess, err
}
func (s *Users) List(ctx context.Context, owner pgtype.UUID) ([]User, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+userColumns+" FROM auth_users u WHERE owner_id=$1 ORDER BY u.created_at,u.id", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := readUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Users) Edit(ctx context.Context, owner, actor, id pgtype.UUID, in UserInput) (User, error) {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if !validUser(in) {
		return User{}, ErrInput
	}
	hash := ""
	var err error
	if in.Password != "" {
		hash, err = s.password(in.Password)
		if err != nil {
			return User{}, err
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	// All role/disable edits in a garden share this lock, protecting the last admin.
	if _, err = tx.Exec(ctx, "SELECT id FROM owner WHERE id=$1 FOR UPDATE", owner); err != nil {
		return User{}, err
	}
	var allowed bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_users WHERE id=$1 AND owner_id=$2 AND role='ADMIN' AND NOT disabled)", actor, owner).Scan(&allowed); err != nil {
		return User{}, err
	}
	if !allowed {
		return User{}, ErrAccess
	}
	creating := !id.Valid
	if creating {
		if hash == "" {
			return User{}, fmt.Errorf("%w: set an initial password", ErrInput)
		}
		id = platform.UUIDv4{}.New()
		_, err = tx.Exec(ctx, `INSERT INTO auth_users(id,owner_id,username,display_name,role,disabled,password_hash) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, owner, in.Username, in.DisplayName, in.Role, in.Disabled, hash)
	} else {
		var primary bool
		if err = tx.QueryRow(ctx, "SELECT is_primary FROM auth_users WHERE id=$1 AND owner_id=$2", id, owner).Scan(&primary); err != nil {
			return User{}, ErrConflict
		}
		_, err = tx.Exec(ctx, "UPDATE auth_users SET username=$3,display_name=$4,role=$5,disabled=$6 WHERE id=$1 AND owner_id=$2", id, owner, in.Username, in.DisplayName, in.Role, in.Disabled)
		if err == nil && hash != "" {
			if primary {
				_, err = tx.Exec(ctx, "UPDATE owner SET password_hash=$2 WHERE id=$1", owner, hash)
			} else {
				_, err = tx.Exec(ctx, "UPDATE auth_users SET password_hash=$2 WHERE id=$1", id, hash)
			}
		}
	}
	if err != nil {
		return User{}, ErrConflict
	}
	var admins int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM auth_users WHERE owner_id=$1 AND role='ADMIN' AND NOT disabled", owner).Scan(&admins); err != nil {
		return User{}, err
	}
	if admins == 0 {
		return User{}, ErrLastAdmin
	}
	// Permissions are resolved on every request; revoke for disable/reset as well.
	if in.Disabled || hash != "" {
		if _, err = tx.Exec(ctx, "UPDATE sessions SET revoked_at=now() WHERE owner_id=$1 AND coalesce(user_id,owner_id)=$2", owner, id); err != nil {
			return User{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return s.ByID(ctx, owner, id)
}
func (s *Users) ChangePassword(ctx context.Context, owner, id pgtype.UUID, current, next string) (IssuedSession, error) {
	hash, err := s.password(next)
	if err != nil {
		return IssuedSession{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return IssuedSession{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM owner WHERE id=$1 FOR UPDATE", owner); err != nil {
		return IssuedSession{}, err
	}
	var old string
	var primary bool
	err = tx.QueryRow(ctx, `SELECT CASE WHEN u.is_primary THEN o.password_hash ELSE u.password_hash END,u.is_primary
 FROM auth_users u JOIN owner o ON o.id=u.owner_id WHERE u.id=$1 AND u.owner_id=$2 AND NOT u.disabled FOR UPDATE OF u,o`, id, owner).Scan(&old, &primary)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !VerifyPassword(old, current)) {
		return IssuedSession{}, ErrInvalidCredential
	}
	if err != nil {
		return IssuedSession{}, err
	}
	if primary {
		_, err = tx.Exec(ctx, "UPDATE owner SET password_hash=$2 WHERE id=$1", owner, hash)
	} else {
		_, err = tx.Exec(ctx, "UPDATE auth_users SET password_hash=$2 WHERE id=$1", id, hash)
	}
	if err != nil {
		return IssuedSession{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE sessions SET revoked_at=now() WHERE owner_id=$1 AND coalesce(user_id,owner_id)=$2", owner, id); err != nil {
		return IssuedSession{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return IssuedSession{}, err
	}
	return s.Sessions.NewForUser(ctx, owner, id)
}
