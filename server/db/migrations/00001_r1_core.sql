-- +goose Up
-- R1 core schema (T02.r1). Semantics authoritative from docs/plan/03-contracts.md.
-- Care/AI tables land in T02.care/T02.ai as later migrations.

-- ---------- enums ----------
CREATE TYPE location_situation AS ENUM ('INDOOR','OUTDOOR','COVERED');
CREATE TYPE plant_type AS ENUM ('INDIVIDUAL','GROUP');
CREATE TYPE plant_category AS ENUM ('CITRUS','FIG','STRAWBERRY','BLACKBERRY','OTHER');
CREATE TYPE plant_status AS ENUM ('ACTIVE','ARCHIVED','DEAD');
CREATE TYPE context_placement AS ENUM ('CONTAINER','GROUND');
CREATE TYPE pricing_state AS ENUM ('UNKNOWN','KNOWN');
CREATE TYPE activity_kind AS ENUM ('WATER','FERTILIZE','PRUNE','REPOT','HARVEST','OBSERVE','NOTE');
CREATE TYPE quantity_kind AS ENUM ('VOLUME','MASS','LENGTH','COUNT','TEMPERATURE');
CREATE TYPE measurement_unit AS ENUM ('ML','L','G','KG','MM','CM','COUNT','C','F');
CREATE TYPE measurement_method AS ENUM ('MANUAL','SENSOR','IMAGE_ESTIMATE');
CREATE TYPE asset_purpose AS ENUM ('PHOTO','RECEIPT');
CREATE TYPE asset_state AS ENUM ('RESERVED','UPLOADED','PROCESSING','READY','FAILED','DELETING');
CREATE TYPE photo_viewpoint AS ENUM ('WHOLE','LEAF','FRUIT','OTHER');
CREATE TYPE photo_date_source AS ENUM ('USER','EXIF','UPLOAD_FALLBACK');
CREATE TYPE journal_entry_type AS ENUM ('ACTIVITY','PHOTO','PURCHASE','MEASUREMENT');
CREATE TYPE export_state AS ENUM ('QUEUED','RUNNING','READY','FAILED','EXPIRED');

