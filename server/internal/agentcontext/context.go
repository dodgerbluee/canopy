// Package agentcontext joins agent authorization and domain transactions. It
// carries request identity, never browser sessions or plaintext credentials.
package agentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

type Origin struct {
	Revision                                                                          int
	GrantID, CredentialID, WorkspaceID, RequestID, Tool, OperationID, Scope, ActionID string
	Input                                                                             json.RawMessage
	Approved                                                                          bool
}
type key struct{}

func With(ctx context.Context, o Origin) context.Context { return context.WithValue(ctx, key{}, o) }
func From(ctx context.Context) (Origin, bool)            { o, ok := ctx.Value(key{}).(Origin); return o, ok }
func Hash(b []byte) string                               { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func Enter(ctx context.Context, tx pgx.Tx) error {
	o, ok := From(ctx)
	if !ok {
		return nil
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT $3=ANY(scopes) AND revoked_at IS NULL AND expires_at>now() AND revision=$5 AND EXISTS(SELECT 1 FROM agent_credentials WHERE id=$4 AND grant_id=$1 AND revoked_at IS NULL) FROM agent_grants WHERE id=$1 AND workspace_id=$2 FOR SHARE`, o.GrantID, o.WorkspaceID, o.Scope, o.CredentialID, o.Revision).Scan(&valid)
	if err != nil || !valid {
		return errors.New("agent authorization changed")
	}
	var maintenance bool
	if err = tx.QueryRow(ctx, `SELECT maintenance FROM system_state WHERE id=true FOR SHARE`).Scan(&maintenance); err != nil {
		return err
	}
	if maintenance {
		return errors.New("writes unavailable during maintenance")
	}
	if o.ActionID != "" {
		var state string
		err = tx.QueryRow(ctx, `SELECT state FROM agent_actions WHERE id=$1 AND grant_id=$2 AND input_hash=$3 AND expires_at>now() FOR UPDATE`, o.ActionID, o.GrantID, Hash(o.Input)).Scan(&state)
		if err != nil || (!o.Approved) || (state != "PENDING" && state != "APPLIED") {
			return errors.New("agent proposal is no longer applicable")
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_operations(grant_id,workspace_id,operation_id,tool,input,input_hash,state) VALUES($1,$2,$3,$4,$5,$6,'EXECUTING') ON CONFLICT DO NOTHING`, o.GrantID, o.WorkspaceID, o.OperationID, o.Tool, o.Input, Hash(o.Input))
	if err != nil {
		return err
	}
	var hash, tool string
	if err = tx.QueryRow(ctx, `SELECT input_hash,tool FROM agent_operations WHERE grant_id=$1 AND operation_id=$2 FOR UPDATE`, o.GrantID, o.OperationID).Scan(&hash, &tool); err != nil {
		return err
	}
	if hash != Hash(o.Input) || tool != o.Tool {
		return errors.New("agent operation identity was reused for different input")
	}
	body, _ := json.Marshal(o)
	_, err = tx.Exec(ctx, `SELECT set_config('canopy.agent_origin',$1,true)`, string(body))
	return err
}
func Complete(ctx context.Context, tx pgx.Tx, result any) error {
	o, ok := From(ctx)
	if !ok {
		return nil
	}
	raw, _ := json.Marshal(result)
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	refs := map[string]json.RawMessage{}
	for _, k := range []string{"id", "ID", "revision", "Revision"} {
		if v, yes := object[k]; yes {
			refs[k] = v
		}
	}
	data, _ := json.Marshal(refs)
	if _, err := tx.Exec(ctx, `UPDATE agent_operations SET state='APPLIED',resource=$3 WHERE grant_id=$1 AND operation_id=$2`, o.GrantID, o.OperationID, data); err != nil {
		return err
	}
	if o.ActionID != "" {
		_, err := tx.Exec(ctx, `UPDATE agent_actions SET state='APPLIED',applied_at=now() WHERE id=$1 AND grant_id=$2`, o.ActionID, o.GrantID)
		return err
	}
	return nil
}

// JobAllowed rechecks durable originating authority before external work or
// archive delivery. Browser-owned jobs (no grant) retain existing behavior.
func JobAllowed(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, grant, credential, workspace, scope, plant string) bool {
	if grant == "" {
		return true
	}
	var valid bool
	err := db.QueryRow(ctx, `SELECT g.revoked_at IS NULL AND g.expires_at>now() AND $4=ANY(g.scopes) AND (cardinality(g.plant_ids)=0 OR $5::uuid=ANY(g.plant_ids)) AND EXISTS(SELECT 1 FROM agent_credentials c WHERE c.id=$2 AND c.grant_id=g.id AND c.revoked_at IS NULL) FROM agent_grants g WHERE g.id=$1 AND g.workspace_id=$3`, grant, credential, workspace, scope, nilUUID(plant)).Scan(&valid)
	return err == nil && valid
}
func nilUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
