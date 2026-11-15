-- name: ListRainWorkspaces :many
SELECT * FROM workspace WHERE latitude IS NOT NULL AND longitude IS NOT NULL ORDER BY id;

-- name: LockRainWorkspace :one
SELECT * FROM workspace WHERE id=$1 FOR UPDATE;

-- name: ListCurrentRainChecks :many
SELECT r.* FROM rain_reviews r JOIN workspace w ON w.id=r.workspace_id
WHERE r.workspace_id=$1 AND r.latitude=w.latitude AND r.longitude=w.longitude AND r.timezone=w.timezone
AND r.event_date BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
ORDER BY r.event_date DESC;

-- name: UpsertRainCheck :exec
INSERT INTO rain_reviews(id,workspace_id,event_date,latitude,longitude,timezone,estimated_mm,checked_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(workspace_id,event_date,latitude,longitude,timezone)
DO UPDATE SET estimated_mm=EXCLUDED.estimated_mm,checked_at=EXCLUDED.checked_at
WHERE EXCLUDED.checked_at >= rain_reviews.checked_at;

-- name: GetRainReviewForUpdate :one
SELECT r.* FROM rain_reviews r JOIN workspace w ON w.id=r.workspace_id
WHERE r.id=$1 AND r.workspace_id=$2 AND r.latitude=w.latitude AND r.longitude=w.longitude AND r.timezone=w.timezone
FOR UPDATE OF r;

-- name: RespondToRainReview :exec
UPDATE rain_reviews SET response=$3,responded_at=$4,rain_event_id=$5 WHERE id=$1 AND workspace_id=$2;

-- name: ExistingRainOnDay :one
SELECT * FROM rain_events WHERE workspace_id=$1 AND event_date=$2 AND voided_at IS NULL ORDER BY created_at,id LIMIT 1;

-- name: DeleteOldRainChecks :exec
DELETE FROM rain_reviews WHERE event_date < $1;

-- name: ListRecordedRainDays :many
SELECT DISTINCT event_date FROM rain_events
WHERE workspace_id=$1 AND event_date BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date AND voided_at IS NULL;
