-- name: InsertAuditRevision :one
INSERT INTO audit_revisions (id, workspace_id, actor_id, action, aggregate_type, aggregate_id, revision, request_id, prior_values)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListAuditForAggregate :many
SELECT * FROM audit_revisions
WHERE aggregate_type = $1 AND aggregate_id = $2
ORDER BY revision DESC LIMIT sqlc.arg('limit_count');

-- name: InsertMutationReceipt :one
INSERT INTO mutation_receipts (id, workspace_id, operation, idempotency_key, request_hash, result, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (workspace_id, operation, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetMutationReceipt :one
SELECT * FROM mutation_receipts
WHERE workspace_id = $1 AND operation = $2 AND idempotency_key = $3 AND expires_at > now();

-- name: LockMutationKey :exec
-- serializes concurrent replays of the same (workspace, operation, key)
SELECT pg_advisory_xact_lock(hashtextextended($1, 0));
