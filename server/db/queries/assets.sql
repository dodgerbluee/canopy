-- name: InsertAsset :one
INSERT INTO assets (id, workspace_id, purpose, state, byte_count, sha256, storage_key, display_name, expires_at)
VALUES ($1, $2, $3, 'RESERVED', $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetAsset :one
SELECT * FROM assets WHERE id = $1 AND workspace_id = $2;

-- name: LockAsset :one
SELECT * FROM assets WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: MarkAssetUploaded :one
UPDATE assets SET state = 'UPLOADED', byte_count = $3, mime_type = $4, sha256 = $5
WHERE id = $1 AND workspace_id = $2 AND state = 'RESERVED'
RETURNING *;

-- name: MarkAssetState :one
UPDATE assets SET state = sqlc.arg('state'), error_code = sqlc.narg('error_code')
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: MarkAssetStateFrom :one
-- guarded transition: matches only when the asset is currently in
-- from_state; ErrNoRows means someone else moved it (e.g. deletion won)
-- and the caller must NOT revive it
UPDATE assets SET state = sqlc.arg('to_state'), error_code = sqlc.narg('error_code')
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
  AND state = sqlc.arg('from_state')
RETURNING *;

-- name: GetAssetByID :one
-- worker-scope read: the job carries only the asset id; workspace scoping
-- happens via the row itself, never via client input
SELECT * FROM assets WHERE id = $1;

-- name: GetAssetVariant :one
SELECT * FROM asset_variants WHERE asset_id = $1 AND name = $2;

-- name: LockAssetByID :one
-- purge-worker lock: the job carries only the asset id
SELECT * FROM assets WHERE id = $1 FOR UPDATE;

-- name: ListPendingAssets :many
SELECT * FROM assets
WHERE workspace_id = $1 AND state IN ('RESERVED','UPLOADED','PROCESSING','FAILED')
  AND (
    sqlc.narg('after_created_at')::timestamptz IS NULL
    OR created_at < sqlc.narg('after_created_at')
    OR (created_at = sqlc.narg('after_created_at') AND id < sqlc.narg('after_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: LockWorkspaceForQuota :one
-- serializes concurrent quota reservations per workspace: usage plus
-- the new claim must fit before any competing insert becomes visible
SELECT id FROM workspace WHERE id = $1 FOR UPDATE;

-- name: ExpireStaleReservations :many
UPDATE assets SET state = 'DELETING'
WHERE state = 'RESERVED' AND expires_at < now()
RETURNING id, workspace_id, storage_key;

-- name: UpsertAssetVariant :one
INSERT INTO asset_variants (asset_id, name, width_px, height_px, storage_key, byte_count)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (asset_id, name) DO UPDATE SET
  width_px = EXCLUDED.width_px, height_px = EXCLUDED.height_px,
  storage_key = EXCLUDED.storage_key, byte_count = EXCLUDED.byte_count
RETURNING *;

-- name: ListAssetVariants :many
SELECT * FROM asset_variants WHERE asset_id = ANY(sqlc.arg('asset_ids')::uuid[]);

-- name: ListAssetsByIDs :many
SELECT * FROM assets WHERE id = ANY(sqlc.arg('ids')::uuid[]);

-- name: WorkspaceStorageUsed :one
SELECT coalesce(sum(byte_count), 0)::bigint AS used_bytes FROM assets
WHERE workspace_id = $1 AND state NOT IN ('DELETING');

-- name: SavePhotoCoverQuality :exec
UPDATE assets SET cover_quality=sqlc.narg(quality)::double precision,cover_quality_version=1
WHERE id=sqlc.arg(id) AND workspace_id=sqlc.arg(workspace_id)
 AND purpose='PHOTO' AND state IN ('PROCESSING','READY');

-- name: ListPendingPhotoCoverQuality :many
SELECT a.id,a.workspace_id,v.storage_key
FROM assets a JOIN asset_variants v ON v.asset_id=a.id AND v.name='thumb'
WHERE a.purpose='PHOTO' AND a.state='READY' AND COALESCE(a.cover_quality_version,0)=0
ORDER BY a.created_at,a.id LIMIT 100;
