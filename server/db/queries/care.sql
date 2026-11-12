-- T02.care canonical queries. Source/profile CRUD arrives with T21; the
-- engine-side materialization and resolution flows land with T22/T23 and will
-- extend this file. Kept minimal on purpose: cursor state, agenda paging and
-- plan/occurrence reads are the shared primitives.

-- name: UpsertCareRuleCursor :one
INSERT INTO care_rule_cursors (plan_id, workspace_id, rule_key, last_resolved_ordinal, decision, anchor_date, next_due_date, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (plan_id, rule_key) DO UPDATE SET
  last_resolved_ordinal = EXCLUDED.last_resolved_ordinal,
  decision = EXCLUDED.decision,
  anchor_date = EXCLUDED.anchor_date,
  next_due_date = EXCLUDED.next_due_date,
  updated_at = now()
RETURNING *;

-- name: ListCareRuleCursors :many
SELECT * FROM care_rule_cursors
WHERE plan_id = $1
ORDER BY rule_key;

-- name: GetCarePlan :one
SELECT * FROM care_plans WHERE id = $1;

-- name: GetCarePlanByPlant :one
SELECT * FROM care_plans WHERE plant_id = $1;

-- name: ListCareAgenda :many
-- Keyset page over the partial DUE index; from/to are inclusive local-date
-- bounds; plantId narrows to one plant (T24 agenda query).
SELECT * FROM care_occurrences
WHERE care_occurrences.workspace_id = sqlc.arg('workspace_id')
  AND status = 'DUE'
  AND (sqlc.narg('from_due_date')::date IS NULL OR current_due_date >= sqlc.narg('from_due_date')::date)
  AND (sqlc.narg('to_due_date')::date IS NULL OR current_due_date <= sqlc.narg('to_due_date')::date)
  AND (sqlc.narg('plant_id')::uuid IS NULL
       OR care_occurrences.plan_id IN (SELECT id FROM care_plans WHERE plant_id = sqlc.narg('plant_id')))
  AND (sqlc.narg('after_due_date')::date IS NULL
       OR (current_due_date, id) > (sqlc.narg('after_due_date')::date, sqlc.narg('after_id')::uuid))
ORDER BY current_due_date, id
LIMIT sqlc.arg('limit_count');

-- name: ListPlanOccurrences :many
SELECT * FROM care_occurrences
WHERE plan_id = sqlc.arg('plan_id')
  AND (sqlc.narg('statuses')::text[] IS NULL OR status::text = ANY(sqlc.narg('statuses')::text[]))
  AND (sqlc.narg('after_due_date')::date IS NULL
       OR (current_due_date, id) > (sqlc.narg('after_due_date')::date, sqlc.narg('after_id')::uuid))
ORDER BY current_due_date, id
LIMIT sqlc.arg('limit_count');

-- name: GetCareOccurrenceByKey :one
SELECT * FROM care_occurrences
WHERE plan_id = $1 AND occurrence_key = $2;

-- name: GetCareOccurrence :one
SELECT * FROM care_occurrences WHERE id = $1;

-- ---------- sources CRUD (T21) ----------

-- name: InsertSource :one
INSERT INTO sources (id, workspace_id, kind, title, publisher, url, region, license)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSource :one
SELECT * FROM sources WHERE id = $1 AND workspace_id = $2;

-- name: UpdateSourceAggregate :one
-- COALESCE semantics: null keeps the existing aggregate value.
UPDATE sources SET
  title = COALESCE(sqlc.narg('title'), title),
  publisher = COALESCE(sqlc.narg('publisher'), publisher),
  url = COALESCE(sqlc.narg('url'), url),
  region = COALESCE(sqlc.narg('region'), region),
  license = COALESCE(sqlc.narg('license'), license),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: RetireSourceRow :one
UPDATE sources SET retired_at = now(), revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $3 AND retired_at IS NULL
RETURNING *;

-- name: ListSources :many
SELECT * FROM sources
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('kind')::text IS NULL OR kind::text = sqlc.narg('kind')::text)
  AND (COALESCE(sqlc.narg('include_retired'), false) OR retired_at IS NULL)
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: InsertSourceVersion :one
INSERT INTO source_versions (id, source_id, workspace_id, version, citation, summary, retrieved_on, content_sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListSourceVersions :many
SELECT * FROM source_versions WHERE source_id = $1 ORDER BY version DESC;

-- name: GetSourceVersionForCitation :one
-- Citations must resolve inside the workspace and against a non-retired source.
SELECT sv.* FROM source_versions sv
JOIN sources s ON s.id = sv.source_id
WHERE sv.id = $1 AND sv.workspace_id = $2 AND s.retired_at IS NULL;

-- name: MaxSourceVersionNumber :one
SELECT COALESCE(MAX(version), 0)::int AS max_version FROM source_versions WHERE source_id = $1;

-- ---------- profile CRUD (T21) ----------

-- name: InsertCareProfile :one
INSERT INTO care_profiles (id, workspace_id, category, species, cultivar, status)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCareProfile :one
SELECT * FROM care_profiles WHERE id = $1 AND workspace_id = $2;

-- name: UpdateCareProfileAggregate :one
UPDATE care_profiles SET
  species = COALESCE(sqlc.narg('species'), species),
  cultivar = COALESCE(sqlc.narg('cultivar'), cultivar),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: BumpCareProfileRevision :one
UPDATE care_profiles SET revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $3
RETURNING *;

-- name: MarkCareProfilePublished :one
UPDATE care_profiles SET status = 'REVIEWED', revision = revision + 1
WHERE id = $1 AND workspace_id = $2 AND revision = $3
RETURNING *;

-- name: DeleteCareProfileRow :exec
DELETE FROM care_profiles WHERE id = $1 AND workspace_id = $2;

-- name: ListCareProfiles :many
SELECT * FROM care_profiles
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('category')::text IS NULL OR category::text = sqlc.narg('category')::text)
  AND (sqlc.narg('query')::text IS NULL
       OR species ILIKE '%' || sqlc.narg('query')::text || '%'
       OR cultivar ILIKE '%' || sqlc.narg('query')::text || '%')
  AND (COALESCE(sqlc.narg('include_drafts'), false) OR status <> 'DRAFT')
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: InsertCareProfileVersion :one
INSERT INTO care_profile_versions (id, profile_id, workspace_id, version, status, environment)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCareProfileVersion :one
SELECT * FROM care_profile_versions WHERE id = $1 AND workspace_id = $2;

