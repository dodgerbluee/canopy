-- name: GetPlant :one
SELECT * FROM plants WHERE id = $1 AND workspace_id = $2;

-- name: ListPlantsNameAsc :many
SELECT * FROM plants p
WHERE p.workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('statuses')::text[] IS NULL OR p.status = ANY(sqlc.narg('statuses')::text[]::plant_status[]))
  AND (sqlc.narg('category')::plant_category IS NULL OR p.category = sqlc.narg('category'))
  AND (sqlc.narg('query')::text IS NULL
       OR p.name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.scientific_name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.cultivar ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('location_id')::uuid IS NULL OR EXISTS (
        SELECT 1 FROM plant_contexts pc
        WHERE pc.plant_id = p.id AND pc.location_id = sqlc.narg('location_id')
          AND pc.effective_from = (
            SELECT max(pc2.effective_from) FROM plant_contexts pc2
            WHERE pc2.plant_id = p.id AND pc2.effective_from <= now())))
  AND (sqlc.narg('tag_ids')::uuid[] IS NULL OR (
        SELECT count(DISTINCT pt.tag_id) FROM plant_tags pt
        WHERE pt.plant_id = p.id AND pt.tag_id = ANY(sqlc.narg('tag_ids')::uuid[])
      ) = cardinality(sqlc.narg('tag_ids')::uuid[]))
  AND (sqlc.narg('after_name')::text IS NULL OR (lower(p.name), p.id) > (lower(sqlc.narg('after_name')), sqlc.narg('after_id')::uuid))
ORDER BY lower(p.name), p.id
LIMIT sqlc.arg('limit_count');

-- name: ListPlantsNameDesc :many
SELECT * FROM plants p
WHERE p.workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('statuses')::text[] IS NULL OR p.status = ANY(sqlc.narg('statuses')::text[]::plant_status[]))
  AND (sqlc.narg('category')::plant_category IS NULL OR p.category = sqlc.narg('category'))
  AND (sqlc.narg('query')::text IS NULL
       OR p.name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.scientific_name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.cultivar ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('location_id')::uuid IS NULL OR EXISTS (
        SELECT 1 FROM plant_contexts pc
        WHERE pc.plant_id = p.id AND pc.location_id = sqlc.narg('location_id')
          AND pc.effective_from = (
            SELECT max(pc2.effective_from) FROM plant_contexts pc2
            WHERE pc2.plant_id = p.id AND pc2.effective_from <= now())))
  AND (sqlc.narg('tag_ids')::uuid[] IS NULL OR (
        SELECT count(DISTINCT pt.tag_id) FROM plant_tags pt
        WHERE pt.plant_id = p.id AND pt.tag_id = ANY(sqlc.narg('tag_ids')::uuid[])
      ) = cardinality(sqlc.narg('tag_ids')::uuid[]))
  AND (sqlc.narg('after_name')::text IS NULL OR (lower(p.name), p.id) < (lower(sqlc.narg('after_name')), sqlc.narg('after_id')::uuid))
ORDER BY lower(p.name) DESC, p.id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListPlantsAcquiredOnDesc :many
SELECT * FROM plants p
WHERE p.workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('statuses')::text[] IS NULL OR p.status = ANY(sqlc.narg('statuses')::text[]::plant_status[]))
  AND (sqlc.narg('category')::plant_category IS NULL OR p.category = sqlc.narg('category'))
  AND (sqlc.narg('query')::text IS NULL
       OR p.name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.scientific_name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.cultivar ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('location_id')::uuid IS NULL OR EXISTS (
        SELECT 1 FROM plant_contexts pc
        WHERE pc.plant_id = p.id AND pc.location_id = sqlc.narg('location_id')
          AND pc.effective_from = (
            SELECT max(pc2.effective_from) FROM plant_contexts pc2
            WHERE pc2.plant_id = p.id AND pc2.effective_from <= now())))
  AND (sqlc.narg('tag_ids')::uuid[] IS NULL OR (
        SELECT count(DISTINCT pt.tag_id) FROM plant_tags pt
        WHERE pt.plant_id = p.id AND pt.tag_id = ANY(sqlc.narg('tag_ids')::uuid[])
      ) = cardinality(sqlc.narg('tag_ids')::uuid[]))
  AND (sqlc.narg('after_acquired_on')::date IS NULL
       OR (COALESCE(p.acquired_on, DATE '0001-01-01'), p.id) < (sqlc.narg('after_acquired_on')::date, sqlc.narg('after_id')::uuid))
