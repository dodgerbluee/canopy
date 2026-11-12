package agentaccess

import (
	"canopy/server/internal/agentcontext"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type Action struct {
	ID           string          `json:"id"`
	GrantID      string          `json:"grantId"`
	CredentialID string          `json:"credentialId"`
	OperationID  string          `json:"operationId"`
	Tool         string          `json:"tool"`
	Before       json.RawMessage `json:"before"`
	Input        json.RawMessage `json:"input"`
	State        string          `json:"state"`
	CreatedAt    time.Time       `json:"createdAt"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	GrantName    string          `json:"grantName"`
}

func (m *Manager) Propose(ctx context.Context, p Principal, tool, operation string, input json.RawMessage, before json.RawMessage) (Action, error) {
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return Action{}, err
	}
	defer tx.Rollback(ctx)
	var valid bool
	err = tx.QueryRow(ctx, `SELECT revoked_at IS NULL AND expires_at>now() AND revision=$3 FROM agent_grants WHERE id=$1 AND workspace_id=$2 FOR SHARE`, p.Grant.ID, p.Grant.WorkspaceID, p.Grant.Revision).Scan(&valid)
	if err != nil || !valid {
		return Action{}, ErrDenied
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_actions(id,grant_id,workspace_id,credential_id,operation_id,tool,input,input_hash,before_snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(grant_id,operation_id) DO NOTHING`, uuid.NewString(), p.Grant.ID, p.Grant.WorkspaceID, p.CredentialID, operation, tool, input, agentcontext.Hash(input), before)
	if err != nil {
		return Action{}, err
	}
	var a Action
	var hash string
	err = tx.QueryRow(ctx, `SELECT id,grant_id,credential_id,operation_id,tool,input,before_snapshot,state,created_at,expires_at,input_hash FROM agent_actions WHERE grant_id=$1 AND operation_id=$2`, p.Grant.ID, operation).Scan(&a.ID, &a.GrantID, &a.CredentialID, &a.OperationID, &a.Tool, &a.Input, &a.Before, &a.State, &a.CreatedAt, &a.ExpiresAt, &hash)
	if err != nil {
		return a, err
	}
	if a.Tool != tool || hash != agentcontext.Hash(input) {
		return a, &Problem{"IDEMPOTENCY_CONFLICT", "This operation identity was already used for different input."}
	}
	if err = tx.Commit(ctx); err != nil {
		return a, err
	}
	return a, nil
}
func (m *Manager) Actions(ctx context.Context, ws pgtype.UUID, grant string) ([]Action, error) {
	_, err := m.Pool.Exec(ctx, `UPDATE agent_actions SET state='EXPIRED' WHERE workspace_id=$1 AND state='PENDING' AND expires_at<=now()`, ws)
	if err != nil {
		return nil, err
	}
	rows, err := m.Pool.Query(ctx, `SELECT a.id,a.grant_id,a.credential_id,a.operation_id,a.tool,a.input,a.before_snapshot,a.state,a.created_at,a.expires_at,g.name FROM agent_actions a JOIN agent_grants g ON g.id=a.grant_id WHERE a.workspace_id=$1 AND ($2='' OR a.grant_id::text=$2) ORDER BY a.created_at DESC,a.id LIMIT 100`, ws, grant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Action{}
	for rows.Next() {
		var a Action
		if err = rows.Scan(&a.ID, &a.GrantID, &a.CredentialID, &a.OperationID, &a.Tool, &a.Input, &a.Before, &a.State, &a.CreatedAt, &a.ExpiresAt, &a.GrantName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (m *Manager) Decide(ctx context.Context, ws pgtype.UUID, id string, approve bool) (any, error) {
	var a Action
	err := m.Pool.QueryRow(ctx, `SELECT id,grant_id,credential_id,operation_id,tool,input,before_snapshot,state,created_at,expires_at FROM agent_actions WHERE workspace_id=$1 AND id=$2`, ws, id).Scan(&a.ID, &a.GrantID, &a.CredentialID, &a.OperationID, &a.Tool, &a.Input, &a.Before, &a.State, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		return nil, ErrDenied
	}
	if a.State == "APPLIED" {
		return map[string]any{"status": "applied", "id": a.ID}, nil
	}
	if a.State != "PENDING" || !a.ExpiresAt.After(m.Now()) {
		return nil, invalid("This proposal is no longer pending.")
	}
	if !approve {
		tag, err := m.Pool.Exec(ctx, `UPDATE agent_actions SET state='REJECTED' WHERE id=$1 AND state='PENDING'`, a.ID)
		if err != nil || tag.RowsAffected() != 1 {
			return nil, invalid("Proposal changed. Reload before continuing.")
		}
		return map[string]any{"status": "rejected", "id": a.ID}, nil
	}
	g, err := m.Get(ctx, ws, ID(a.GrantID))
	if err != nil {
		return nil, err
	}
	var owner string
	if err = m.Pool.QueryRow(ctx, `SELECT owner_id FROM workspace WHERE id=$1`, ws).Scan(&owner); err != nil {
		return nil, err
	}
	p, err := m.Current(ctx, Principal{Grant: g, OwnerID: owner, CredentialID: a.CredentialID})
	if err != nil {
		return nil, err
	}
	if m.Apply == nil {
		return nil, errors.New("Agent action execution is unavailable")
	}
	return m.Apply(ctx, p, a)
}