-- name: ListCareProfileVersions :many
SELECT * FROM care_profile_versions WHERE profile_id = $1 ORDER BY version DESC;

-- name: MaxProfileVersionNumber :one
SELECT COALESCE(MAX(version), 0)::int AS max_version FROM care_profile_versions WHERE profile_id = $1;

-- name: PublishCareProfileVersion :one
-- The DB trigger requires reviewer fields in the same UPDATE.
UPDATE care_profile_versions SET
  status = 'REVIEWED', reviewer_label = $2, reviewed_on = $3, published_at = now()
WHERE id = $1 AND status = 'DRAFT'
RETURNING *;

-- name: RetireCareProfileVersionRow :one
UPDATE care_profile_versions SET status = 'RETIRED', retired_at = now()
WHERE id = $1 AND status = 'REVIEWED'
RETURNING *;

-- name: DeleteCareProfileVersionRow :exec
DELETE FROM care_profile_versions WHERE id = $1 AND status = 'DRAFT';

-- name: UpdateCareProfileDraftEnvironment :one
-- DRAFT rows only (the version update guard rejects non-DRAFT edits).
UPDATE care_profile_versions SET environment = $2
WHERE id = $1 AND status = 'DRAFT'
RETURNING *;

-- name: MarkCareProfileDraft :one
-- Aggregate returns to DRAFT when a published profile forks a new draft
-- version; the revision bump already rode the aggregate update.
UPDATE care_profiles SET status = 'DRAFT'
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: CountPlansPinningVersion :one
SELECT count(*)::int AS pinned_count FROM care_plans WHERE profile_version_id = $1;

-- name: InsertCareRule :one
INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, activity_kind, recurrence, required_facts, weather_sensitivity, guidance_text)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: DeleteCareRulesByVersion :exec
DELETE FROM care_rules WHERE profile_version_id = $1;

