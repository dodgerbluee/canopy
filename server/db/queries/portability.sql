-- T38.a portability: purge ledger + fencing, maintenance quiesce, and
-- the workspace-scoped export snapshot reads.

-- ---------- maintenance quiesce ----------

-- name: GetMaintenanceFlag :one
SELECT maintenance FROM system_state WHERE id = true;

-- name: SetMaintenanceFlag :one
UPDATE system_state SET maintenance = sqlc.arg('maintenance'), updated_at = now()
WHERE id = true
RETURNING maintenance;

-- ---------- deletion ledger (minimal tombstones) ----------

-- name: InsertTombstone :one
INSERT INTO deletion_tombstones (id, workspace_id, entity_kind, entity_id, action, actor_id, occurred_at, privacy_epoch)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (workspace_id, entity_kind, entity_id) DO NOTHING
RETURNING *;

-- name: ListTombstones :many
SELECT * FROM deletion_tombstones
WHERE workspace_id = $1
ORDER BY occurred_at, id;

-- name: MaxTombstoneEpoch :one
SELECT COALESCE(MAX(privacy_epoch), 0)::bigint AS max_epoch
FROM deletion_tombstones WHERE workspace_id = $1;

-- ---------- plant purge ----------

-- name: ClearPlantAllocations :exec
-- deleted-plant marker: the allocation keeps its amounts and line, only
-- the plant reference is removed (03: totals preserved, references
-- replaced with deleted-plant markers)
UPDATE purchase_allocations SET plant_id = NULL
WHERE workspace_id = sqlc.arg('workspace_id') AND plant_id = sqlc.arg('plant_id');

-- name: ListPlantPhotoRows :many
SELECT id, asset_id FROM plant_photos
WHERE workspace_id = $1 AND plant_id = $2 AND deleted_at IS NULL
ORDER BY id;

-- name: DeletePlantPhotoRow :exec
DELETE FROM plant_photos WHERE workspace_id = $1 AND id = $2;

-- name: ListPlantOwnedIDs :many
-- every entity id that cascades away with the plant; feeds tombstones
-- and the reverse-reference scans for receipts and audit rows
SELECT 'journal_entry'::text AS entity_kind, je.id FROM journal_entries je WHERE je.workspace_id = $1 AND je.plant_id = $2
UNION ALL
SELECT 'activity', ac.id FROM activities ac WHERE ac.workspace_id = $1 AND ac.plant_id = $2
UNION ALL
SELECT 'measurement', m.id FROM measurements m WHERE m.workspace_id = $1 AND m.plant_id = $2
UNION ALL
SELECT 'acquisition', aq.id FROM acquisitions aq WHERE aq.workspace_id = $1 AND aq.plant_id = $2
UNION ALL
SELECT 'plant_context', pc.id FROM plant_contexts pc WHERE pc.workspace_id = $1 AND pc.plant_id = $2
ORDER BY entity_kind, id;

-- name: TombstoneReceiptsTouching :exec
-- stored responses that reference any purged id replay as RESOURCE_GONE;
-- operation/key/hash stay until their original TTL. The purge command's
-- own receipt replays its minimal success instead.
UPDATE mutation_receipts SET gone = true, result = '{"gone": true}'::jsonb
WHERE workspace_id = sqlc.arg('workspace_id') AND NOT gone AND operation <> 'purgePlant'
  AND EXISTS (
    SELECT 1 FROM unnest(sqlc.arg('refs')::text[]) AS ref
    WHERE strpos(result::text, ref) > 0
  );

-- name: RedactAuditTouching :exec
-- redact prior content for the purged aggregate and any audit row whose
-- bounded prior values mention a purged id; actor/action/timestamp/
-- aggregate id/revision stay (03 deletion matrix).
UPDATE audit_revisions SET prior_values = NULL
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (aggregate_id = ANY(sqlc.arg('aggregate_ids')::uuid[])
       OR EXISTS (
         SELECT 1 FROM unnest(sqlc.arg('refs')::text[]) AS ref
         WHERE prior_values IS NOT NULL AND strpos(prior_values::text, ref) > 0
       ));

