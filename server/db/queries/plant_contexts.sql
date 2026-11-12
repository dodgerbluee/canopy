-- name: InsertPlantContext :one
INSERT INTO plant_contexts (id, workspace_id, plant_id, effective_from, location_id, placement, pot_volume_liters, medium_note, light_note)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetCurrentContext :one
-- clock_timestamp(), not now(): now() is the transaction start, which
-- would hide a just-inserted effective-now row from the collection-care
-- hook running inside the same transaction.
SELECT * FROM plant_contexts
WHERE plant_id = $1 AND effective_from <= clock_timestamp()
ORDER BY effective_from DESC, created_at DESC
LIMIT 1;

-- name: ListContextsForPlant :many
SELECT * FROM plant_contexts WHERE plant_id = $1 ORDER BY effective_from DESC LIMIT sqlc.arg('limit_count');
