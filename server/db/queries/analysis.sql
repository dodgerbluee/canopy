-- T02.ai canonical queries. Run lifecycle primitives (insert/claim/commit/
-- cancel/stale), budget aggregation and suggestion paging/decisions. The
-- provider worker flows (T29) and accept/dismiss use cases (T33) build on
-- exactly these; generation fencing and the semantic-key dedup index are the
-- concurrency contracts, so every state transition is a guarded UPDATE.

-- ---------- runs ----------

-- name: InsertAnalysisRun :one
INSERT INTO analysis_runs (
  id, workspace_id, kind, capability, plant_id, receipt_asset_id,
  photo_id_a, photo_id_b, input_snapshot, input_hash, semantic_key,
  cost_reserved_micro_usd
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: GetAnalysisRun :one
SELECT * FROM analysis_runs
WHERE id = $1 AND workspace_id = $2;

-- name: FindActiveRunBySemanticKey :one
-- Pre-check for the request path; the partial unique index is the authority
-- under races (insert may still 23p03/23505 and must be handled).
SELECT * FROM analysis_runs
WHERE workspace_id = $1 AND semantic_key = $2 AND state IN ('QUEUED','RUNNING');

-- name: ClaimAnalysisRun :one
-- QUEUED -> RUNNING (or retry re-claim of a RUNNING row within attempts):
-- bumps generation so any older claim's late commit is rejected.
UPDATE analysis_runs SET
  state = 'RUNNING',
  attempts = attempts + 1,
  generation = generation + 1,
  started_at = COALESCE(started_at, now()),
  revision = revision + 1,
  updated_at = now()
WHERE id = $1
  AND generation = $2
  AND state IN ('QUEUED','RUNNING')
  AND attempts < 3
RETURNING *;

-- name: CommitAnalysisRun :one
-- RUNNING -> SUCCEEDED, generation-fenced: a superseded claim cannot commit.
UPDATE analysis_runs SET
  state = 'SUCCEEDED',
  result = $3,
  provider = $4, model = $5, prompt_version = $6, result_schema_version = $7,
  cost_actual_micro_usd = $8,
  finished_at = now(),
  revision = revision + 1,
  updated_at = now()
WHERE id = $1 AND generation = $2 AND state = 'RUNNING'
RETURNING *;

-- name: FailAnalysisRun :one
UPDATE analysis_runs SET
  state = 'FAILED',
  failure_code = $3, failure_message = $4,
  provider = $5, model = $6,
  cost_actual_micro_usd = $7,
  finished_at = now(),
  revision = revision + 1,
  updated_at = now()
WHERE id = $1 AND generation = $2 AND state = 'RUNNING'
RETURNING *;

-- name: MarkAnalysisRunStale :one
-- Target changed/deleted or snapshot superseded: terminal STALE regardless of
-- claim generation (the invalidator bumps generation first, then marks).
UPDATE analysis_runs SET
  state = 'STALE',
  generation = generation + 1,
  finished_at = now(),
  revision = revision + 1,
  updated_at = now()
WHERE id = $1 AND state IN ('QUEUED','RUNNING')
RETURNING *;

-- name: CancelAnalysisRun :one
-- Client cancel with optimistic revision; terminal runs reject (0 rows).
-- Reservation stays encumbered: cost columns are not touched here.
UPDATE analysis_runs SET
  state = 'CANCELLED',
  generation = generation + 1,
  finished_at = now(),
  revision = revision + 1,
  updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND revision = $3
  AND state IN ('QUEUED','RUNNING')
RETURNING *;

-- name: SettleAnalysisRunCost :one
-- Post-dispatch reconciliation (charged timeout/cancel): fills actual cost on
-- an already-terminal run without touching state.
UPDATE analysis_runs SET
  cost_actual_micro_usd = $2,
  updated_at = now()
WHERE id = $1 AND state IN ('SUCCEEDED','FAILED','CANCELLED','STALE')
  AND cost_actual_micro_usd IS NULL
RETURNING *;

-- name: CountRunsSince :one
-- Daily budget gate: runs requested in the workspace-local day (the caller
-- converts the local day boundary to timestamptz; the DB stays timezone-free).
SELECT count(*)::int AS run_count FROM analysis_runs
WHERE workspace_id = $1 AND requested_at >= $2;

-- name: BudgetMonthTotals :one
-- UTC-month aggregation for capability/budget status. reserved_total counts
-- every reservation this month (encumbered until reconciled). actual_count
-- distinguishes "nothing reconciled yet" (0 -> report NULL, NEVER 0) from a
-- reconciled total; actual_total is only meaningful when actual_count > 0.
SELECT
  COALESCE(SUM(cost_reserved_micro_usd), 0)::bigint AS reserved_total,
  COALESCE(SUM(cost_actual_micro_usd), 0)::bigint AS actual_total,
  COUNT(cost_actual_micro_usd)::int AS actual_count
FROM analysis_runs
WHERE workspace_id = $1 AND requested_at >= $2 AND requested_at < $3;

-- name: CountConcurrentRunsPerCapability :many
-- One concurrent run per provider (03): live runs grouped by capability.
SELECT capability, count(*)::int AS live_count
FROM analysis_runs
WHERE workspace_id = $1 AND state IN ('QUEUED','RUNNING')
GROUP BY capability;

-- ---------- suggestions ----------

-- name: InsertSuggestion :one
-- evidence/missing arrays arrive as NULL from Go callers that omit them;
-- COALESCE keeps the NOT NULL invariant without forcing every caller to
-- spell '[]'.
INSERT INTO suggestions (
  id, workspace_id, plant_id, run_id, kind, severity,
  observation, hypothesis, action, evidence_ids, missing_facts, expires_at
) VALUES (
  sqlc.arg('id'), sqlc.arg('workspace_id'), sqlc.arg('plant_id'),
  sqlc.narg('run_id'), sqlc.arg('kind'), sqlc.arg('severity'),
  sqlc.narg('observation'), sqlc.narg('hypothesis'), sqlc.arg('action'),
  COALESCE(sqlc.narg('evidence_ids')::jsonb, '[]'::jsonb),
  COALESCE(sqlc.narg('missing_facts')::jsonb, '[]'::jsonb),
  sqlc.narg('expires_at')
)
RETURNING *;

-- name: GetSuggestion :one
SELECT * FROM suggestions WHERE id = $1 AND workspace_id = $2;

-- name: ListSuggestionsByRun :many
SELECT * FROM suggestions WHERE run_id = $1 ORDER BY created_at, id;

-- name: ListSuggestionsPage :many
-- Keyset page over (created_at DESC, id DESC), bound to the filter signature
-- by the caller; filters mirror the frozen SuggestionFilter input.
SELECT * FROM suggestions
WHERE suggestions.workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('plant_id')::uuid IS NULL OR plant_id = sqlc.narg('plant_id'))
  AND (sqlc.narg('statuses')::text[] IS NULL OR status::text = ANY(sqlc.narg('statuses')::text[]))
  AND (sqlc.narg('kinds')::text[] IS NULL OR kind::text = ANY(sqlc.narg('kinds')::text[]))
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: AcceptSuggestion :one
-- PROPOSED -> ACCEPTED creating exactly one task; the exact-revision guard is
-- the "references the exact suggestion version" rule. needs_review must be
-- cleared by an explicit owner review touch before accepting (T33).
UPDATE suggestions SET
  status = 'ACCEPTED',
  accepted_task_id = $3,
  revision = revision + 1,
  updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND revision = $4
  AND status = 'PROPOSED' AND needs_review = false