-- ---------- export snapshot (JSONL-ready rows) ----------

-- name: ExportWorkspaceRow :one
SELECT to_jsonb(t) AS doc FROM workspace t WHERE t.id = $1;

-- name: ExportPlants :many
SELECT to_jsonb(t) AS row FROM plants t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportLocations :many
SELECT to_jsonb(t) AS row FROM locations t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportTags :many
SELECT to_jsonb(t) AS row FROM tags t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportPlantTags :many
SELECT to_jsonb(t) AS row FROM plant_tags t WHERE t.workspace_id = $1 ORDER BY t.plant_id, t.tag_id;

-- name: ExportPlantContexts :many
SELECT to_jsonb(t) AS row FROM plant_contexts t WHERE t.workspace_id = $1 ORDER BY t.plant_id, t.effective_from, t.id;

-- name: ExportActivities :many
SELECT to_jsonb(t) AS row FROM activities t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportMeasurements :many
SELECT to_jsonb(t) AS row FROM measurements t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportPlantPhotos :many
SELECT to_jsonb(t) AS row FROM plant_photos t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportAssets :many
SELECT to_jsonb(t) AS row FROM assets t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportAssetVariants :many
SELECT to_jsonb(t) AS row FROM asset_variants t
JOIN assets a ON a.id = t.asset_id
WHERE a.workspace_id = $1 ORDER BY t.asset_id, t.name;

-- name: ExportPurchases :many
SELECT to_jsonb(t) AS row FROM purchases t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportPurchaseLines :many
SELECT to_jsonb(t) AS row FROM purchase_lines t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportPurchaseAllocations :many
SELECT to_jsonb(t) AS row FROM purchase_allocations t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportAcquisitions :many
SELECT to_jsonb(t) AS row FROM acquisitions t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportPurchaseReceipts :many
SELECT to_jsonb(t) AS row FROM purchase_receipts t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportJournalEntries :many
SELECT to_jsonb(t) AS row FROM journal_entries t WHERE t.workspace_id = $1 ORDER BY t.id;

-- name: ExportFinancialTotals :many
-- reconciliation anchor: live purchase totals per currency
SELECT currency, SUM(total_minor)::bigint AS total_minor
FROM purchases WHERE workspace_id = $1 AND voided_at IS NULL
GROUP BY currency ORDER BY currency;

-- name: ExportAllocationTotals :one
-- reconciliation anchor: allocation sums (amounts survive plant purge)
SELECT COALESCE(SUM(line_minor + tax_minor + shipping_minor), 0)::bigint AS allocated_minor
FROM purchase_allocations WHERE workspace_id = $1;

-- ---------- export job lifecycle ----------

-- name: ListLiveExportJobs :many
SELECT id FROM export_jobs
WHERE workspace_id = $1 AND state IN ('QUEUED', 'RUNNING', 'READY')
ORDER BY id;

-- name: StartExportJob :one
UPDATE export_jobs SET state = 'RUNNING'
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND state = 'QUEUED'
RETURNING *;

-- name: GetWorkspaceEpoch :one
SELECT privacy_epoch FROM workspace WHERE id = $1;

-- name: GetExportJobForWorker :one
-- worker-side lookup: the job id is server-generated, workspace scoping
-- is enforced at the API boundary, not here.
SELECT * FROM export_jobs WHERE id = $1;

-- name: ListExportMediaKeys :many
-- every stored object an export must carry: originals and derivatives
-- of READY assets
SELECT a.storage_key AS storage_key FROM assets a
WHERE a.workspace_id = $1 AND a.state = 'READY'
UNION
SELECT v.storage_key FROM asset_variants v
JOIN assets a ON a.id = v.asset_id
WHERE a.workspace_id = $1 AND a.state = 'READY'
ORDER BY storage_key;
