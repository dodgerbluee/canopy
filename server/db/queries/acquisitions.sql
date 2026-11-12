-- name: GetAcquisition :one
SELECT * FROM acquisitions WHERE id = $1 AND workspace_id = $2;

-- name: GetAcquisitionByPlant :one
SELECT * FROM acquisitions WHERE plant_id = $1 AND workspace_id = $2;

-- name: LockAcquisitionByPlant :one
SELECT * FROM acquisitions WHERE plant_id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: LockAcquisition :one
SELECT * FROM acquisitions WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: InsertAcquisition :one
INSERT INTO acquisitions (id, workspace_id, plant_id, seller, acquired_on, pricing_state, initial_purchase_allocation_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateAcquisitionProvenance :one
UPDATE acquisitions SET
  seller = COALESCE(sqlc.narg('seller'), seller),
  acquired_on = COALESCE(sqlc.narg('acquired_on'), acquired_on),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: PriceAcquisitionLink :one
UPDATE acquisitions SET
  pricing_state = 'KNOWN',
  initial_purchase_allocation_id = sqlc.narg('allocation_id'),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: RevertAcquisitionUnknown :one
UPDATE acquisitions SET
  pricing_state = 'UNKNOWN',
  initial_purchase_allocation_id = NULL,
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: AcquisitionsForAllocations :many
SELECT * FROM acquisitions WHERE workspace_id = $1 AND initial_purchase_allocation_id = ANY(sqlc.arg('allocation_ids')::uuid[]);