RETURNING *;

-- name: DismissSuggestion :one
UPDATE suggestions SET
  status = 'DISMISSED',
  dismiss_reason = $3,
  revision = revision + 1,
  updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND revision = $4
  AND status = 'PROPOSED'
RETURNING *;

-- name: ExpireDueSuggestions :many
-- Sweeper: PROPOSED past expiry become EXPIRED (cannot be accepted after).
UPDATE suggestions SET
  status = 'EXPIRED',
  revision = revision + 1,
  updated_at = now()
WHERE status = 'PROPOSED' AND expires_at IS NOT NULL AND expires_at <= now()
RETURNING id;

-- name: ReviewSuggestion :one
-- Explicit owner review touch clears needs_review (the only path back to
-- acceptable); revision bump keeps every other writer honest.
UPDATE suggestions SET
  needs_review = false,
  revision = revision + 1,
  updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND revision = $3
  AND status = 'PROPOSED'
RETURNING *;

-- name: FlagSuggestionsNeedingReview :many
-- Source retired or plant context changed: pending advice must be re-reviewed
-- by the owner before acceptance (03 source-change rule). Bumps revision so
-- in-flight accepts fail with CONFLICT, not silent staleness. Already-flagged
-- rows are left alone: repeated context changes must not inflate the revision
-- or force repeat confirmations on advice the owner has not re-read yet.
UPDATE suggestions SET
  needs_review = true,
  revision = revision + 1,
  updated_at = now()
WHERE workspace_id = $1
  AND status = 'PROPOSED'
  AND needs_review = false
  AND (run_id = ANY(sqlc.arg('run_ids')::uuid[]) OR plant_id = ANY(sqlc.arg('plant_ids')::uuid[]))
RETURNING id;
