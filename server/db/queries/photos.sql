-- name: InsertPhoto :one
INSERT INTO plant_photos (id, workspace_id, plant_id, asset_id, taken_at, date_source, timezone, caption, viewpoint, is_cover)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetPhoto :one
SELECT * FROM plant_photos WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL;

-- name: GetPhotoAny :one
-- includes soft-deleted rows: the delete flow needs the asset id after
-- the live view has already hidden the row
SELECT * FROM plant_photos WHERE id = $1 AND workspace_id = $2;

-- name: UpdatePhoto :one
UPDATE plant_photos SET
  taken_at = COALESCE(sqlc.narg('taken_at'), taken_at),
  date_source = COALESCE(sqlc.narg('date_source'), date_source),
  timezone = COALESCE(sqlc.narg('timezone'), timezone),
  caption = COALESCE(sqlc.narg('caption'), caption),
  viewpoint = COALESCE(sqlc.narg('viewpoint'), viewpoint),
  is_cover = COALESCE(sqlc.narg('is_cover'), is_cover),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision') AND deleted_at IS NULL
RETURNING *;

-- name: DeletePhoto :one
UPDATE plant_photos SET deleted_at = now(), is_cover = false, revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $3 AND deleted_at IS NULL
RETURNING id;

-- name: ListPhotosByPlant :many
SELECT * FROM plant_photos
WHERE workspace_id = $1 AND plant_id = $2 AND deleted_at IS NULL
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL OR (created_at, id) < (sqlc.narg('after_created_at'), sqlc.narg('after_id')))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: GetCoverPhoto :one
SELECT * FROM plant_photos WHERE plant_id = $1 AND workspace_id = $2 AND is_cover AND deleted_at IS NULL;

-- name: ClearCoverForPlant :exec
UPDATE plant_photos SET is_cover = false, revision = revision + 1
WHERE plant_id = $1 AND workspace_id = $2 AND is_cover AND deleted_at IS NULL;

-- name: CountPhotosByPlant :many
SELECT plant_id, count(*)::int AS photo_count FROM plant_photos
WHERE workspace_id = $1 AND plant_id = ANY(sqlc.arg('plant_ids')::uuid[]) AND deleted_at IS NULL
GROUP BY plant_id;

-- name: GetLivePhotoLinkByAsset :one
-- photo assets never implicitly share: a live link (any plant) blocks
-- re-attachment; enforced under the asset row lock, so the check cannot race
SELECT * FROM plant_photos WHERE asset_id = $1 AND workspace_id = $2 AND deleted_at IS NULL;

-- name: CountLivePhotoLinksByAsset :one
SELECT count(*)::int AS link_count FROM plant_photos
WHERE asset_id = $1 AND workspace_id = $2 AND deleted_at IS NULL;

-- name: ListCoverPhotos :many
SELECT * FROM plant_photos
WHERE workspace_id = $1 AND plant_id = ANY(sqlc.arg('plant_ids')::uuid[])
  AND is_cover AND deleted_at IS NULL;

-- name: ListPhotosByIDs :many
-- includes soft-deleted rows: tombstoned journal entries still reference
-- their photo (photo.deletedAt is part of the frozen PhotoFields shape)
SELECT * FROM plant_photos WHERE workspace_id = $1 AND id = ANY(sqlc.arg('ids')::uuid[]);

-- name: ListPhotosWithAssetsVariantsByIDs :many
-- one-query batch hydration for journal pages (T18 query budget): photo +
-- its asset + all variants in a single join; soft-deleted rows included.
SELECT p.id AS p_id, p.workspace_id AS p_workspace_id, p.plant_id AS p_plant_id, p.asset_id AS p_asset_id,
  p.taken_at AS p_taken_at, p.date_source AS p_date_source, p.timezone AS p_timezone, p.caption AS p_caption,
  p.viewpoint AS p_viewpoint, p.is_cover AS p_is_cover, p.revision AS p_revision, p.deleted_at AS p_deleted_at,
  p.created_at AS p_created_at, p.updated_at AS p_updated_at,
  a.id AS a_id, a.workspace_id AS a_workspace_id, a.purpose AS a_purpose, a.state AS a_state,
  a.byte_count AS a_byte_count, a.mime_type AS a_mime_type, a.sha256 AS a_sha256, a.storage_key AS a_storage_key,
  a.display_name AS a_display_name, a.error_code AS a_error_code, a.expires_at AS a_expires_at,
  a.created_at AS a_created_at, a.updated_at AS a_updated_at,
  v.name AS v_name, v.width_px AS v_width_px, v.height_px AS v_height_px,
  v.storage_key AS v_storage_key, v.byte_count AS v_byte_count
FROM plant_photos p
JOIN assets a ON a.id = p.asset_id
LEFT JOIN asset_variants v ON v.asset_id = a.id
WHERE p.workspace_id = sqlc.arg('workspace_id') AND p.id = ANY(sqlc.arg('ids')::uuid[])
ORDER BY p.id, v.name;

-- name: LockPlantForPhotoChange :one
SELECT id FROM plants WHERE id=$1 AND workspace_id=$2 FOR UPDATE;
