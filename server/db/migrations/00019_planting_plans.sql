-- +goose Up
-- Future intentions are separate from owned plants, recorded care and spending.
CREATE TYPE planting_plan_state AS ENUM ('IDEA','PLANNED','IN_PROGRESS','COMPLETED','CANCELLED');
CREATE TYPE planting_method AS ENUM ('START_INDOORS','DIRECT_SOW','NURSERY_PLANT');
CREATE TYPE planting_period_precision AS ENUM ('DATE_RANGE','MONTH_RANGE','SEASON','YEAR');
CREATE TYPE planting_season AS ENUM ('SPRING','SUMMER','AUTUMN','WINTER');
CREATE TYPE planting_action_kind AS ENUM ('PREPARE','BUY','START_INDOORS','DIRECT_SOW','TRANSPLANT','PLANT_OUT');
CREATE TYPE planting_action_state AS ENUM ('OPEN','DONE','DISMISSED');
CREATE TYPE planting_guidance_state AS ENUM ('PENDING','PROCESSING','READY','NEEDS_LOCATION','DISABLED','UNSUPPORTED','FAILED');
CREATE TYPE planting_link_mode AS ENUM ('CREATED','EXISTING');

-- Composite references reject cross-workspace links even outside the service.
CREATE UNIQUE INDEX planning_locations_scope_idx ON locations(workspace_id,id);
CREATE UNIQUE INDEX planning_plants_scope_idx ON plants(workspace_id,id);

CREATE TABLE planting_plans (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
 scientific_name text CHECK (scientific_name IS NULL OR char_length(btrim(scientific_name)) BETWEEN 1 AND 160),
 cultivar text CHECK (cultivar IS NULL OR char_length(btrim(cultivar)) BETWEEN 1 AND 160),
 category plant_category NOT NULL DEFAULT 'OTHER',
 desired_quantity integer CHECK (desired_quantity IS NULL OR desired_quantity > 0),
 method planting_method,
 intended_location_id uuid,
 intended_placement context_placement,
 target_precision planting_period_precision,
 target_from date,
 target_to date,
 target_from_month text,
 target_to_month text,
 target_year integer,
 target_season planting_season,
 budget_minor bigint CHECK (budget_minor IS NULL OR budget_minor BETWEEN 0 AND 1000000000000000),
 budget_currency text CHECK (budget_currency IS NULL OR budget_currency ~ '^[A-Z]{3}$'),
 notes text CHECK (notes IS NULL OR char_length(notes) <= 20000),
 state planting_plan_state NOT NULL DEFAULT 'IDEA',
 resume_state planting_plan_state,
 cancelled_at timestamptz,
 completed_at timestamptz,
 revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
 guidance_generation integer NOT NULL DEFAULT 1 CHECK (guidance_generation > 0),
 accepted_guidance_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id),
 FOREIGN KEY(workspace_id,intended_location_id) REFERENCES locations(workspace_id,id) ON DELETE SET NULL (intended_location_id),
 CONSTRAINT planting_budget_pair CHECK ((budget_minor IS NULL) = (budget_currency IS NULL)),
 CONSTRAINT planting_completion_date CHECK ((state='COMPLETED') = (completed_at IS NOT NULL)),
 CONSTRAINT planting_cancellation CHECK (
  (state='CANCELLED' AND cancelled_at IS NOT NULL AND resume_state IS NOT NULL AND resume_state IN ('IDEA','PLANNED','IN_PROGRESS'))
  OR (state<>'CANCELLED' AND cancelled_at IS NULL AND resume_state IS NULL)
 ),
 CONSTRAINT planting_target_shape CHECK (CASE target_precision
  WHEN 'DATE_RANGE' THEN target_from IS NOT NULL AND target_to IS NOT NULL AND target_from<=target_to
   AND isfinite(target_from) AND isfinite(target_to)
   AND extract(year from target_from) BETWEEN 1 AND 9999 AND extract(year from target_to) BETWEEN 1 AND 9999
   AND target_from_month IS NULL AND target_to_month IS NULL AND target_year IS NULL AND target_season IS NULL
  WHEN 'MONTH_RANGE' THEN target_from_month IS NOT NULL AND target_to_month IS NOT NULL
   AND target_from_month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$' AND left(target_from_month,4)<>'0000'
   AND target_to_month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$' AND left(target_to_month,4)<>'0000'
   AND target_from_month COLLATE "C" <= target_to_month COLLATE "C"
   AND target_from IS NULL AND target_to IS NULL AND target_year IS NULL AND target_season IS NULL
  WHEN 'SEASON' THEN target_year IS NOT NULL AND target_year BETWEEN 1 AND 9999 AND target_season IS NOT NULL
   AND target_from IS NULL AND target_to IS NULL AND target_from_month IS NULL AND target_to_month IS NULL
  WHEN 'YEAR' THEN target_year IS NOT NULL AND target_year BETWEEN 1 AND 9999
   AND target_from IS NULL AND target_to IS NULL AND target_from_month IS NULL AND target_to_month IS NULL AND target_season IS NULL
  ELSE target_precision IS NULL AND target_from IS NULL AND target_to IS NULL
   AND target_from_month IS NULL AND target_to_month IS NULL AND target_year IS NULL AND target_season IS NULL
 END),
 CONSTRAINT planting_accepted_target CHECK (accepted_guidance_id IS NULL OR target_precision IS NOT NULL)
);
CREATE INDEX planting_plans_scope_state_idx ON planting_plans(workspace_id,state,id);
CREATE INDEX planting_plans_scope_name_idx ON planting_plans(workspace_id,lower(name) COLLATE "C",id);
CREATE TRIGGER planting_plans_updated BEFORE UPDATE ON planting_plans FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE planting_actions (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 plan_id uuid NOT NULL,
 kind planting_action_kind NOT NULL,
 title text NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 160),
 scheduled_on date NOT NULL CHECK (isfinite(scheduled_on) AND extract(year from scheduled_on) BETWEEN 1 AND 9999),
 reminder_on date CHECK (reminder_on IS NULL OR (isfinite(reminder_on) AND extract(year from reminder_on) BETWEEN 1 AND 9999 AND reminder_on<=scheduled_on)),
 schedule_revision integer NOT NULL DEFAULT 1 CHECK (schedule_revision > 0),
 notes text CHECK (notes IS NULL OR char_length(notes)<=20000),
 state planting_action_state NOT NULL DEFAULT 'OPEN',
 completed_on date,
 completed_at timestamptz,
 dismissed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,plan_id,id),
 FOREIGN KEY(workspace_id,plan_id) REFERENCES planting_plans(workspace_id,id) ON DELETE CASCADE,
 CONSTRAINT planting_action_completion CHECK (
  (state='DONE' AND completed_on IS NOT NULL AND completed_at IS NOT NULL AND isfinite(completed_on) AND extract(year from completed_on) BETWEEN 1 AND 9999)
  OR (state<>'DONE' AND completed_on IS NULL AND completed_at IS NULL)
 ),
 CONSTRAINT planting_action_dismissal CHECK ((state='DISMISSED')=(dismissed_at IS NOT NULL))
);
CREATE INDEX planting_actions_due_idx ON planting_actions(workspace_id,scheduled_on,id) WHERE state='OPEN';
CREATE INDEX planting_actions_plan_idx ON planting_actions(workspace_id,plan_id,scheduled_on,id);
CREATE INDEX planting_actions_reminder_idx ON planting_actions(reminder_on,id) WHERE state='OPEN' AND reminder_on IS NOT NULL;
CREATE TRIGGER planting_actions_updated BEFORE UPDATE ON planting_actions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE planting_guidance_snapshots (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 plan_id uuid NOT NULL,
 generation integer NOT NULL CHECK (generation > 0),
 context_sha256 text NOT NULL CHECK (context_sha256 ~ '^[0-9a-f]{64}$'),
 state planting_guidance_state NOT NULL DEFAULT 'PENDING',
 message text NOT NULL DEFAULT '' CHECK (char_length(message)<=1000),
 facts jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(facts)='array' AND octet_length(facts::text)<=262144),
 assumptions text[] NOT NULL DEFAULT '{}' CHECK (cardinality(assumptions)<=16 AND array_position(assumptions,NULL) IS NULL),
 retrieved_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,plan_id,id),
 UNIQUE(plan_id,generation),
 FOREIGN KEY(workspace_id,plan_id) REFERENCES planting_plans(workspace_id,id) ON DELETE CASCADE,
 CONSTRAINT planting_guidance_ready CHECK (state<>'READY' OR (retrieved_at IS NOT NULL AND CASE WHEN jsonb_typeof(facts)='array' THEN jsonb_array_length(facts)>0 ELSE false END))
);
CREATE INDEX planting_guidance_pending_idx ON planting_guidance_snapshots(state,updated_at,id) WHERE state IN ('PENDING','PROCESSING');
ALTER TABLE planting_plans ADD CONSTRAINT planting_accepted_guidance_scope
 FOREIGN KEY(workspace_id,id,accepted_guidance_id) REFERENCES planting_guidance_snapshots(workspace_id,plan_id,id)
 DEFERRABLE INITIALLY DEFERRED;

