-- name: InsertJournalEntry :one
INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpsertJournalEntry :one
INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (workspace_id, entry_type, source_id, plant_id) DO UPDATE SET
  occurred_at = EXCLUDED.occurred_at, summary = EXCLUDED.summary, tombstoned_at = NULL
RETURNING *;

-- name: TombstoneJournalEntry :many
UPDATE journal_entries SET tombstoned_at = now()
WHERE workspace_id = $1 AND entry_type = $2 AND source_id = $3
RETURNING id;

-- name: DeletePurchaseJournalEntries :exec
-- PURCHASE entries are keyed by allocation id (one entry per allocation);
-- re-projection clears the purchase's whole entry set first.
DELETE FROM journal_entries
WHERE journal_entries.workspace_id = $1 AND journal_entries.entry_type = 'PURCHASE'
  AND source_id IN (
    SELECT pa.id FROM purchase_allocations pa
    JOIN purchase_lines pl ON pl.id = pa.purchase_line_id
    WHERE pl.purchase_id = $2);

-- name: TombstonePurchaseJournalEntries :many
UPDATE journal_entries SET tombstoned_at = now()
WHERE journal_entries.workspace_id = $1 AND journal_entries.entry_type = 'PURCHASE'
  AND journal_entries.tombstoned_at IS NULL
  AND source_id IN (
    SELECT pa.id FROM purchase_allocations pa
    JOIN purchase_lines pl ON pl.id = pa.purchase_line_id
    WHERE pl.purchase_id = $2)
RETURNING id;

-- name: ListJournal :many
SELECT * FROM journal_entries
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('plant_id')::uuid IS NULL OR plant_id = sqlc.narg('plant_id'))
  AND (sqlc.narg('entry_types')::text[] IS NULL OR entry_type::text = ANY(sqlc.narg('entry_types')::text[]))
  AND (COALESCE(sqlc.narg('include_tombstoned'), false) OR tombstoned_at IS NULL)
  AND (sqlc.narg('from_at')::timestamptz IS NULL OR occurred_at >= sqlc.narg('from_at'))
  AND (sqlc.narg('to_at')::timestamptz IS NULL OR occurred_at <= sqlc.narg('to_at'))
  AND (sqlc.narg('after_occurred_at')::timestamptz IS NULL
       OR (occurred_at, id) < (sqlc.narg('after_occurred_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: DeleteJournalEntry :exec
-- hard removal for sources with no tombstone shape in the frozen contract
-- (Measurement has no deletedAt: a deleted measurement must not appear in
-- any timeline view; audit_revisions keeps the trail)
DELETE FROM journal_entries
WHERE workspace_id = $1 AND entry_type = $2 AND source_id = $3;
