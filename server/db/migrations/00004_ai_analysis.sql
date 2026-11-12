-- +goose Up
-- AI/analysis schema (T02.ai, reserved number per docs/adr/0002-data.md).
-- Semantics authoritative from docs/plan/03-contracts.md ("AI, identification,
-- and OCR"; analysis_runs + guidance/suggestions rows). The run lifecycle
-- (QUEUED -> RUNNING -> SUCCEEDED/FAILED/CANCELLED/STALE), attempt bounds,
-- generation fencing and semantic-key dedup live here as constraints; the
-- provider validator, budget policy and River jobs are T29.

-- ---------- enums ----------
CREATE TYPE analysis_kind AS ENUM ('TEXT','VISION','IDENTIFICATION','RECEIPT_EXTRACTION','SYNTHESIS');
CREATE TYPE analysis_run_state AS ENUM ('QUEUED','RUNNING','SUCCEEDED','FAILED','CANCELLED','STALE');
CREATE TYPE analysis_failure_code AS ENUM (
  'PROVIDER_UNAVAILABLE','TIMEOUT','PROVIDER_UNAVAILABLE_RETRYABLE',
  'INVALID_OUTPUT','REPAIR_FAILED','BUDGET_EXCEEDED','SUPERSEDED','INTERNAL');
CREATE TYPE suggestion_kind AS ENUM ('HEALTH','CARE','COST','OTHER');
CREATE TYPE suggestion_severity AS ENUM ('LOW','MEDIUM','HIGH');
CREATE TYPE suggestion_status AS ENUM ('PROPOSED','ACCEPTED','DISMISSED','EXPIRED');

-- INSIGHT journal entries are projected from accepted synthesis (T33); the
-- GraphQL union member is wired there too. Same staging pattern as CARE in
-- 00003: the enum value exists before any projection writes it.
ALTER TYPE journal_entry_type ADD VALUE 'INSIGHT';

-- ---------- analysis runs ----------
CREATE TABLE analysis_runs (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind analysis_kind NOT NULL,
  -- Which runtime port served the run (03: TextAnalyzer/VisionAnalyzer/
  -- PlantIdentifier/ReceiptExtractor). Kept separate from kind because one
  -- kind may be served by different capabilities over time.
  capability text NOT NULL CHECK (capability IN ('TEXT_ANALYZER','VISION_ANALYZER','PLANT_IDENTIFIER','RECEIPT_EXTRACTOR')),
  plant_id uuid REFERENCES plants(id) ON DELETE CASCADE,
  receipt_asset_id uuid REFERENCES assets(id) ON DELETE RESTRICT,
  -- VISION comparison pair: exactly two, or none for every other kind.
  photo_id_a uuid REFERENCES assets(id) ON DELETE RESTRICT,
  photo_id_b uuid REFERENCES assets(id) ON DELETE RESTRICT,
  -- Bounded input snapshot (activities, measurements, photos, source
  -- versions, weather snapshots, missing facts). Contents validated by the
  -- engine snapshot builder (T29); the DB pins shape + hash.
  input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot) = 'object'),
  input_hash text NOT NULL CHECK (input_hash ~ '^sha256:[0-9a-f]{64}$'),
  -- Engine-composed dedup key (kind + target refs + input_hash). Duplicate
  -- ACTIVE runs are blocked by the partial unique index below; terminal runs
  -- stay for history.
  semantic_key text NOT NULL CHECK (char_length(semantic_key) BETWEEN 1 AND 512),
  provider text CHECK (provider IS NULL OR char_length(provider) <= 200),
  model text CHECK (model IS NULL OR char_length(model) <= 200),
  prompt_version text CHECK (prompt_version IS NULL OR char_length(prompt_version) <= 100),
  result_schema_version text CHECK (result_schema_version IS NULL OR char_length(result_schema_version) <= 100),
  state analysis_run_state NOT NULL DEFAULT 'QUEUED',
  -- Claim generation: a late provider response commits only while its
  -- claimed generation still matches; target change/cancel bumps it.
  generation integer NOT NULL DEFAULT 0 CHECK (generation >= 0),
  -- 03: retry only timeout/429/5xx up to 3 attempts; the single bounded
  -- schema repair counts inside this bound.
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
  -- Integer micro-USD as bigint (never floats). Reserved BEFORE dispatch and
  -- encumbered until reconciliation even after timeout/cancel; actual NULL
  -- means unknown, never zero.
  cost_reserved_micro_usd bigint CHECK (cost_reserved_micro_usd IS NULL OR cost_reserved_micro_usd >= 0),
  cost_actual_micro_usd bigint CHECK (cost_actual_micro_usd IS NULL OR cost_actual_micro_usd >= 0),
  failure_code analysis_failure_code,
  failure_message text CHECK (failure_message IS NULL OR char_length(failure_message) <= 500),
  -- Validated provider result only; raw provider bodies never land here.
  result jsonb CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
  requested_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  finished_at timestamptz,
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_run_target_by_kind CHECK (
    CASE kind
      WHEN 'RECEIPT_EXTRACTION' THEN
        receipt_asset_id IS NOT NULL AND plant_id IS NULL
        AND photo_id_a IS NULL AND photo_id_b IS NULL
      WHEN 'VISION' THEN
        plant_id IS NOT NULL AND photo_id_a IS NOT NULL AND photo_id_b IS NOT NULL
        AND receipt_asset_id IS NULL
      ELSE
        plant_id IS NOT NULL AND receipt_asset_id IS NULL
        AND photo_id_a IS NULL AND photo_id_b IS NULL
    END
  ),
  CONSTRAINT chk_run_photo_pair_distinct CHECK (photo_id_a IS NULL OR photo_id_a <> photo_id_b),
  CONSTRAINT chk_run_state_timeline CHECK (
    CASE state
      WHEN 'QUEUED'  THEN started_at IS NULL AND finished_at IS NULL
      WHEN 'RUNNING' THEN started_at IS NOT NULL AND finished_at IS NULL
      ELSE finished_at IS NOT NULL
    END
  ),
  CONSTRAINT chk_run_success_result CHECK (state <> 'SUCCEEDED' OR result IS NOT NULL),
  CONSTRAINT chk_run_failure_code CHECK (state <> 'FAILED' OR failure_code IS NOT NULL),
  -- A claimed (started) run always carries the capability that served it;
  -- queued runs pin the intended capability up front for budget gating.
  CONSTRAINT chk_run_cost_shape CHECK (
    cost_actual_micro_usd IS NULL OR state IN ('SUCCEEDED','FAILED','CANCELLED','STALE'))
);
-- 03: "duplicate active run blocked by semantic key" — one live run per
-- semantic key per workspace; history keeps terminal rows.
CREATE UNIQUE INDEX analysis_runs_active_semantic_uk
  ON analysis_runs(workspace_id, semantic_key)
  WHERE state IN ('QUEUED','RUNNING');