-- name: ListCareRules :many
SELECT * FROM care_rules WHERE profile_version_id = $1 ORDER BY rule_key;

-- name: InsertCareRuleSource :exec
INSERT INTO care_rule_sources (rule_id, source_version_id) VALUES ($1, $2);

-- name: ListRuleSourcesForVersion :many
SELECT rs.rule_id, sv.*
FROM care_rule_sources rs
JOIN source_versions sv ON sv.id = rs.source_version_id
WHERE rs.rule_id IN (SELECT id FROM care_rules WHERE profile_version_id = $1);


-- ---------- plans + overrides (T23) ----------

-- name: InsertCarePlan :one
INSERT INTO care_plans (id, workspace_id, plant_id, profile_version_id, timezone, enabled, notification_time)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetCarePlanForUpdate :one
-- Serializes materialization and resolution transactions per plan.
SELECT * FROM care_plans WHERE id = $1 FOR UPDATE;

-- name: UpdateCarePlanAggregate :one
-- COALESCE semantics: null keeps the existing value.
UPDATE care_plans SET
  profile_version_id = COALESCE(sqlc.narg('profile_version_id'), profile_version_id),
  timezone = COALESCE(sqlc.narg('timezone'), timezone),
  enabled = COALESCE(sqlc.narg('enabled'), enabled),
  notification_time = COALESCE(sqlc.narg('notification_time'), notification_time),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id') AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: SetPlanWatermark :exec
UPDATE care_plans SET last_generated_through = sqlc.arg('through_date') WHERE id = sqlc.arg('id');

-- name: ListEnabledCarePlans :many
-- Worker tick scan; deliberately not workspace-scoped (the worker is a
-- system process; each plan row carries its workspace). Archived/dead
-- plants never materialize new work (T24 lifecycle integration).
SELECT cp.* FROM care_plans cp
JOIN plants p ON p.id = cp.plant_id
WHERE cp.enabled AND p.status = 'ACTIVE'
ORDER BY cp.id;

-- name: DeleteCarePlanOverrides :exec
-- Replace-all semantics on plan update.
DELETE FROM care_plan_overrides WHERE plan_id = $1;

-- name: InsertCarePlanOverride :exec
INSERT INTO care_plan_overrides (id, plan_id, workspace_id, rule_key, enabled, recurrence_override)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListCarePlanOverrides :many
SELECT * FROM care_plan_overrides WHERE plan_id = $1 ORDER BY rule_key;

-- ---------- occurrence materialization (T23) ----------

-- name: InsertCareOccurrenceUpsert :one
-- Duplicate ticks are harmless: the logical key is the uniqueness anchor.
-- A CANCELLED slot that the current schedule still generates is revived
-- in place (plan-edit regeneration); resolved history is never touched.
-- weather_snapshot_id records the EXACT snapshot the advice was computed
-- from (T26); NULL means no snapshot backed this slot.
INSERT INTO care_occurrences (
  id, workspace_id, plan_id, rule_key, kind, activity_kind, occurrence_key,
  original_due_date, current_due_date, status, plan_timezone,
  ordinal, reason_text, missing_facts, evidence_notes, window_end,
  weather_snapshot_id)
VALUES (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, 'DUE', $10,
  $11, $12, $13, $14, $15, $16)
ON CONFLICT (plan_id, occurrence_key) DO UPDATE SET
  status = 'DUE',
  resolved_at = NULL,
  completed_activity_id = NULL,
  snoozed_until = NULL,
  original_due_date = EXCLUDED.original_due_date,
  current_due_date = EXCLUDED.current_due_date,
  plan_timezone = EXCLUDED.plan_timezone,
  ordinal = EXCLUDED.ordinal,
  reason_text = EXCLUDED.reason_text,
  missing_facts = EXCLUDED.missing_facts,
  evidence_notes = EXCLUDED.evidence_notes,
  window_end = EXCLUDED.window_end,
  weather_snapshot_id = EXCLUDED.weather_snapshot_id,
  revision = care_occurrences.revision + 1
WHERE care_occurrences.status = 'CANCELLED'
RETURNING *;

