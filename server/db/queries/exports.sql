-- name: InsertExportJob :one
INSERT INTO export_jobs (id, workspace_id, state, snapshot_at, expires_at)
VALUES ($1, $2, 'QUEUED', $3, $4)
RETURNING *;

-- name: GetExportJob :one
SELECT * FROM export_jobs WHERE id = $1 AND workspace_id = $2;

-- name: UpdateExportJobState :one
UPDATE export_jobs SET
  state = sqlc.arg('state'),
  finished_at = CASE WHEN sqlc.arg('state') IN ('READY'::export_state,'FAILED'::export_state,'EXPIRED'::export_state) THEN now() ELSE finished_at END,
  manifest_sha256 = COALESCE(sqlc.narg('manifest_sha256'), manifest_sha256),
  error_code = sqlc.narg('error_code')
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: RevokeWorkspaceExports :exec
UPDATE export_jobs SET state = 'EXPIRED', finished_at = now()
WHERE workspace_id = $1 AND state IN ('QUEUED','RUNNING','READY');
