-- T29 support surface: worker reads, attempt-cost reservations, budget
-- ledger split (encumbered vs spent), and the bounded snapshot reads.
-- The frozen T02.ai surface (analysis.sql) stays untouched; this file is
-- additive per the lane rule.

-- name: GetAnalysisRunByID :one
-- worker path: the job carries only the run id; workspace scoping is
-- enforced by the row itself (the run was inserted by this workspace).
SELECT * FROM analysis_runs WHERE id = $1;

-- name: FindPriorRunBySemanticKey :one
-- same-hash reuse (03: "no repeat analyses without new input hash unless
-- the owner explicitly reruns"): newest run for the semantic key in any
-- state; the active case is covered by FindActiveRunBySemanticKey.
SELECT * FROM analysis_runs
WHERE workspace_id = $1 AND semantic_key = $2
ORDER BY requested_at DESC, id DESC
LIMIT 1;

-- name: ReserveAttemptCost :one
-- 03: every potentially billable attempt (retry or schema repair)
-- atomically reserves its own maximum cost BEFORE dispatch. The run's
-- reservation is the cumulative encumbrance of unreconciled attempts.
UPDATE analysis_runs SET
  cost_reserved_micro_usd = cost_reserved_micro_usd + sqlc.arg('extra_micro_usd'),
  updated_at = now()
WHERE id = sqlc.arg('id') AND generation = sqlc.arg('generation') AND state = 'RUNNING'
RETURNING *;

-- name: LockWorkspaceBudget :exec
-- serializes budget check+insert and check+reserve across processes so
-- "maximum possible known plus encumbered spend cannot exceed ceiling"
-- holds under concurrent requests (03 budget rules).
SELECT pg_advisory_xact_lock(hashtextextended($1, 0));

-- name: BudgetMonthLedger :one
-- UTC-month ledger split: encumbered = reservations of runs whose actual
-- cost is still unknown (may still be charged); spent = reconciled
-- actuals. A run belongs to the UTC month it was requested in; prior-
-- month reservations never migrate into a new month's allowance.
SELECT
  COALESCE(SUM(cost_reserved_micro_usd) FILTER (WHERE cost_actual_micro_usd IS NULL), 0)::bigint AS encumbered_micro_usd,
  COALESCE(SUM(cost_actual_micro_usd), 0)::bigint AS spent_micro_usd
FROM analysis_runs
WHERE workspace_id = $1 AND requested_at >= $2 AND requested_at < $3;

-- ---------- bounded snapshot reads (03: latest 12 activities, at most
-- 4 image derivatives, retrieved approved guidance, weather freshness) --

-- name: SnapshotActivities :many
SELECT id, kind, occurred_at, timezone, note, quantity, unit, revision
FROM activities
WHERE workspace_id = $1 AND plant_id = $2 AND voided_at IS NULL
ORDER BY occurred_at DESC, id DESC
LIMIT 12;

-- name: SnapshotMeasurements :many
SELECT id, quantity_kind, value, unit, observed_at, revision
FROM measurements
WHERE workspace_id = $1 AND plant_id = $2
ORDER BY observed_at DESC, id DESC
LIMIT 12;

-- name: SnapshotPhotos :many
SELECT pp.id, pp.asset_id, pp.taken_at, pp.revision, a.sha256
FROM plant_photos pp
JOIN assets a ON a.id = pp.asset_id
WHERE pp.workspace_id = $1 AND pp.plant_id = $2 AND pp.deleted_at IS NULL
  AND a.purpose = 'PHOTO' AND a.state = 'READY'
ORDER BY pp.created_at DESC, pp.id DESC
LIMIT 4;

-- name: GetReadyPhotoForRun :one
-- VISION pair validation: photo must be live, in-workspace, and its asset
-- READY; plant_id is returned so the service can enforce same-plant pairs.
SELECT pp.id, pp.plant_id, pp.asset_id, pp.taken_at, a.sha256
FROM plant_photos pp
JOIN assets a ON a.id = pp.asset_id
WHERE pp.id = $1 AND pp.workspace_id = $2 AND pp.deleted_at IS NULL
  AND a.purpose = 'PHOTO' AND a.state = 'READY';

-- name: GetReadyReceiptForRun :one
SELECT a.id, a.state
FROM assets a
WHERE a.id = $1 AND a.workspace_id = $2 AND a.purpose = 'RECEIPT' AND a.state = 'READY';

-- name: SnapshotGuidance :many
-- approved guidance = published, not-retired care profile versions.
SELECT id, profile_id, version
FROM care_profile_versions
WHERE workspace_id = $1 AND status = 'REVIEWED' AND published_at IS NOT NULL AND retired_at IS NULL
ORDER BY version DESC
LIMIT 8;

-- name: SnapshotWeather :many
-- weather freshness: unexpired snapshots, newest first.
SELECT id, location_id, provider, fetched_at, valid_from, valid_until,
       high_temp_c, low_temp_c, rainfall_mm, et0_mm
FROM weather_snapshots
WHERE workspace_id = $1 AND expires_at > now()
ORDER BY fetched_at DESC
LIMIT 5;

-- name: GetReadyAssetForAdapter :one
-- Adapter file access: READY asset in-workspace, storage key + mime.
SELECT id, storage_key, mime_type, byte_count
FROM assets
WHERE id = $1 AND workspace_id = $2 AND state = 'READY';

-- name: GetAssetVariantsForAdapter :many
-- Orientation-corrected variants for OCR/vision input preference.
SELECT name, storage_key
FROM asset_variants
WHERE asset_id = $1
ORDER BY name;

-- name: SnapshotPurchaseEvidence :many
-- T33 typed cost evidence for SYNTHESIS snapshots: purchases linked to the
-- plant through allocations, with the exact plant-allocated amount per
-- purchase (same arithmetic as PlantLifetimeSpendingByCurrency, so the
-- snapshot totals match the money ledger by construction).
SELECT p.id, p.currency, p.purchased_on,
       sum(pa.line_minor + pa.tax_minor + pa.shipping_minor)::numeric AS plant_minor
FROM purchases p
JOIN purchase_lines pl ON pl.purchase_id = p.id
JOIN purchase_allocations pa ON pa.purchase_line_id = pl.id
WHERE pa.workspace_id = sqlc.arg('workspace_id') AND pa.plant_id = sqlc.arg('plant_id')
  AND p.voided_at IS NULL
GROUP BY p.id, p.currency, p.purchased_on
ORDER BY p.purchased_on NULLS LAST, p.id;

-- name: LockSuggestion :one
-- Accept/dismiss serialize on the suggestion row; the exact-revision guard
-- stays the contract rule, the lock only makes check+write atomic.
SELECT * FROM suggestions WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: RunsCitingSource :many
-- SUCCEEDED runs whose validated results cite a guidance source (by
-- profile id or version id): retiring either must force owner review of
-- the pending suggestions those runs produced.
SELECT id FROM analysis_runs
WHERE workspace_id = sqlc.arg('workspace_id') AND state = 'SUCCEEDED' AND result IS NOT NULL
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(result->'citations', '[]'::jsonb)) c
              WHERE c->>'sourceId' = sqlc.arg('source_id')::text
                 OR c->>'sourceVersionId' = sqlc.arg('source_version_id')::text);

-- name: SnapshotCostTotals :many
-- Per-currency authoritative totals for SYNTHESIS snapshots: identical
-- joins/filters to PlantLifetimeSpendingByCurrency, so snapshot totals
-- equal the money ledger by construction (T33 acceptance rule).
SELECT p.currency,
       sum(pa.line_minor + pa.tax_minor + pa.shipping_minor)::numeric AS total_minor,
       count(DISTINCT p.id)::bigint AS purchase_count
FROM purchases p
JOIN purchase_lines pl ON pl.purchase_id = p.id
JOIN purchase_allocations pa ON pa.purchase_line_id = pl.id
WHERE pa.workspace_id = sqlc.arg('workspace_id') AND pa.plant_id = sqlc.arg('plant_id')
  AND p.voided_at IS NULL
GROUP BY p.currency
ORDER BY p.currency;

-- name: ListAnalysisRunsByIDs :many
-- Journal INSIGHT member hydration (T33): batch-load the runs behind
-- accepted-synthesis journal lines.
SELECT id, kind FROM analysis_runs
WHERE workspace_id = sqlc.arg('workspace_id') AND id = ANY(sqlc.arg('ids')::uuid[]);