-- name: CancelStaleBacklogKeepEarliest :many
-- Outage collapse (03): repeating missed checks reduce to ONE overdue
-- actionable per rule; the earliest open slot stays actionable. One-time
-- occurrences are excluded (fixed events stay distinct).
UPDATE care_occurrences SET status = 'CANCELLED', resolved_at = now(), revision = revision + 1
WHERE care_occurrences.plan_id = sqlc.arg('plan_id') AND care_occurrences.rule_key = sqlc.arg('rule_key')
  AND care_occurrences.status = 'DUE' AND care_occurrences.current_due_date < sqlc.arg('today_date')
  AND care_occurrences.id <> (
    SELECT k.id FROM care_occurrences k
    WHERE k.plan_id = sqlc.arg('plan_id') AND k.rule_key = sqlc.arg('rule_key')
      AND k.status = 'DUE' AND k.current_due_date < sqlc.arg('today_date')
    ORDER BY k.current_due_date, k.id
    LIMIT 1
  )
RETURNING *;

-- name: CancelOpenOccurrencesAfter :many
-- Regeneration on plan edit / cursor move: cancel open occurrences whose
-- logical (original) due date is strictly after the cutoff. History and
-- occurrences at or before the cutoff are preserved.
UPDATE care_occurrences SET status = 'CANCELLED', resolved_at = now(), revision = revision + 1
WHERE plan_id = sqlc.arg('plan_id') AND rule_key = sqlc.arg('rule_key')
  AND status = 'DUE' AND original_due_date > sqlc.arg('cutoff_date')
RETURNING *;

-- name: CancelAllOpenOccurrences :many
-- Full-plan regeneration on save: every open occurrence goes, then the
-- materializer rebuilds the future transactionally.
UPDATE care_occurrences SET status = 'CANCELLED', resolved_at = now(), revision = revision + 1
WHERE plan_id = $1 AND status = 'DUE'
RETURNING *;

-- ---------- resolution flows (T23) ----------

-- name: LockCareOccurrence :one
SELECT * FROM care_occurrences WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: ResolveOccurrence :one
-- complete/skip: DUE -> terminal decision, revision-guarded.
UPDATE care_occurrences SET
  status = sqlc.arg('status'),
  resolved_at = now(),
  completed_activity_id = sqlc.narg('completed_activity_id'),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'DUE' AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: SnoozeOccurrenceRow :one
-- Snooze is a field, not a new occurrence: effective due moves, the
-- logical key and cadence cursor never change.
UPDATE care_occurrences SET
  current_due_date = sqlc.arg('until_date'),
  snoozed_until = sqlc.arg('until_date'),
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'DUE' AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: ReopenOccurrenceRow :one
-- Undo of the latest completion with no later decision: back to one
-- overdue actionable check.
UPDATE care_occurrences SET
  status = 'DUE', resolved_at = NULL, completed_activity_id = NULL,
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'COMPLETED' AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: RetractOccurrenceRow :one
-- Undo when a LATER decision exists: newer history is authoritative; the
-- older occurrence becomes COMPLETION_RETRACTED and its completion link
-- clears (the activity itself is voided separately).
UPDATE care_occurrences SET
  status = 'COMPLETION_RETRACTED', completed_activity_id = NULL,
  revision = revision + 1
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'COMPLETED' AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: ListResolvedOccurrencesByRule :many
-- Cursor replay source for undo: resolved decisions in logical order.
SELECT * FROM care_occurrences
WHERE plan_id = sqlc.arg('plan_id') AND rule_key = sqlc.arg('rule_key')
  AND status IN ('COMPLETED', 'SKIPPED')
ORDER BY ordinal;

-- name: GetCareRuleCursor :one
SELECT * FROM care_rule_cursors WHERE plan_id = $1 AND rule_key = $2;

-- name: GetLinkedCompletionOccurrence :one
-- Journal guard: a linked completion is corrected through the care flow
-- only (LINKED_COMPLETION_USE_CORRECTION).
SELECT * FROM care_occurrences
WHERE completed_activity_id = $1 AND status = 'COMPLETED';

-- name: LatestWeatherSnapshot :one
-- Most recent snapshot for the plant's current location (advisory
-- evidence; freshness bands are applied by the engine, not here).
SELECT * FROM weather_snapshots
WHERE workspace_id = $1 AND location_id = $2
ORDER BY fetched_at DESC
LIMIT 1;

