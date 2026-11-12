-- name: InsertPurchase :one
INSERT INTO purchases (id, workspace_id, seller, purchased_on, currency, total_minor, shipping_minor, tax_minor)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: InsertPurchaseLine :one
INSERT INTO purchase_lines (id, workspace_id, purchase_id, description, quantity, line_total_minor, position)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: InsertPurchaseAllocation :one
INSERT INTO purchase_allocations (id, workspace_id, purchase_line_id, plant_id, line_minor, tax_minor, shipping_minor, position)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetPurchase :one
SELECT * FROM purchases WHERE id = $1 AND workspace_id = $2;

-- name: LockPurchase :one
SELECT * FROM purchases WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: ListPurchases :many
SELECT * FROM purchases
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (COALESCE(sqlc.narg('include_voided'), false) OR voided_at IS NULL)
  AND (sqlc.narg('seller_query')::text IS NULL OR seller ILIKE '%' || sqlc.narg('seller_query') || '%')
  AND (sqlc.narg('from_date')::date IS NULL OR purchased_on >= sqlc.narg('from_date'))
  AND (sqlc.narg('to_date')::date IS NULL OR purchased_on <= sqlc.narg('to_date'))
  AND (sqlc.narg('currency')::text IS NULL OR currency = sqlc.narg('currency'))
  AND (
    sqlc.narg('after_id')::uuid IS NULL
    OR (sqlc.narg('after_purchased_on')::date IS NOT NULL AND (
          purchased_on < sqlc.narg('after_purchased_on')
          OR (purchased_on = sqlc.narg('after_purchased_on') AND id < sqlc.narg('after_id'))
          OR purchased_on IS NULL))
    OR (sqlc.narg('after_purchased_on')::date IS NULL AND purchased_on IS NULL AND id < sqlc.narg('after_id'))
  )
ORDER BY purchased_on DESC NULLS LAST, id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListPurchaseLinesPage :many
SELECT * FROM purchase_lines
WHERE purchase_id = sqlc.arg('purchase_id')
  AND (
    sqlc.narg('after_id')::uuid IS NULL
    OR (position, id) > (sqlc.narg('after_position')::smallint, sqlc.narg('after_id')::uuid)
  )
ORDER BY position, id
LIMIT sqlc.arg('limit_count');

-- name: ListAllocationsByPurchase :many
SELECT pa.* FROM purchase_allocations pa
JOIN purchase_lines pl ON pl.id = pa.purchase_line_id
WHERE pl.purchase_id = sqlc.arg('purchase_id')
ORDER BY pl.position, pa.position, pa.id;

-- name: GetPurchaseAllocation :one
SELECT pa.*, pl.purchase_id FROM purchase_allocations pa
JOIN purchase_lines pl ON pl.id = pa.purchase_line_id
WHERE pa.id = $1 AND pa.workspace_id = $2;

-- name: AcquisitionsForPurchase :many
SELECT a.* FROM acquisitions a
JOIN purchase_allocations pa ON pa.id = a.initial_purchase_allocation_id
JOIN purchase_lines pl ON pl.id = pa.purchase_line_id
WHERE pl.purchase_id = sqlc.arg('purchase_id') AND a.workspace_id = sqlc.arg('workspace_id')
ORDER BY a.id;

-- name: ListPurchaseLines :many
SELECT * FROM purchase_lines WHERE purchase_id = $1 ORDER BY position, id;

-- name: ListAllocationsForLines :many
SELECT * FROM purchase_allocations WHERE purchase_line_id = ANY(sqlc.arg('line_ids')::uuid[])
ORDER BY position, id;

-- name: UpdatePurchaseHeader :one
UPDATE purchases SET
  seller = COALESCE(sqlc.narg('seller'), seller),
  purchased_on = COALESCE(sqlc.narg('purchased_on'), purchased_on),
  currency = COALESCE(sqlc.narg('currency'), currency),
  total_minor = COALESCE(sqlc.narg('total_minor'), total_minor),
  shipping_minor = COALESCE(sqlc.narg('shipping_minor'), shipping_minor),
  tax_minor = COALESCE(sqlc.narg('tax_minor'), tax_minor),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision') AND voided_at IS NULL
RETURNING *;

-- name: VoidPurchase :one
UPDATE purchases SET voided_at = now(), revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $3 AND voided_at IS NULL
RETURNING *;

-- name: DeletePurchaseLines :exec
DELETE FROM purchase_lines WHERE purchase_id = $1;

-- name: ListPurchaseAllocationsByIDs :many
-- batch hydration for journal PURCHASE entries (one load per page)
SELECT * FROM purchase_allocations
WHERE workspace_id = sqlc.arg('workspace_id') AND id = ANY(sqlc.arg('ids')::uuid[]);

-- name: ListPurchaseAllocationsForPurchase :many
SELECT pa.* FROM purchase_allocations pa
JOIN purchase_lines pl ON pl.id = pa.purchase_line_id
WHERE pl.purchase_id = sqlc.arg('purchase_id') AND pa.workspace_id = sqlc.arg('workspace_id');
