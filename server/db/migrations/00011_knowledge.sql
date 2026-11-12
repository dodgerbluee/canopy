-- +goose Up
CREATE TABLE garden_knowledge_settings (
 workspace_id uuid PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
 country text NOT NULL DEFAULT '' CHECK (length(country) IN (0,2)),
 region text NOT NULL DEFAULT '' CHECK (length(region)<=160),
 place text NOT NULL DEFAULT '' CHECK (length(place)<=160),
 hardiness_zone text CHECK (length(hardiness_zone)<=40),
 zone_system text CHECK (length(zone_system)<=40),
 enabled boolean NOT NULL DEFAULT false,
 revision integer NOT NULL DEFAULT 1
);
CREATE TABLE knowledge_documents (
 url text PRIMARY KEY,
 provider text NOT NULL,
 content_sha256 text NOT NULL,
 facts jsonb NOT NULL,
 fetched_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL
);
CREATE TABLE knowledge_profiles (
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 profile_key text NOT NULL,
 facts jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,profile_key)
);
CREATE TABLE plant_knowledge (
 plant_id uuid PRIMARY KEY REFERENCES plants(id) ON DELETE CASCADE,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 generation bigint NOT NULL DEFAULT 1,
 state text NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','PROCESSING','READY','NEEDS_LOCATION','DISABLED','UNSUPPORTED','FAILED')),
 message text NOT NULL DEFAULT 'Waiting for regional guidance.',
 profile_key text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz
);
CREATE INDEX plant_knowledge_pending ON plant_knowledge(state,updated_at);
-- A durable outbox: every creation/import/identity edit is observed,
-- including non-HTTP imports. No outbound request runs in the plant transaction.
-- +goose StatementBegin
CREATE FUNCTION queue_plant_knowledge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO plant_knowledge(plant_id,workspace_id) VALUES(NEW.id,NEW.workspace_id)
 ON CONFLICT(plant_id) DO UPDATE SET generation=plant_knowledge.generation+1,state='PENDING',profile_key=NULL,message='Plant changed; refreshing regional guidance.',updated_at=now(),lease_until=NULL;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER plant_knowledge_queue AFTER INSERT OR UPDATE OF name,scientific_name,cultivar,category,status ON plants FOR EACH ROW EXECUTE FUNCTION queue_plant_knowledge();
INSERT INTO plant_knowledge(plant_id,workspace_id) SELECT id,workspace_id FROM plants;
-- +goose Down
DROP TRIGGER plant_knowledge_queue ON plants;
DROP FUNCTION queue_plant_knowledge();
DROP TABLE plant_knowledge;
DROP TABLE knowledge_profiles;
DROP TABLE knowledge_documents;
DROP TABLE garden_knowledge_settings;