-- name: InsertWeatherSnapshot :one
-- One provider fetch becomes one immutable snapshot row (T26). The
-- worker only ever calls this for a location the owner entered
-- coordinates for — locations are never geolocated.
INSERT INTO weather_snapshots (
  id, workspace_id, location_id, provider, fetched_at, valid_from, valid_until,
  high_temp_c, low_temp_c, rainfall_mm, et0_mm, license, expires_at)
VALUES (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: ListLocationsWithCoordinates :many
-- Weather opt-in surface: only locations whose owner entered coordinates
-- are ever fetched (03: "Approximate, private, user-entered"). Stable
-- order keeps worker runs deterministic for tests.
SELECT id, workspace_id, latitude, longitude FROM locations
WHERE latitude IS NOT NULL AND longitude IS NOT NULL
ORDER BY workspace_id, id;

-- name: ListWeatherSnapshotsByIDs :many
-- Batch hydration for agenda evidence: every occurrence references the
-- exact snapshot id it was computed from.
SELECT * FROM weather_snapshots
WHERE workspace_id = sqlc.arg('workspace_id') AND id = ANY(sqlc.arg('ids')::uuid[]);

-- name: DeleteExpiredWeatherSnapshots :exec
-- Retention sweep (worker run): rows past expires_at are removed;
-- occurrences keep their typed reason text while the evidence reference
-- SET NULLs (00006).
DELETE FROM weather_snapshots WHERE expires_at < $1;

-- ---------- T24: agenda reads, notifications, lifecycle hooks ----------

-- name: ListOccurrencesByIDs :many
-- Journal union hydration: CARE entries carry their occurrence payload.
SELECT * FROM care_occurrences
WHERE workspace_id = $1 AND id = ANY(sqlc.arg('ids')::uuid[]);

-- name: NextOpenOccurrenceForRule :one
-- Earliest still-open slot for a plan rule (payload nextOccurrence).
SELECT * FROM care_occurrences
WHERE plan_id = $1 AND rule_key = $2 AND status = 'DUE'
ORDER BY current_due_date, id
LIMIT 1;

-- name: CancelOpenOccurrencesForPlant :many
-- Plant archive: every open slot for the plant's plan goes away; the
-- plan itself stays (restore regenerates).
UPDATE care_occurrences SET status = 'CANCELLED', resolved_at = now(),
  revision = revision + 1, updated_at = now()
WHERE care_occurrences.workspace_id = sqlc.arg('workspace_id')
  AND status = 'DUE'
  AND care_occurrences.plan_id IN (SELECT id FROM care_plans WHERE plant_id = sqlc.arg('plant_id'))
RETURNING id;

-- name: CancelFutureOpenOccurrencesForPlant :many
-- Context edit: only slots due on/after the change date are invalidated;
-- past-due advice stays actionable until regeneration replaces it.
UPDATE care_occurrences SET status = 'CANCELLED', resolved_at = now(),
  revision = revision + 1, updated_at = now()
WHERE care_occurrences.workspace_id = sqlc.arg('workspace_id')
  AND status = 'DUE'
  AND current_due_date >= sqlc.arg('from_date')::date
  AND care_occurrences.plan_id IN (SELECT id FROM care_plans WHERE plant_id = sqlc.arg('plant_id'))
RETURNING id;

-- name: InsertNotification :exec
-- Tick-time notification intents; the dedup key makes replays idempotent.
INSERT INTO notifications (id, workspace_id, owner_id, channel, dedup_key, ref_kind, ref_id, title, body)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (workspace_id, dedup_key) DO NOTHING;

-- name: ListNotifications :many
SELECT * FROM notifications
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (COALESCE(sqlc.narg('unread_only'), false) OR true)
  AND (NOT COALESCE(sqlc.narg('unread_only'), false) OR read_at IS NULL)
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: GetNotification :one
SELECT * FROM notifications WHERE id = $1 AND workspace_id = $2;

-- name: MarkNotificationReadRow :one
UPDATE notifications SET read_at = now()
WHERE id = $1 AND workspace_id = $2 AND read_at IS NULL
RETURNING *;

-- name: ListDueTodayOccurrences :many
-- Notification intents for the tick: slots due exactly on the plan-local
-- today, oldest first.
SELECT * FROM care_occurrences
WHERE plan_id = $1 AND status = 'DUE' AND current_due_date = $2
ORDER BY current_due_date, id;

-- ---------- T27: daily digest intents + email delivery ----------

-- name: ListDigestInputs :many
-- Digest composition surface: every open occurrence due (or overdue) on
-- the plan-local today, joined to plan/workspace/plant for one digest
-- per owner per workspace-local date. Snoozed past today stays out.
SELECT o.id AS occurrence_id, o.workspace_id, w.owner_id,
       w.timezone AS ws_timezone, p.timezone AS plan_timezone,
       p.notification_time, pl.name AS plant_name,
       o.kind, o.activity_kind, o.current_due_date
FROM care_occurrences o
JOIN care_plans p ON p.id = o.plan_id AND p.enabled
JOIN workspace w ON w.id = o.workspace_id
JOIN plants pl ON pl.id = p.plant_id
WHERE o.status = 'DUE'
  AND o.current_due_date <= (now() AT TIME ZONE p.timezone)::date
  AND (o.snoozed_until IS NULL
       OR (o.snoozed_until AT TIME ZONE p.timezone)::date
          <= (now() AT TIME ZONE p.timezone)::date)
ORDER BY o.workspace_id, w.owner_id, o.current_due_date, o.id;

-- name: UpsertDigestIntent :exec
-- One digest per owner per local date (dedup key); the row refreshes in
-- place until delivered so late-appearing tasks ride the same digest —
-- never a second digest for the same day.
INSERT INTO notifications (id, workspace_id, owner_id, channel, dedup_key,
  ref_kind, ref_id, title, body, digest_date, scheduled_at, message_id)
VALUES ($1, $2, $3, 'EMAIL', $4, 'OCCURRENCE', $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, dedup_key) DO UPDATE SET
  title = EXCLUDED.title,
  body = EXCLUDED.body,
  ref_id = EXCLUDED.ref_id,
  scheduled_at = EXCLUDED.scheduled_at,
  message_id = EXCLUDED.message_id;

-- name: ListPendingEmailNotifications :many
-- Delivery sweep: undelivered email intents whose local-time gate has
-- opened, with fewer attempts than the bounded-retry cap. A SENT
-- attempt excludes an intent forever (at-least-once, never at-most).
SELECT n.id, n.workspace_id, n.dedup_key, n.title, n.body, n.message_id,
       (SELECT count(*) FROM delivery_attempts a
        WHERE a.notification_id = n.id) AS attempts
FROM notifications n
WHERE n.channel = 'EMAIL' AND n.delivered_at IS NULL
  AND n.scheduled_at <= sqlc.arg('now_gate')
  AND NOT EXISTS (SELECT 1 FROM delivery_attempts a
                  WHERE a.notification_id = n.id AND a.state = 'SENT')
  AND (SELECT count(*) FROM delivery_attempts a
       WHERE a.notification_id = n.id)::bigint < sqlc.arg('max_attempts')::int
ORDER BY n.scheduled_at, n.id
LIMIT sqlc.arg('limit_count');

-- name: InsertDeliveryAttempt :exec
-- Append-only attempt log: every send (success or failure) is recorded.
INSERT INTO delivery_attempts (id, notification_id, workspace_id, state,
  provider_message_id, error_code)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: AckEmailNotification :one
-- First successful send acks the intent; concurrent acks lose cleanly
-- (no rows) rather than double-marking.
UPDATE notifications SET delivered_at = $2, message_id = $3
WHERE id = $1 AND delivered_at IS NULL
RETURNING id;

-- name: LatestWorkspaceWeather :one
-- Dashboard conditions (T28): the most recently fetched snapshot across
-- every location in the workspace, with the location's display name.
SELECT s.*, l.name AS location_name
FROM weather_snapshots s
JOIN locations l ON l.id = s.location_id
WHERE s.workspace_id = $1
ORDER BY s.fetched_at DESC
LIMIT 1;
