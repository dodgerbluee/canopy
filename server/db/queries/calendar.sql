-- name: InsertRain :one
INSERT INTO rain_events(id,workspace_id,event_date,amount_mm,note)
VALUES(sqlc.arg(id),sqlc.arg(workspace_id),sqlc.arg(event_date),sqlc.narg(amount_mm),sqlc.narg(note)) RETURNING *;

-- name: GetRainForUpdate :one
SELECT * FROM rain_events WHERE id=sqlc.arg(id) AND workspace_id=sqlc.arg(workspace_id) FOR UPDATE;

-- name: VoidRain :one
UPDATE rain_events SET voided_at=now(), updated_at=now(), revision=revision+1
WHERE id=sqlc.arg(id) AND workspace_id=sqlc.arg(workspace_id) AND revision=sqlc.arg(expected_revision) AND voided_at IS NULL RETURNING *;

-- name: ExportRain :many
SELECT to_jsonb(t) AS row FROM rain_events t WHERE t.workspace_id=$1 ORDER BY t.id;

-- name: ListCalendarEvents :many
WITH events AS (
 SELECT j.id::text AS event_key, j.source_id AS entity_id, j.plant_id,
   (CASE WHEN j.entry_type='PHOTO' THEN p.taken_at ELSE j.occurred_at END AT TIME ZONE sqlc.arg(timezone)::text)::date AS day,
   j.entry_type::text AS kind, COALESCE(a.kind::text, '')::text AS activity_kind, j.summary AS title,
   NULL::numeric AS amount_mm, NULL::text AS note, NULL::integer AS revision
 FROM journal_entries j
 LEFT JOIN plant_photos p ON j.entry_type='PHOTO' AND p.id=j.source_id AND p.workspace_id=j.workspace_id
 LEFT JOIN activities a ON j.entry_type='ACTIVITY' AND a.id=j.source_id AND a.workspace_id=j.workspace_id
 WHERE j.workspace_id=sqlc.arg(workspace_id) AND j.tombstoned_at IS NULL
 AND (j.entry_type<>'PHOTO' OR (p.id IS NOT NULL AND p.deleted_at IS NULL AND p.taken_at IS NOT NULL AND p.date_source <> 'UPLOAD_FALLBACK'))
 AND (j.entry_type<>'ACTIVITY' OR (a.id IS NOT NULL AND a.voided_at IS NULL))
 UNION ALL
 SELECT 'rain:'||r.id::text, r.id, NULL::uuid, r.event_date, 'RAIN', ''::text, 'Rain recorded', r.amount_mm, r.note, r.revision
 FROM rain_events r WHERE r.workspace_id=sqlc.arg(workspace_id) AND r.voided_at IS NULL
)
SELECT event_key, entity_id, plant_id, day, kind, activity_kind, title, amount_mm, note, revision
FROM events
WHERE day BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
AND (sqlc.narg(after_key)::text IS NULL OR (day::text||':'||event_key) > sqlc.narg(after_key)::text)
ORDER BY day, event_key LIMIT sqlc.arg(limit_count);