-- ---------- shared trigger ----------
-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- ---------- identity ----------
CREATE TABLE owner (
  id uuid PRIMARY KEY,
  password_hash text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id uuid PRIMARY KEY,
  owner_id uuid NOT NULL REFERENCES owner(id) ON DELETE CASCADE,
  -- hash of the opaque authentication cookie token; the token itself is never stored
  token_hash text NOT NULL UNIQUE,
  -- CSRF synchronizer token: server-only DB value, returned only after authentication,
  -- retrievable for authenticated bootstrap after reload; never logged.
  csrf_token text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  last_seen_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_owner_idx ON sessions(owner_id);

-- ---------- workspace ----------
CREATE TABLE workspace (
  id uuid PRIMARY KEY,
  owner_id uuid NOT NULL REFERENCES owner(id),
  display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 160),
  timezone text NOT NULL,
  default_currency text NOT NULL CHECK (default_currency ~ '^[A-Z]{3}$'),
  settings_revision integer NOT NULL DEFAULT 1 CHECK (settings_revision > 0),
  -- approximate, private, user-entered
  latitude numeric(9,6) CHECK (latitude IS NULL OR (latitude BETWEEN -90 AND 90)),
  longitude numeric(9,6) CHECK (longitude IS NULL OR (longitude BETWEEN -180 AND 180)),
  location_label text CHECK (location_label IS NULL OR char_length(location_label) <= 160),
  -- privacy epoch: incremented by plant purge; async/export commits compare it
  privacy_epoch bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER workspace_updated BEFORE UPDATE ON workspace
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------- locations ----------
CREATE TABLE locations (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
  parent_id uuid REFERENCES locations(id) ON DELETE RESTRICT,
  situation location_situation NOT NULL,
  latitude numeric(9,6) CHECK (latitude IS NULL OR (latitude BETWEEN -90 AND 90)),
  longitude numeric(9,6) CHECK (longitude IS NULL OR (longitude BETWEEN -180 AND 180)),
  label text CHECK (label IS NULL OR char_length(label) <= 160),
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE INDEX locations_workspace_idx ON locations(workspace_id);
CREATE TRIGGER locations_updated BEFORE UPDATE ON locations
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- cycle/depth <= 5 enforced by service (recursive CTE) in one transaction

-- ---------- plants ----------
CREATE TABLE plants (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
  scientific_name text CHECK (scientific_name IS NULL OR char_length(scientific_name) <= 160),
  cultivar text CHECK (cultivar IS NULL OR char_length(cultivar) <= 160),
  rootstock text CHECK (rootstock IS NULL OR char_length(rootstock) <= 160),
  type plant_type NOT NULL,
  count integer CHECK (count IS NULL OR count > 0),
  category plant_category NOT NULL,
  -- read projection of acquisitions.acquired_on; updated only in the same transaction
  acquired_on date,
  planted_on date,
  estimated_age_years numeric(8,3) CHECK (estimated_age_years IS NULL OR (estimated_age_years >= 0 AND estimated_age_years <= 1000)),
  status plant_status NOT NULL DEFAULT 'ACTIVE',
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX plants_workspace_name_idx ON plants(workspace_id, lower(name), id);
CREATE INDEX plants_workspace_status_idx ON plants(workspace_id, status);
CREATE TRIGGER plants_updated BEFORE UPDATE ON plants
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------- plant contexts (immutable versioned transitions) ----------
CREATE TABLE plant_contexts (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  effective_from timestamptz NOT NULL,
  location_id uuid REFERENCES locations(id) ON DELETE RESTRICT,
  placement context_placement NOT NULL,
  pot_volume_liters numeric CHECK (pot_volume_liters IS NULL OR pot_volume_liters > 0),
  medium_note text CHECK (medium_note IS NULL OR char_length(medium_note) <= 20000),
  light_note text CHECK (light_note IS NULL OR char_length(light_note) <= 20000),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX plant_contexts_plant_effective_idx ON plant_contexts(plant_id, effective_from DESC);

-- ---------- tags ----------
CREATE TABLE tags (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  label text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 40),
  archived_at timestamptz,
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- workspace-normalized unique label among live tags
CREATE UNIQUE INDEX tags_workspace_label_idx ON tags(workspace_id, lower(label)) WHERE archived_at IS NULL;
CREATE TRIGGER tags_updated BEFORE UPDATE ON tags
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE plant_tags (
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  tag_id uuid NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (plant_id, tag_id)
);
CREATE INDEX plant_tags_tag_idx ON plant_tags(tag_id);

-- ---------- activities ----------
CREATE TABLE activities (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  kind activity_kind NOT NULL,
  occurred_at timestamptz NOT NULL,
  timezone text NOT NULL,
  note text CHECK (note IS NULL OR char_length(note) <= 20000),
  quantity numeric CHECK (quantity IS NULL OR quantity >= 0),
  unit measurement_unit,
  product text CHECK (product IS NULL OR char_length(product) <= 160),
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  voided_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- quantity and unit travel together
  CHECK ((quantity IS NULL) = (unit IS NULL)),
  -- kind/unit compatibility: water VOLUME, fertilizer MASS or VOLUME, others none
  CHECK (
    (kind = 'WATER' AND unit IN ('ML','L'))
    OR (kind = 'FERTILIZE' AND unit IN ('G','KG','ML','L'))
    OR (kind NOT IN ('WATER','FERTILIZE') AND quantity IS NULL AND unit IS NULL)
  )
);
CREATE INDEX activities_plant_occurred_idx ON activities(plant_id, occurred_at DESC, id DESC);
CREATE INDEX activities_workspace_occurred_idx ON activities(workspace_id, occurred_at DESC);
CREATE TRIGGER activities_updated BEFORE UPDATE ON activities
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------- measurements ----------
CREATE TABLE measurements (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  quantity_kind quantity_kind NOT NULL,
  value numeric NOT NULL,
  unit measurement_unit NOT NULL,
  observed_at timestamptz NOT NULL,
  method measurement_method NOT NULL DEFAULT 'MANUAL',
  uncertainty numeric CHECK (uncertainty IS NULL OR uncertainty >= 0),
  activity_id uuid REFERENCES activities(id) ON DELETE SET NULL,
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (
    (quantity_kind = 'VOLUME' AND unit IN ('ML','L'))
    OR (quantity_kind = 'MASS' AND unit IN ('G','KG'))
    OR (quantity_kind = 'LENGTH' AND unit IN ('MM','CM'))
    OR (quantity_kind = 'COUNT' AND unit = 'COUNT')
    OR (quantity_kind = 'TEMPERATURE' AND unit IN ('C','F'))
  )
);
CREATE INDEX measurements_plant_observed_idx ON measurements(plant_id, observed_at DESC, id DESC);
CREATE TRIGGER measurements_updated BEFORE UPDATE ON measurements
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------- purchases / lines / allocations ----------
CREATE TABLE purchases (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  seller text CHECK (seller IS NULL OR char_length(seller) <= 160),
  purchased_on date,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  total_minor numeric NOT NULL CHECK (total_minor >= 0),
  shipping_minor numeric NOT NULL DEFAULT 0 CHECK (shipping_minor >= 0),
  tax_minor numeric NOT NULL DEFAULT 0 CHECK (tax_minor >= 0),
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  voided_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (tax_minor + shipping_minor <= total_minor)
);
CREATE INDEX purchases_workspace_purchased_idx ON purchases(workspace_id, purchased_on DESC, id DESC);
CREATE TRIGGER purchases_updated BEFORE UPDATE ON purchases
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE purchase_lines (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  purchase_id uuid NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
  description text NOT NULL CHECK (char_length(description) BETWEEN 1 AND 200),
  quantity numeric NOT NULL CHECK (quantity > 0),
  line_total_minor numeric NOT NULL CHECK (line_total_minor >= 0)
);
CREATE INDEX purchase_lines_purchase_idx ON purchase_lines(purchase_id);

CREATE TABLE purchase_allocations (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  purchase_line_id uuid NOT NULL REFERENCES purchase_lines(id) ON DELETE CASCADE,
  -- null = unallocated supplies (in workspace total, not plant totals)
  plant_id uuid REFERENCES plants(id) ON DELETE RESTRICT,
  line_minor numeric NOT NULL CHECK (line_minor >= 0),
  tax_minor numeric NOT NULL DEFAULT 0 CHECK (tax_minor >= 0),
  shipping_minor numeric NOT NULL DEFAULT 0 CHECK (shipping_minor >= 0)
);
CREATE UNIQUE INDEX purchase_allocations_line_plant_idx
  ON purchase_allocations(purchase_line_id, plant_id) WHERE plant_id IS NOT NULL;
CREATE INDEX purchase_allocations_plant_idx ON purchase_allocations(plant_id) WHERE plant_id IS NOT NULL;
CREATE INDEX purchase_allocations_workspace_idx ON purchase_allocations(workspace_id);
-- per-line lineMinor sums and overhead sums are enforced atomically in the service
-- transaction (cross-row arithmetic; see ADR-0002)

-- ---------- acquisitions ----------
CREATE TABLE acquisitions (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  seller text CHECK (seller IS NULL OR char_length(seller) <= 160),
  acquired_on date,
  pricing_state pricing_state NOT NULL,
  -- deferred: purchase/line/allocation/acquisition are created in one transaction
  initial_purchase_allocation_id uuid REFERENCES purchase_allocations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- one active provenance record per plant
  UNIQUE (workspace_id, plant_id),
  -- KNOWN requires exactly one allocation; UNKNOWN requires none
  CHECK (
    (pricing_state = 'KNOWN' AND initial_purchase_allocation_id IS NOT NULL)
    OR (pricing_state = 'UNKNOWN' AND initial_purchase_allocation_id IS NULL)
  )
);
CREATE TRIGGER acquisitions_updated BEFORE UPDATE ON acquisitions
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------- assets / media ----------
CREATE TABLE assets (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  purpose asset_purpose NOT NULL,
  state asset_state NOT NULL DEFAULT 'RESERVED',
  byte_count integer NOT NULL CHECK (byte_count >= 0),
  mime_type text,
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  -- opaque server-generated storage key; never derived from user input
  storage_key text NOT NULL UNIQUE,
  display_name text CHECK (display_name IS NULL OR char_length(display_name) <= 200),
  error_code text,
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX assets_workspace_state_idx ON assets(workspace_id, state);
CREATE TRIGGER assets_updated BEFORE UPDATE ON assets
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- deterministic derivatives produced by the processor (asset + processorVersion keyed)
CREATE TABLE asset_variants (
  asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (name IN ('thumb','medium','display','original')),
  width_px integer,
  height_px integer,
  storage_key text NOT NULL,
  byte_count integer NOT NULL CHECK (byte_count >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (asset_id, name)
);

CREATE TABLE plant_photos (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
  taken_at timestamptz,
  date_source photo_date_source NOT NULL,
  timezone text,
  caption text CHECK (caption IS NULL OR char_length(caption) <= 20000),
  viewpoint photo_viewpoint NOT NULL,
  is_cover boolean NOT NULL DEFAULT false,
  revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- at most one cover per plant
CREATE UNIQUE INDEX plant_photos_cover_idx ON plant_photos(plant_id) WHERE is_cover AND deleted_at IS NULL;
CREATE INDEX plant_photos_plant_idx ON plant_photos(plant_id) WHERE deleted_at IS NULL;
CREATE TRIGGER plant_photos_updated BEFORE UPDATE ON plant_photos
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE purchase_receipts (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  purchase_id uuid NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
  asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX purchase_receipts_purchase_idx ON purchase_receipts(purchase_id);

-- ---------- journal projection ----------
CREATE TABLE journal_entries (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  plant_id uuid NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
  entry_type journal_entry_type NOT NULL,
  source_id uuid NOT NULL,
  occurred_at timestamptz NOT NULL,
  summary text NOT NULL CHECK (char_length(summary) <= 280),
  tombstoned_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, entry_type, source_id, plant_id)
);
CREATE INDEX journal_entries_plant_time_idx ON journal_entries(workspace_id, plant_id, occurred_at DESC, id DESC);
CREATE INDEX journal_entries_workspace_time_idx ON journal_entries(workspace_id, occurred_at DESC, id DESC);

-- ---------- audit + idempotency ----------
CREATE TABLE audit_revisions (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  actor_id uuid NOT NULL REFERENCES owner(id),
  action text NOT NULL,
  aggregate_type text NOT NULL,
  aggregate_id uuid NOT NULL,
  revision integer NOT NULL,
  request_id text NOT NULL,
  -- bounded prior values, never secrets
  prior_values jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_revisions_aggregate_idx ON audit_revisions(aggregate_type, aggregate_id, revision);

CREATE TABLE mutation_receipts (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  operation text NOT NULL,
  idempotency_key text NOT NULL,
  request_hash text NOT NULL,
  result jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  UNIQUE (workspace_id, operation, idempotency_key)
);

-- ---------- export jobs ----------
CREATE TABLE export_jobs (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  state export_state NOT NULL DEFAULT 'QUEUED',
  snapshot_at timestamptz NOT NULL,
  manifest_sha256 text CHECK (manifest_sha256 IS NULL OR manifest_sha256 ~ '^[0-9a-f]{64}$'),
  error_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  expires_at timestamptz
);

-- +goose Down
DROP TABLE IF EXISTS export_jobs;
DROP TABLE IF EXISTS mutation_receipts;
DROP TABLE IF EXISTS audit_revisions;
DROP TABLE IF EXISTS journal_entries;
DROP TABLE IF EXISTS purchase_receipts;
DROP TABLE IF EXISTS plant_photos;
DROP TABLE IF EXISTS asset_variants;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS acquisitions;
DROP TABLE IF EXISTS purchase_allocations;
DROP TABLE IF EXISTS purchase_lines;
DROP TABLE IF EXISTS purchases;
DROP TABLE IF EXISTS measurements;
DROP TABLE IF EXISTS activities;
DROP TABLE IF EXISTS plant_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS plant_contexts;
DROP TABLE IF EXISTS plants;
DROP TABLE IF EXISTS locations;
DROP TABLE IF EXISTS workspace;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS owner;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TYPE IF EXISTS export_state;
DROP TYPE IF EXISTS journal_entry_type;
DROP TYPE IF EXISTS photo_date_source;
DROP TYPE IF EXISTS photo_viewpoint;
DROP TYPE IF EXISTS asset_state;
DROP TYPE IF EXISTS asset_purpose;
DROP TYPE IF EXISTS measurement_method;
DROP TYPE IF EXISTS measurement_unit;
DROP TYPE IF EXISTS quantity_kind;
DROP TYPE IF EXISTS activity_kind;
DROP TYPE IF EXISTS pricing_state;
DROP TYPE IF EXISTS context_placement;
DROP TYPE IF EXISTS plant_status;
DROP TYPE IF EXISTS plant_category;
DROP TYPE IF EXISTS plant_type;
DROP TYPE IF EXISTS location_situation;
