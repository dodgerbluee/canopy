-- name: InsertReceipt :one
INSERT INTO purchase_receipts (id, workspace_id, purchase_id, asset_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetReceiptByID :one
SELECT * FROM purchase_receipts WHERE id = $1 AND workspace_id = $2;

-- name: ListReceiptsByPurchase :many
SELECT * FROM purchase_receipts WHERE purchase_id = $1;

-- name: DeleteReceipt :one
DELETE FROM purchase_receipts
WHERE id = sqlc.arg('id') AND purchase_id = sqlc.arg('purchase_id') AND workspace_id = sqlc.arg('workspace_id')
RETURNING id;

-- name: GetReceiptByPurchaseAsset :one
SELECT * FROM purchase_receipts WHERE purchase_id = $1 AND asset_id = $2 AND workspace_id = $3;

-- name: CountReceiptLinksByAsset :one
SELECT count(*)::int AS link_count FROM purchase_receipts
WHERE asset_id = $1 AND workspace_id = $2;

-- name: ListReceiptsByPurchases :many
SELECT * FROM purchase_receipts
WHERE workspace_id = $1 AND purchase_id = ANY(sqlc.arg('purchase_ids')::uuid[]);
