-- +goose Up
-- Care domain schema (T02.care). Semantics authoritative from
-- docs/plan/03-contracts.md ("Care scheduling", "Correcting a completed
-- occurrence", recurrence/cursor rules). Recurrence JSON shape is documented
-- in docs/adr/0002-data.md; the DB enforces form + numeric ranges, the care
-- engine (T22) validates enum membership and calendar semantics.

-- ---------- enums ----------
CREATE TYPE source_kind AS ENUM ('OWNER_NOTE','BOOK','ARTICLE','EXTENSION_PUBLICATION','OTHER');
CREATE TYPE profile_status AS ENUM ('DRAFT','REVIEWED','RETIRED');
CREATE TYPE rule_kind AS ENUM ('CHECK','ACTION');
CREATE TYPE anchor_policy AS ENUM ('PLAN_START','LAST_QUALIFYING_COMPLETION');
CREATE TYPE weather_sensitivity AS ENUM ('NONE','RAINFALL','FROST');
CREATE TYPE occurrence_status AS ENUM ('DUE','COMPLETED','SKIPPED','CANCELLED','COMPLETION_RETRACTED');
CREATE TYPE notification_channel AS ENUM ('INAPP','EMAIL');
CREATE TYPE notification_ref_kind AS ENUM ('OCCURRENCE','SUGGESTION');
CREATE TYPE delivery_state AS ENUM ('PENDING','SENDING','SENT','FAILED');

-- CARE journal entries are projected from care completions (T23); the GraphQL
-- union member is wired at T24. PG12+ allows ADD VALUE in-transaction; the
-- value is not referenced inside this migration.
ALTER TYPE journal_entry_type ADD VALUE 'CARE';

-- ---------- recurrence validation ----------
-- Exactly one form per object, discriminated by "form". Weekday membership
-- beyond stringness is engine-validated (T22).
-- +goose StatementBegin
CREATE FUNCTION valid_recurrence(r jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
  -- COALESCE is load-bearing: a NULL branch result (e.g. missing anchor) must
  -- be false, not NULL, because a CHECK constraint passes on NULL.
  SELECT COALESCE(CASE r ->> 'form'
    WHEN 'EVERY_N_DAYS' THEN
      jsonb_typeof(r -> 'days') = 'number'
      AND (r ->> 'days')::int BETWEEN 1 AND 365
      AND r ->> 'anchor' IN ('PLAN_START','LAST_QUALIFYING_COMPLETION')
    WHEN 'WEEKLY_DAYS' THEN
      jsonb_typeof(r -> 'weekdays') = 'array'
      AND jsonb_array_length(r -> 'weekdays') BETWEEN 1 AND 7
      AND jsonb_typeof(r -> 'weekdays' -> 0) = 'string'
    WHEN 'ANNUAL_WINDOW' THEN
      jsonb_typeof(r -> 'month') = 'number'
      AND (r ->> 'month')::int BETWEEN 1 AND 12
      AND jsonb_typeof(r -> 'day') = 'number'
      AND (r ->> 'day')::int BETWEEN 1 AND 31
      AND (r -> 'window_days') IS NULL
          OR (jsonb_typeof(r -> 'window_days') = 'number' AND (r ->> 'window_days')::int BETWEEN 1 AND 366)
      AND (r -> 'every_years') IS NULL
          OR (jsonb_typeof(r -> 'every_years') = 'number' AND (r ->> 'every_years')::int BETWEEN 1 AND 50)
    WHEN 'ONE_TIME' THEN
      jsonb_typeof(r -> 'date') = 'string'
      AND (r ->> 'date') ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
    ELSE false
  END, false)
$$;
-- +goose StatementEnd

-- ---------- sources ----------
CREATE TABLE sources (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind source_kind NOT NULL,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 500),
  publisher text CHECK (publisher IS NULL OR char_length(publisher) <= 300),
  url text CHECK (url IS NULL OR char_length(url) <= 2000),
  region text CHECK (region IS NULL OR char_length(region) <= 200),
  -- reuse policy note; no inferred permission to mirror whole sites
  license text CHECK (license IS NULL OR char_length(license) <= 500),
  retired_at timestamptz,
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sources_workspace_idx ON sources(workspace_id);
CREATE TRIGGER sources_updated BEFORE UPDATE ON sources
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Immutable versions: appends only; any UPDATE is rejected by trigger so
-- pinned citations can never mutate under readers.
CREATE TABLE source_versions (
  id uuid PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  version integer NOT NULL CHECK (version > 0),
  citation text NOT NULL CHECK (char_length(citation) BETWEEN 1 AND 1000),
  -- owner-approved summary or small excerpt; never a full mirror
  summary text NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 8000),
  retrieved_on date,
  content_sha256 text CHECK (content_sha256 IS NULL OR content_sha256 ~ '^[0-9a-f]{64}$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source_id, version)
);
CREATE INDEX source_versions_workspace_idx ON source_versions(workspace_id);

-- +goose StatementBegin
CREATE FUNCTION forbid_row_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'immutable table: %', TG_TABLE_NAME;
END $$;
-- +goose StatementEnd
CREATE TRIGGER source_versions_immutable BEFORE UPDATE ON source_versions
  FOR EACH ROW EXECUTE FUNCTION forbid_row_update();

-- ---------- reviewed knowledge profiles ----------
CREATE TABLE care_profiles (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  category plant_category NOT NULL,
  species text CHECK (species IS NULL OR char_length(species) <= 300),
  cultivar text CHECK (cultivar IS NULL OR char_length(cultivar) <= 300),
  status profile_status NOT NULL DEFAULT 'DRAFT',
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX care_profiles_workspace_idx ON care_profiles(workspace_id);
CREATE TRIGGER care_profiles_updated BEFORE UPDATE ON care_profiles
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Version lifecycle enforced by trigger:
--   DRAFT    -> editable content (identity columns stay fixed)
--   DRAFT    -> REVIEWED (publish: reviewer fields set in the same UPDATE)
--   REVIEWED -> RETIRED (retire: only status + retired_at change)
--   RETIRED  -> immutable
-- Publish flow inserts the new version row as DRAFT, copies its rules, then
-- flips it to REVIEWED; that keeps the rules-of-non-draft guard consistent.
-- +goose StatementBegin
CREATE FUNCTION care_profile_version_update_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.version <> OLD.version OR NEW.profile_id <> OLD.profile_id
     OR NEW.workspace_id <> OLD.workspace_id OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'care profile version identity is immutable';
  END IF;
  IF OLD.status = 'DRAFT' THEN
    IF NEW.status = 'REVIEWED' AND
       (NEW.reviewer_label IS NULL OR NEW.reviewed_on IS NULL OR NEW.published_at IS NULL) THEN
      RAISE EXCEPTION 'publishing requires reviewer attribution fields';
    END IF;
    RETURN NEW;
  ELSIF OLD.status = 'REVIEWED' THEN
    IF NEW.status <> 'RETIRED' THEN
      RAISE EXCEPTION 'published care profile version content is immutable';
    END IF;
    IF NEW.environment <> OLD.environment
       OR NEW.reviewer_label IS DISTINCT FROM OLD.reviewer_label
       OR NEW.reviewed_on IS DISTINCT FROM OLD.reviewed_on
       OR NEW.published_at IS DISTINCT FROM OLD.published_at THEN
      RAISE EXCEPTION 'retire may only change status and retired_at';
    END IF;
    RETURN NEW;
  ELSE
    RAISE EXCEPTION 'retired care profile versions are immutable';
  END IF;
END $$;
-- +goose StatementEnd

CREATE TABLE care_profile_versions (
  id uuid PRIMARY KEY,
  profile_id uuid NOT NULL REFERENCES care_profiles(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  version integer NOT NULL CHECK (version > 0),
  status profile_status NOT NULL,
  -- environment predicates: {"situations":[...],"hemispheres":[...]}; empty = any
  environment jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(environment) = 'object'),
  reviewer_label text CHECK (reviewer_label IS NULL OR char_length(reviewer_label) <= 300),
  reviewed_on date,
  published_at timestamptz,
  retired_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (profile_id, version),
  -- REVIEWED requires owner-visible attribution
  CONSTRAINT chk_reviewed_attribution CHECK (status <> 'REVIEWED'
    OR (reviewer_label IS NOT NULL AND reviewed_on IS NOT NULL AND published_at IS NOT NULL))
);
CREATE INDEX care_profile_versions_workspace_idx ON care_profile_versions(workspace_id);
CREATE TRIGGER care_profile_version_guard BEFORE UPDATE ON care_profile_versions
  FOR EACH ROW EXECUTE FUNCTION care_profile_version_update_guard();

CREATE TABLE care_rules (
  id uuid PRIMARY KEY,
  profile_version_id uuid NOT NULL REFERENCES care_profile_versions(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  rule_key text NOT NULL CHECK (rule_key ~ '^[a-z0-9][a-z0-9_-]{0,99}$'),
  kind rule_kind NOT NULL,
  -- set exactly when kind = ACTION
  activity_kind activity_kind,
  recurrence jsonb NOT NULL CHECK (jsonb_typeof(recurrence) = 'object' AND valid_recurrence(recurrence)),
  required_facts text[] NOT NULL DEFAULT '{}',
  weather_sensitivity weather_sensitivity NOT NULL DEFAULT 'NONE',
  guidance_text text NOT NULL CHECK (char_length(guidance_text) BETWEEN 1 AND 4000),
  UNIQUE (profile_version_id, rule_key),
  CONSTRAINT chk_rule_activity_exclusive CHECK ((kind = 'ACTION') = (activity_kind IS NOT NULL)),
  CONSTRAINT chk_rule_required_facts CHECK (required_facts <@ ARRAY['CULTIVAR','SCIENTIFIC_NAME','PLACEMENT','LOCATION_SITUATION','POT_VOLUME','REGION','HEMISPHERE']::text[])
);
CREATE INDEX care_rules_workspace_idx ON care_rules(workspace_id);

-- Rules may only be written while their version is DRAFT; published rule sets
-- are frozen (publish copies rules into the new version before flipping it).
-- A missing parent row means the version itself is being deleted in this
-- statement; only DRAFT versions are deletable (see the version delete guard),
-- so cascading rule deletes from a gone parent are legitimate.
-- +goose StatementBegin
CREATE FUNCTION care_rules_version_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_status profile_status;
BEGIN
  SELECT status INTO v_status FROM care_profile_versions
   WHERE id = COALESCE(NEW.profile_version_id, OLD.profile_version_id);
  IF v_status IS NOT NULL AND v_status <> 'DRAFT' THEN
    RAISE EXCEPTION 'rules of a non-draft profile version are immutable';
  END IF;
  RETURN COALESCE(NEW, OLD);
END $$;
-- +goose StatementEnd
CREATE TRIGGER care_rules_guard BEFORE INSERT OR UPDATE OR DELETE ON care_rules
  FOR EACH ROW EXECUTE FUNCTION care_rules_version_guard();

-- Published/retired versions are undeletable so their rule sets can never be
-- cascade-removed; DRAFT versions may be deleted (rules cascade with them).
-- +goose StatementBegin
CREATE FUNCTION care_profile_version_delete_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'DRAFT' THEN
    RAISE EXCEPTION 'published care profile versions cannot be deleted';
  END IF;
  RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER care_profile_version_delete_guard BEFORE DELETE ON care_profile_versions
  FOR EACH ROW EXECUTE FUNCTION care_profile_version_delete_guard();

CREATE TABLE care_rule_sources (
  rule_id uuid NOT NULL REFERENCES care_rules(id) ON DELETE CASCADE,
  source_version_id uuid NOT NULL REFERENCES source_versions(id) ON DELETE RESTRICT,
  PRIMARY KEY (rule_id, source_version_id)
);

-- ---------- plans ----------
CREATE TABLE care_plans (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  -- pinned profile version; RESTRICT keeps pinned citations inspectable
  profile_version_id uuid REFERENCES care_profile_versions(id) ON DELETE RESTRICT,
  timezone text NOT NULL CHECK (timezone ~ '^[A-Za-z][A-Za-z0-9_+/-]{1,99}$'),
  enabled boolean NOT NULL DEFAULT true,
  -- HH:MM local in plan timezone; default 09:00
  notification_time text NOT NULL DEFAULT '09:00'
    CHECK (notification_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (plant_id)
);
CREATE INDEX care_plans_workspace_idx ON care_plans(workspace_id);
CREATE TRIGGER care_plans_updated BEFORE UPDATE ON care_plans
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE care_plan_overrides (
  id uuid PRIMARY KEY,
  plan_id uuid NOT NULL REFERENCES care_plans(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  rule_key text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  recurrence_override jsonb
    CHECK (recurrence_override IS NULL OR (jsonb_typeof(recurrence_override) = 'object' AND valid_recurrence(recurrence_override))),
  UNIQUE (plan_id, rule_key)
);

-- ---------- rule-level recurrence cursors (03: independent of activity history) ----------
CREATE TABLE care_rule_cursors (
  plan_id uuid NOT NULL REFERENCES care_plans(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  rule_key text NOT NULL,
  -- last resolved occurrence ordinal; null until the first decision
  last_resolved_ordinal bigint CHECK (last_resolved_ordinal IS NULL OR last_resolved_ordinal >= 0),
  decision occurrence_status,
  anchor_date date,
  next_due_date date,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (plan_id, rule_key)
);

-- ---------- occurrences ----------
CREATE TABLE care_occurrences (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plan_id uuid NOT NULL REFERENCES care_plans(id) ON DELETE CASCADE,
  rule_key text NOT NULL,
  kind rule_kind NOT NULL,
  activity_kind activity_kind,
  -- stable logical key (plan:rule:logical-date/ordinal); never changes with snooze
  occurrence_key text NOT NULL CHECK (char_length(occurrence_key) <= 512),
  original_due_date date NOT NULL,
  current_due_date date NOT NULL,
  snoozed_until date,
  status occurrence_status NOT NULL DEFAULT 'DUE',
  completed_activity_id uuid REFERENCES activities(id) ON DELETE RESTRICT,
  resolved_at timestamptz,
  plan_timezone text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (plan_id, occurrence_key),
  CONSTRAINT chk_occurrence_activity_exclusive CHECK ((kind = 'ACTION') = (activity_kind IS NOT NULL)),
  CONSTRAINT chk_occurrence_resolution CHECK ((status = 'DUE') = (resolved_at IS NULL)),
  CONSTRAINT chk_occurrence_completed_activity CHECK (status <> 'COMPLETED' OR completed_activity_id IS NOT NULL),
  CONSTRAINT chk_occurrence_snooze_forward CHECK (snoozed_until IS NULL OR snoozed_until >= current_due_date),
  CONSTRAINT chk_occurrence_due_forward CHECK (current_due_date >= original_due_date)
);
CREATE INDEX care_occurrences_agenda_idx
  ON care_occurrences(workspace_id, current_due_date, id) WHERE status = 'DUE';
CREATE INDEX care_occurrences_plan_due_idx ON care_occurrences(plan_id, current_due_date, id);
CREATE TRIGGER care_occurrences_updated BEFORE UPDATE ON care_occurrences
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------- weather snapshots (advisory evidence only) ----------
CREATE TABLE weather_snapshots (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  location_id uuid NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
  provider text NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 100),
  fetched_at timestamptz NOT NULL,
  valid_from timestamptz NOT NULL,
  valid_until timestamptz NOT NULL,
  high_temp_c numeric,
  low_temp_c numeric,
  rainfall_mm numeric CHECK (rainfall_mm IS NULL OR rainfall_mm >= 0),
  et0_mm numeric CHECK (et0_mm IS NULL OR et0_mm >= 0),
  license text CHECK (license IS NULL OR char_length(license) <= 500),
  expires_at timestamptz NOT NULL,
  CONSTRAINT chk_weather_window CHECK (valid_until > valid_from)
);
CREATE INDEX weather_snapshots_location_fetched_idx ON weather_snapshots(location_id, fetched_at DESC);

-- ---------- notifications ----------
CREATE TABLE notifications (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  owner_id uuid NOT NULL REFERENCES owner(id) ON DELETE CASCADE,
  channel notification_channel NOT NULL,
  -- stable dedup key: unique per occurrence/date/channel; duplicate ticks
  -- cannot create duplicate intents
  dedup_key text NOT NULL CHECK (char_length(dedup_key) <= 512),
  ref_kind notification_ref_kind NOT NULL,
  ref_id uuid NOT NULL,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 280),
  body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
  digest_date date,
  read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, dedup_key)
);
CREATE INDEX notifications_workspace_created_idx ON notifications(workspace_id, created_at DESC, id DESC);
CREATE INDEX notifications_workspace_unread_idx
  ON notifications(workspace_id, created_at DESC, id DESC) WHERE read_at IS NULL;

CREATE TABLE delivery_attempts (
  id uuid PRIMARY KEY,
  notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  state delivery_state NOT NULL,
  provider_message_id text CHECK (provider_message_id IS NULL OR char_length(provider_message_id) <= 512),
  error_code text CHECK (error_code IS NULL OR char_length(error_code) <= 100),
  attempted_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX delivery_attempts_notification_idx ON delivery_attempts(notification_id, attempted_at DESC);

-- +goose Down
-- Enum values (including journal_entry_type 'CARE') cannot be removed in
-- Postgres; Down drops relations only, matching the append-only enum policy.
DROP TABLE IF EXISTS delivery_attempts;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS weather_snapshots;
DROP TABLE IF EXISTS care_occurrences;
DROP TABLE IF EXISTS care_rule_cursors;
DROP TABLE IF EXISTS care_plan_overrides;
DROP TABLE IF EXISTS care_plans;
DROP TABLE IF EXISTS care_rule_sources;
DROP TABLE IF EXISTS care_rules;
DROP TABLE IF EXISTS care_profile_versions;
DROP TABLE IF EXISTS care_profiles;
DROP TABLE IF EXISTS source_versions;
DROP TABLE IF EXISTS sources;
DROP FUNCTION IF EXISTS care_profile_version_delete_guard();
DROP FUNCTION IF EXISTS care_rules_version_guard();
DROP FUNCTION IF EXISTS care_profile_version_update_guard();
DROP FUNCTION IF EXISTS forbid_row_update();
DROP FUNCTION IF EXISTS valid_recurrence(jsonb);