ORDER BY COALESCE(p.acquired_on, DATE '0001-01-01') DESC, p.id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListPlantsCreatedDesc :many
SELECT * FROM plants p
WHERE p.workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('statuses')::text[] IS NULL OR p.status = ANY(sqlc.narg('statuses')::text[]::plant_status[]))
  AND (sqlc.narg('category')::plant_category IS NULL OR p.category = sqlc.narg('category'))
  AND (sqlc.narg('query')::text IS NULL
       OR p.name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.scientific_name ILIKE '%' || sqlc.narg('query') || '%'
       OR p.cultivar ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('location_id')::uuid IS NULL OR EXISTS (
        SELECT 1 FROM plant_contexts pc
        WHERE pc.plant_id = p.id AND pc.location_id = sqlc.narg('location_id')
          AND pc.effective_from = (
            SELECT max(pc2.effective_from) FROM plant_contexts pc2
            WHERE pc2.plant_id = p.id AND pc2.effective_from <= now())))
  AND (sqlc.narg('tag_ids')::uuid[] IS NULL OR (
        SELECT count(DISTINCT pt.tag_id) FROM plant_tags pt
        WHERE pt.plant_id = p.id AND pt.tag_id = ANY(sqlc.narg('tag_ids')::uuid[])
      ) = cardinality(sqlc.narg('tag_ids')::uuid[]))
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL OR (p.created_at, p.id) < (sqlc.narg('after_created_at'), sqlc.narg('after_id')::uuid))
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg('limit_count');

-- name: InsertPlant :one
INSERT INTO plants (id, workspace_id, name, scientific_name, cultivar, rootstock, type, count, category, acquired_on, planted_on, estimated_age_years)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: UpdatePlant :one
UPDATE plants SET
  name = COALESCE(sqlc.narg('name'), name),
  scientific_name = COALESCE(sqlc.narg('scientific_name'), scientific_name),
  cultivar = COALESCE(sqlc.narg('cultivar'), cultivar),
  rootstock = COALESCE(sqlc.narg('rootstock'), rootstock),
  type = COALESCE(sqlc.narg('type'), type),
  count = COALESCE(sqlc.narg('count'), count),
  category = COALESCE(sqlc.narg('category'), category),
  planted_on = COALESCE(sqlc.narg('planted_on'), planted_on),
  estimated_age_years = COALESCE(sqlc.narg('estimated_age_years'), estimated_age_years),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: SetPlantStatus :one
UPDATE plants SET status = $2, revision = revision + 1
WHERE id = $1 AND workspace_id = $3 AND revision = $4
RETURNING *;

-- name: SetPlantAcquiredOnProjection :exec
-- acquisition projection only; never exposed as an independent edit
UPDATE plants SET acquired_on = $2 WHERE id = $1 AND workspace_id = $3;

-- name: BumpPlantRevision :one
UPDATE plants SET revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: DeletePlant :one
DELETE FROM plants WHERE id = $1 AND workspace_id = $2 AND revision = $3 RETURNING id;

-- name: CountPlantsWithCover :one
SELECT count(*) AS total,
  count(*) FILTER (WHERE EXISTS (
    SELECT 1 FROM plant_photos pp WHERE pp.plant_id = p.id AND pp.is_cover AND pp.deleted_at IS NULL
  )) AS with_cover
FROM plants p WHERE p.workspace_id = $1 AND p.status = 'ACTIVE';