-- A finished snapshot is provenance; a refresh writes a new generation.
-- +goose StatementBegin
CREATE FUNCTION protect_planting_guidance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state='READY' THEN
  IF NEW IS DISTINCT FROM OLD THEN
   RAISE EXCEPTION 'ready planting guidance is immutable' USING ERRCODE='23514';
  END IF;
  RETURN NULL;
 END IF;
 NEW.updated_at=now();
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER planting_guidance_immutable BEFORE UPDATE ON planting_guidance_snapshots FOR EACH ROW EXECUTE FUNCTION protect_planting_guidance();

CREATE TABLE planting_fulfillments (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 plan_id uuid NOT NULL UNIQUE,
 -- Null only after the real plant is purged. The link marker remains, preventing
 -- an old request from recreating erased data. Do not duplicate plant details.
 plant_id uuid,
 action_id uuid,
 mode planting_link_mode NOT NULL,
 linked_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,plan_id) REFERENCES planting_plans(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,plant_id) REFERENCES plants(workspace_id,id) ON DELETE SET NULL (plant_id),
 FOREIGN KEY(workspace_id,plan_id,action_id) REFERENCES planting_actions(workspace_id,plan_id,id) ON DELETE SET NULL (action_id)
);
CREATE INDEX planting_fulfillments_plant_idx ON planting_fulfillments(workspace_id,plant_id) WHERE plant_id IS NOT NULL;

-- +goose Down
ALTER TABLE planting_plans DROP CONSTRAINT planting_accepted_guidance_scope;
DROP TABLE planting_fulfillments;
DROP TABLE planting_guidance_snapshots;
DROP FUNCTION protect_planting_guidance();
DROP TABLE planting_actions;
DROP TABLE planting_plans;
DROP INDEX planning_plants_scope_idx;
DROP INDEX planning_locations_scope_idx;
DROP TYPE planting_link_mode,planting_guidance_state,planting_action_state,planting_action_kind,planting_season,planting_period_precision,planting_method,planting_plan_state;
