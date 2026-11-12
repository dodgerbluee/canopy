-- name: ListAgentGrants :many
SELECT * FROM agent_grants WHERE workspace_id=$1 ORDER BY created_at DESC,id;
-- name: GetAgentGrant :one
SELECT * FROM agent_grants WHERE workspace_id=$1 AND id=$2;
-- name: LockAgentGrant :one
SELECT * FROM agent_grants WHERE workspace_id=$1 AND id=$2 FOR UPDATE;
-- name: InsertAgentGrant :one
INSERT INTO agent_grants(id,workspace_id,name,scopes,plant_ids,expires_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING *;
-- name: EditAgentGrant :one
UPDATE agent_grants SET name=$3,scopes=$4,plant_ids=$5,expires_at=$6,revision=revision+1 WHERE workspace_id=$1 AND id=$2 RETURNING *;
-- name: RevokeAgentGrant :exec
UPDATE agent_grants SET revoked_at=COALESCE(revoked_at,now()),revision=revision+1 WHERE workspace_id=$1 AND id=$2;
-- name: InsertAgentCredential :exec
INSERT INTO agent_credentials(id,workspace_id,grant_id,verifier) VALUES($1,$2,$3,$4);
-- name: RevokeAgentCredentials :exec
UPDATE agent_credentials SET revoked_at=COALESCE(revoked_at,now()) WHERE workspace_id=$1 AND grant_id=$2;
-- name: VerifyAgentCredential :one
SELECT g.*, c.id AS credential_id, c.verifier,w.owner_id FROM agent_credentials c JOIN agent_grants g ON g.id=c.grant_id AND g.workspace_id=c.workspace_id JOIN workspace w ON w.id=g.workspace_id WHERE c.id=$1 AND c.revoked_at IS NULL AND g.revoked_at IS NULL AND g.expires_at>now();
-- name: TouchAgentGrant :exec
UPDATE agent_grants SET last_used_at=now(),last_tool=$3 WHERE workspace_id=$1 AND id=$2;
-- name: ExportAgentGrantMetadata :many
SELECT to_jsonb(t) FROM agent_grants t WHERE workspace_id=$1 ORDER BY id;
-- name: ExportAgentActions :many
SELECT (to_jsonb(t)-'credential_id')::jsonb FROM agent_actions t WHERE workspace_id=$1 ORDER BY id;
-- name: ExportAgentOperations :many
SELECT (to_jsonb(t)-'response')::jsonb FROM agent_operations t WHERE workspace_id=$1 ORDER BY created_at,operation_id;
