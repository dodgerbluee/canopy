-- name: InsertActivity :one
INSERT INTO activities (id, workspace_id, plant_id, kind, occurred_at, timezone, note, quantity, unit, product)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateActivity :one
UPDATE activities SET
  occurred_at = COALESCE(sqlc.narg('occurred_at'), occurred_at),
  timezone = COALESCE(sqlc.narg('timezone'), timezone),
  note = COALESCE(sqlc.narg('note'), note),
  quantity = COALESCE(sqlc.narg('quantity'), quantity),
  unit = COALESCE(sqlc.narg('unit'), unit),
  product = COALESCE(sqlc.narg('product'), product),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision') AND voided_at IS NULL
RETURNING *;

-- name: VoidActivity :one
UPDATE activities SET voided_at = now(), revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $3 AND voided_at IS NULL
RETURNING *;

-- name: GetActivity :one
SELECT * FROM activities WHERE id = $1 AND workspace_id = $2;

-- name: ListActivitiesByPlant :many
SELECT * FROM activities
WHERE workspace_id = $1 AND plant_id = $2
  AND (sqlc.narg('after_occurred_at')::timestamptz IS NULL OR (occurred_at, id) < (sqlc.narg('after_occurred_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: LatestActivitiesForPlants :many
-- bounded selection for detail pages and analysis snapshots: <= N per plant in one statement
SELECT a.* FROM unnest(sqlc.arg('plant_ids')::uuid[]) AS pid(plant_id)
CROSS JOIN LATERAL (
  SELECT * FROM activities act
  WHERE act.plant_id = pid.plant_id AND act.workspace_id = sqlc.arg('workspace_id') AND act.voided_at IS NULL
  ORDER BY act.occurred_at DESC, act.id DESC
  LIMIT sqlc.arg('per_plant')
) a
ORDER BY a.plant_id, a.occurred_at DESC;

-- name: ListActivitiesByIDs :many
-- journal hydration: voided activities stay selectable (voidedAt is exposed)
SELECT * FROM activities WHERE workspace_id = $1 AND id = ANY(sqlc.arg('ids')::uuid[]);
