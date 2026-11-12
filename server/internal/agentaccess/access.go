package agentaccess

import (
	dbgen "canopy/server/db/generated"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"sync"
	"time"
)

var ErrDenied = errors.New("Agent access is unavailable or not permitted.")
var Scopes = []string{"garden:read", "knowledge:read", "photos:read", "logs:write", "rain:write", "plants:write", "care:read", "care:write", "purchases:read", "purchases:write", "receipts:read", "media:write", "analysis:run", "suggestions:review", "inbox:read", "inbox:write", "exports:request"}

type Problem struct{ Code, Message string }

func (p *Problem) Error() string   { return p.Message }
func invalid(msg string) error     { return &Problem{"VALIDATION", msg} }
func ID(s string) pgtype.UUID      { var id pgtype.UUID; _ = id.Scan(s); return id }
func String(id pgtype.UUID) string { v, _ := id.Value(); s, _ := v.(string); return s }

type Grant struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Name        string     `json:"name"`
	Scopes      []string   `json:"scopes"`
	PlantIDs    []string   `json:"plantIds"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
	Revision    int        `json:"revision"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	LastTool    *string    `json:"lastTool"`
	CreatedAt   time.Time  `json:"createdAt"`
}

func (g Grant) Has(scope string) bool {
	for _, s := range g.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
func (g Grant) Plant(id string) bool {
	if len(g.PlantIDs) == 0 {
		return true
	}
	for _, p := range g.PlantIDs {
		if p == id {
			return true
		}
	}
	return false
}
func (g Grant) Active(now time.Time) bool { return g.RevokedAt == nil && g.ExpiresAt.After(now) }

type Principal struct {
	Grant                 Grant
	OwnerID, CredentialID string
}
type key struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, key{}, p)
}
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(key{}).(Principal)
	return p, ok
}

type SaveInput struct {
	ID, Name         string
	Scopes, PlantIDs []string
	ExpiresAt        time.Time
	ExpectedRevision int
}
type Issued struct {
	Grant Grant  `json:"grant"`
	Token string `json:"token"`
}
type Manager struct {
	Apply func(context.Context, Principal, Action) (any, error)
	Pool  *pgxpool.Pool
	Q     *dbgen.Queries
	Now   func() time.Time
	mu    sync.Mutex
	rates map[string][]time.Time
}

