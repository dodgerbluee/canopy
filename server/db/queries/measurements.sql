-- name: InsertMeasurement :one
INSERT INTO measurements (id, workspace_id, plant_id, quantity_kind, value, unit, observed_at, method, uncertainty, activity_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateMeasurement :one
UPDATE measurements SET
  value = COALESCE(sqlc.narg('value'), value),
  uncertainty = COALESCE(sqlc.narg('uncertainty'), uncertainty),
  observed_at = COALESCE(sqlc.narg('observed_at'), observed_at),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: DeleteMeasurement :one
DELETE FROM measurements
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING id;

-- name: GetMeasurement :one
SELECT * FROM measurements WHERE id = $1 AND workspace_id = $2;

-- name: ListMeasurementsByPlant :many
SELECT * FROM measurements
WHERE workspace_id = $1 AND plant_id = $2
  AND (sqlc.narg('after_observed_at')::timestamptz IS NULL OR (observed_at, id) < (sqlc.narg('after_observed_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY observed_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListMeasurementsByIDs :many
SELECT * FROM measurements WHERE workspace_id = $1 AND id = ANY(sqlc.arg('ids')::uuid[]);
