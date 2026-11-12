-- name: GetWorkspace :one
SELECT * FROM workspace WHERE owner_id = $1;

-- name: InsertWorkspace :one
INSERT INTO workspace (id, owner_id, display_name, timezone, default_currency)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetWorkspaceByID :one
SELECT * FROM workspace WHERE id = $1;

-- name: UpdateWorkspaceSettings :one
UPDATE workspace SET
  display_name = COALESCE(sqlc.narg('display_name'), display_name),
  timezone = COALESCE(sqlc.narg('timezone'), timezone),
  default_currency = COALESCE(sqlc.narg('default_currency'), default_currency),
  latitude = COALESCE(sqlc.narg('latitude'), latitude),
  longitude = COALESCE(sqlc.narg('longitude'), longitude),
  location_label = sqlc.narg('location_label'),
  settings_revision = settings_revision + 1
WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') AND settings_revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: BumpPrivacyEpoch :one
UPDATE workspace SET privacy_epoch = privacy_epoch + 1
WHERE id = $1
RETURNING privacy_epoch;