CREATE INDEX analysis_runs_workspace_recent_idx ON analysis_runs(workspace_id, requested_at DESC, id DESC);
CREATE INDEX analysis_runs_plant_idx ON analysis_runs(plant_id, requested_at DESC) WHERE plant_id IS NOT NULL;
-- Budget aggregation: month-scoped reservation/actual sums (UTC month).
CREATE INDEX analysis_runs_requested_month_idx ON analysis_runs(requested_at);
CREATE TRIGGER analysis_runs_updated BEFORE UPDATE ON analysis_runs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------- suggestions (advice, separate from logged care) ----------
CREATE TABLE suggestions (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  -- Analysis-produced suggestions carry their run; deterministic guidance-
  -- derived ones have none. Run deletion keeps the advice but unlinks it
  -- (03 redaction: evidence references are redacted, not silently rewritten).
  run_id uuid REFERENCES analysis_runs(id) ON DELETE SET NULL,
  kind suggestion_kind NOT NULL,
  severity suggestion_severity NOT NULL,
  status suggestion_status NOT NULL DEFAULT 'PROPOSED',
  -- Structured advice parts (03): what is visible, what it might mean, what
  -- to do. action is the accept-able core; observation/hypothesis optional.
  observation text CHECK (observation IS NULL OR char_length(observation) <= 2000),
  hypothesis text CHECK (hypothesis IS NULL OR char_length(hypothesis) <= 2000),
  action text NOT NULL CHECK (char_length(action) BETWEEN 1 AND 2000),
  -- Evidence references into run snapshots/entities; the engine validates
  -- every reference before persistence (T33), the DB pins array shape.
  evidence_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_ids) = 'array'),
  missing_facts jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(missing_facts) = 'array'),
  -- True when a referenced source was retired or context changed: still
  -- PROPOSED, but the owner must review before accepting (03 source rules).
  needs_review boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz,
  -- Set exactly when ACCEPTED: the single one-off task the acceptance
  -- created (idempotent replays return the same one).
  accepted_task_id uuid REFERENCES care_occurrences(id) ON DELETE SET NULL,
  dismiss_reason text CHECK (dismiss_reason IS NULL OR char_length(dismiss_reason) <= 500),
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_suggestion_accepted_task CHECK (
    (status = 'ACCEPTED') = (accepted_task_id IS NOT NULL)),
  CONSTRAINT chk_suggestion_expiry_future CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE INDEX suggestions_workspace_recent_idx ON suggestions(workspace_id, created_at DESC, id DESC);
CREATE INDEX suggestions_plant_recent_idx ON suggestions(plant_id, created_at DESC, id DESC);
-- run.suggestions connection (AnalysisRunFields fragment, T33).
CREATE INDEX suggestions_run_idx ON suggestions(run_id) WHERE run_id IS NOT NULL;
-- Expiry sweeper + Today's "unresolved suggestions" strip.
CREATE INDEX suggestions_open_expiry_idx
  ON suggestions(workspace_id, expires_at)
  WHERE status = 'PROPOSED';
CREATE TRIGGER suggestions_updated BEFORE UPDATE ON suggestions
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS suggestions;
DROP TABLE IF EXISTS analysis_runs;
DROP TYPE IF EXISTS suggestion_status;
DROP TYPE IF EXISTS suggestion_severity;
DROP TYPE IF EXISTS suggestion_kind;
DROP TYPE IF EXISTS analysis_failure_code;
DROP TYPE IF EXISTS analysis_run_state;
DROP TYPE IF EXISTS analysis_kind;
