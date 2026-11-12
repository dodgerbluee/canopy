-- name: ListTags :many
SELECT * FROM tags
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (COALESCE(sqlc.narg('include_archived'), false) OR archived_at IS NULL)
  AND (sqlc.narg('query')::text IS NULL OR label ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('after_label')::text IS NULL OR (lower(label), id) > (lower(sqlc.narg('after_label')), sqlc.narg('after_id')::uuid))
ORDER BY lower(label), id
LIMIT sqlc.arg('limit_count');

-- name: GetTag :one
SELECT * FROM tags WHERE id = $1 AND workspace_id = $2;

-- name: GetTagByLabel :one
SELECT * FROM tags WHERE workspace_id = $1 AND lower(label) = lower(sqlc.arg('label')::text) AND archived_at IS NULL;

-- name: InsertTag :one
INSERT INTO tags (id, workspace_id, label)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateTagLabel :one
UPDATE tags SET label = $3, revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $4
RETURNING *;

-- name: ArchiveTag :one
UPDATE tags SET archived_at = now(), revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $3 AND archived_at IS NULL
RETURNING *;

-- name: ListTagsForPlant :many
SELECT t.* FROM tags t JOIN plant_tags pt ON pt.tag_id = t.id
WHERE pt.plant_id = $1 ORDER BY lower(t.label), t.id;

-- name: ReplacePlantTags :exec
DELETE FROM plant_tags WHERE plant_id = $1 AND workspace_id = $2;

-- name: InsertPlantTag :exec
INSERT INTO plant_tags (workspace_id, plant_id, tag_id) VALUES ($1, $2, $3);

-- name: FilterPlantIDsByTags :many
SELECT pt.plant_id FROM plant_tags pt
WHERE pt.workspace_id = $1 AND pt.tag_id = ANY(sqlc.arg('tag_ids')::uuid[])
GROUP BY pt.plant_id HAVING count(DISTINCT pt.tag_id) = cardinality(sqlc.arg('tag_ids')::uuid[]);
