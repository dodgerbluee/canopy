-- name: ListLocations :many
SELECT * FROM locations WHERE workspace_id = $1 ORDER BY lower(name), id;

-- name: GetLocation :one
SELECT * FROM locations WHERE id = $1 AND workspace_id = $2;

-- name: InsertLocation :one
INSERT INTO locations (id, workspace_id, name, parent_id, situation, latitude, longitude, label)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateLocation :one
UPDATE locations SET
  name = COALESCE(sqlc.narg('name'), name),
  parent_id = sqlc.narg('parent_id'),
  situation = COALESCE(sqlc.narg('situation'), situation),
  latitude = COALESCE(sqlc.narg('latitude'), latitude),
  longitude = COALESCE(sqlc.narg('longitude'), longitude),
  label = sqlc.narg('label'),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: LocationAncestors :many
WITH RECURSIVE anc(id, parent_id, depth) AS (
  SELECT l.id, l.parent_id, 1 FROM locations l WHERE l.id = $1 AND l.workspace_id = $2
  UNION ALL
  SELECT l.id, l.parent_id, a.depth + 1 FROM locations l JOIN anc a ON l.id = a.parent_id WHERE a.depth < 10
)
SELECT * FROM anc;

-- name: LocationSubtreeDepth :one
WITH RECURSIVE sub(id, depth) AS (
  SELECT l.id, 1 FROM locations l WHERE l.id = $1 AND l.workspace_id = $2
  UNION ALL
  SELECT l.id, s.depth + 1 FROM locations l JOIN sub s ON l.parent_id = s.id WHERE s.depth < 10
)
SELECT COALESCE(max(depth), 0)::int AS max_depth FROM sub;