func New(pool *pgxpool.Pool) *Manager {
	return &Manager{Pool: pool, Q: dbgen.New(pool), Now: time.Now, rates: map[string][]time.Time{}}
}
func public(g dbgen.AgentGrant) Grant {
	o := Grant{ID: String(g.ID), WorkspaceID: String(g.WorkspaceID), Name: g.Name, Scopes: g.Scopes, PlantIDs: []string{}, ExpiresAt: g.ExpiresAt.Time, Revision: int(g.Revision), CreatedAt: g.CreatedAt.Time}
	for _, id := range g.PlantIds {
		o.PlantIDs = append(o.PlantIDs, String(id))
	}
	if g.RevokedAt.Valid {
		o.RevokedAt = &g.RevokedAt.Time
	}
	if g.LastUsedAt.Valid {
		o.LastUsedAt = &g.LastUsedAt.Time
	}
	if g.LastTool.Valid {
		o.LastTool = &g.LastTool.String
	}
	return o
}
func (m *Manager) List(ctx context.Context, ws pgtype.UUID) ([]Grant, error) {
	rows, err := m.Q.ListAgentGrants(ctx, ws)
	out := []Grant{}
	for _, r := range rows {
		out = append(out, public(r))
	}
	return out, err
}
func (m *Manager) Get(ctx context.Context, ws, id pgtype.UUID) (Grant, error) {
	row, err := m.Q.GetAgentGrant(ctx, dbgen.GetAgentGrantParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return Grant{}, ErrDenied
	}
	return public(row), nil
}
func (m *Manager) validate(ctx context.Context, ws pgtype.UUID, in SaveInput) ([]pgtype.UUID, error) {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 {
		return nil, invalid("Enter a connection name of up to 120 characters.")
	}
	if !in.ExpiresAt.After(m.Now()) || in.ExpiresAt.After(m.Now().Add(90*24*time.Hour)) {
		return nil, invalid("Expiry must be in the next 90 days.")
	}
	if len(in.Scopes) == 0 || len(in.Scopes) > len(Scopes) || len(in.PlantIDs) > 100 {
		return nil, invalid("Choose valid permissions and up to 100 plants.")
	}
	seen := map[string]bool{}
	for _, s := range in.Scopes {
		valid := false
		for _, allowed := range Scopes {
			if s == allowed {
				valid = true
			}
		}
		if !valid || seen[s] {
			return nil, invalid("Choose each supported permission once.")
		}
		seen[s] = true
		if len(in.PlantIDs) > 0 && (strings.HasPrefix(s, "purchases:") || s == "receipts:read" || s == "exports:request") {
			return nil, invalid("Financial, receipt and export access requires an unrestricted garden grant.")
		}
	}
	if (seen["purchases:write"] || seen["receipts:read"]) && !seen["purchases:read"] {
		return nil, invalid("Purchase changes and receipt access also require purchase read permission.")
	}
	if seen["exports:request"] {
		for _, scope := range []string{"garden:read", "photos:read", "purchases:read", "receipts:read"} {
			if !seen[scope] {
				return nil, invalid("Full garden exports require garden, photo, purchase and receipt read permissions.")
			}
		}
	}
	ids := []pgtype.UUID{}
	seen = map[string]bool{}
	for _, s := range in.PlantIDs {
		id := ID(s)
		if !id.Valid || seen[s] {
			return nil, invalid("Choose valid, unique plants.")
		}
		seen[s] = true
		if _, err := m.Q.GetPlant(ctx, dbgen.GetPlantParams{WorkspaceID: ws, ID: id}); err != nil {
			return nil, ErrDenied
		}
		ids = append(ids, id)
	}
	return ids, nil
}
func (m *Manager) issue(ctx context.Context, q *dbgen.Queries, g Grant) (string, error) {
	id := uuid.NewString()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := "canopy_agent." + id + "." + base64.RawURLEncoding.EncodeToString(secret)
	sum := sha256.Sum256([]byte(token))
	err := q.InsertAgentCredential(ctx, dbgen.InsertAgentCredentialParams{ID: ID(id), WorkspaceID: ID(g.WorkspaceID), GrantID: ID(g.ID), Verifier: sum[:]})
	return token, err
}
func (m *Manager) Save(ctx context.Context, ws pgtype.UUID, in SaveInput) (Issued, error) {
	ids, err := m.validate(ctx, ws, in)
	if err != nil {
		return Issued{}, err
	}
	id := ID(in.ID)
	if !id.Valid {
		return Issued{}, invalid("Invalid connection identity.")
	}
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return Issued{}, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	var row dbgen.AgentGrant
	if in.ExpectedRevision == 0 {
		row, err = q.InsertAgentGrant(ctx, dbgen.InsertAgentGrantParams{ID: id, WorkspaceID: ws, Name: strings.TrimSpace(in.Name), Scopes: in.Scopes, PlantIds: ids, ExpiresAt: pgtype.Timestamptz{Time: in.ExpiresAt, Valid: true}})
	} else {
		cur, e := q.LockAgentGrant(ctx, dbgen.LockAgentGrantParams{WorkspaceID: ws, ID: id})
		if e != nil {
			return Issued{}, ErrDenied
		}
		if int(cur.Revision) != in.ExpectedRevision {
			return Issued{}, &Problem{"CONFLICT", "Connection changed. Reload before saving."}
		}
		if cur.RevokedAt.Valid {
			return Issued{}, ErrDenied
		}
		row, err = q.EditAgentGrant(ctx, dbgen.EditAgentGrantParams{WorkspaceID: ws, ID: id, Name: strings.TrimSpace(in.Name), Scopes: in.Scopes, PlantIds: ids, ExpiresAt: pgtype.Timestamptz{Time: in.ExpiresAt, Valid: true}})
	}
	if err != nil {
		return Issued{}, invalid("Connection could not be saved. Reload before retrying.")
	}
	out := Issued{Grant: public(row)}
	if in.ExpectedRevision == 0 {
		out.Token, err = m.issue(ctx, q, out.Grant)
		if err != nil {
			return Issued{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Issued{}, err
	}
	return out, nil
}
func (m *Manager) ChangeCredential(ctx context.Context, ws, id pgtype.UUID, revision int, revoke bool) (Issued, error) {
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return Issued{}, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	cur, err := q.LockAgentGrant(ctx, dbgen.LockAgentGrantParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return Issued{}, ErrDenied
	}
	if int(cur.Revision) != revision {
		return Issued{}, &Problem{"CONFLICT", "Connection changed. Reload before continuing."}
	}
	if err = q.RevokeAgentCredentials(ctx, dbgen.RevokeAgentCredentialsParams{WorkspaceID: ws, GrantID: id}); err != nil {
		return Issued{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_actions SET state='REVOKED' WHERE grant_id=$1 AND state='PENDING'`, id); err != nil {
		return Issued{}, err
	}
	g := public(cur)
	out := Issued{Grant: g}
	if revoke {
		err = q.RevokeAgentGrant(ctx, dbgen.RevokeAgentGrantParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return out, err
		}
	} else {
		if !g.Active(m.Now()) {
			return out, ErrDenied
		}
		out.Token, err = m.issue(ctx, q, g)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, `UPDATE agent_grants SET revision=revision+1 WHERE id=$1`, id)
		if err != nil {
			return out, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	out.Grant, err = m.Get(ctx, ws, id)
	return out, err
}
func (m *Manager) Verify(ctx context.Context, token string) (Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "canopy_agent" || len(parts[2]) != 43 || !ID(parts[1]).Valid {
		return Principal{}, ErrDenied
	}
	row, err := m.Q.VerifyAgentCredential(ctx, ID(parts[1]))
	if err != nil {
		return Principal{}, ErrDenied
	}
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(sum[:], row.Verifier) != 1 {
		return Principal{}, ErrDenied
	}
	g := public(dbgen.AgentGrant{ID: row.ID, WorkspaceID: row.WorkspaceID, Name: row.Name, Scopes: row.Scopes, PlantIds: row.PlantIds, ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt, Revision: row.Revision, LastUsedAt: row.LastUsedAt, LastTool: row.LastTool, CreatedAt: row.CreatedAt})
	return Principal{Grant: g, OwnerID: String(row.OwnerID), CredentialID: String(row.CredentialID)}, nil
}
func (m *Manager) Current(ctx context.Context, p Principal) (Principal, error) {
	g, err := m.Get(ctx, ID(p.Grant.WorkspaceID), ID(p.Grant.ID))
	if err != nil || !g.Active(m.Now()) {
		return Principal{}, ErrDenied
	}
	var valid bool
	err = m.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_credentials WHERE id=$1 AND grant_id=$2 AND revoked_at IS NULL)`, ID(p.CredentialID), ID(g.ID)).Scan(&valid)
	if err != nil || !valid {
		return Principal{}, ErrDenied
	}
	p.Grant = g
	return p, nil
}
func (m *Manager) Limit(p Principal, write bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	kind := "read"
	limit := 60
	if write {
		kind = "write"
		limit = 10
	}
	key := p.Grant.ID + ":" + kind
	now := m.Now()
	recent := []time.Time{}
	for _, t := range m.rates[key] {
		if t.After(now.Add(-time.Minute)) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		return false
	}
	m.rates[key] = append(recent, now)
	return true
}

var _ = pgx.ErrNoRows
