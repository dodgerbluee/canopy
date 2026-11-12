-- name: SpendingByCurrency :many
SELECT p.currency, sum(p.total_minor)::numeric AS total_minor
FROM purchases p
WHERE p.workspace_id = sqlc.arg('workspace_id') AND p.voided_at IS NULL
  AND (sqlc.narg('from_date')::date IS NULL OR p.purchased_on >= sqlc.narg('from_date'))
  AND (sqlc.narg('to_date')::date IS NULL OR p.purchased_on <= sqlc.narg('to_date'))
GROUP BY p.currency;

-- name: PlantLifetimeSpendingByCurrency :many
SELECT p.currency, sum(pa.line_minor + pa.tax_minor + pa.shipping_minor)::numeric AS total_minor
FROM purchase_allocations pa
JOIN purchase_lines pl ON pl.id = pa.purchase_line_id
JOIN purchases p ON p.id = pl.purchase_id
WHERE pa.workspace_id = sqlc.arg('workspace_id') AND pa.plant_id = sqlc.narg('plant_id') AND p.voided_at IS NULL
  AND (sqlc.narg('from_date')::date IS NULL OR p.purchased_on >= sqlc.narg('from_date'))
  AND (sqlc.narg('to_date')::date IS NULL OR p.purchased_on <= sqlc.narg('to_date'))
GROUP BY p.currency;

-- name: UnknownAcquisitionCostCount :one
SELECT count(*) FROM acquisitions a
WHERE a.workspace_id = $1 AND a.pricing_state = 'UNKNOWN';

-- name: MissingAcquisitionRecordCount :one
SELECT count(*) FROM plants pl
WHERE pl.workspace_id = $1 AND NOT EXISTS (SELECT 1 FROM acquisitions a WHERE a.plant_id = pl.id);
