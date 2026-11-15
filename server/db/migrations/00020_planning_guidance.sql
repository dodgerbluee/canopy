-- +goose Up
-- Public, rebuildable source cache. No plan/user/provider credentials belong here.
CREATE TABLE knowledge_publications (
 url text PRIMARY KEY,
 provider text NOT NULL,
 content_sha256 text NOT NULL CHECK(content_sha256 ~ '^[0-9a-f]{64}$'),
 source_text text NOT NULL CHECK(octet_length(source_text) BETWEEN 1 AND 70000),
 fetched_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL,
 planning_key text CHECK(planning_key IS NULL OR planning_key ~ '^[0-9a-f]{64}$'),
 planning_facts jsonb CHECK(planning_facts IS NULL OR (jsonb_typeof(planning_facts)='array' AND octet_length(planning_facts::text)<=262144)),
 planning_extracted_at timestamptz,
 CHECK((planning_key IS NULL)=(planning_facts IS NULL)),
 CHECK((planning_key IS NULL)=(planning_extracted_at IS NULL))
);
CREATE TABLE planning_provider_usage (
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 window_start timestamptz NOT NULL,
 requests integer NOT NULL CHECK(requests BETWEEN 1 AND 8),
 PRIMARY KEY(workspace_id,window_start)
);
CREATE INDEX planning_provider_usage_expiry_idx ON planning_provider_usage(window_start);

-- Nullable so a populated pre-20 archive can restore without synthesizing data.
ALTER TABLE planting_guidance_snapshots ADD COLUMN next_check_at timestamptz DEFAULT now();
CREATE INDEX planting_guidance_review_idx ON planting_guidance_snapshots(next_check_at,id);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_planting_guidance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state='READY' THEN
  IF (to_jsonb(NEW)-'next_check_at') IS DISTINCT FROM (to_jsonb(OLD)-'next_check_at') THEN
   RAISE EXCEPTION 'ready planting guidance is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.next_check_at IS NOT DISTINCT FROM OLD.next_check_at THEN RETURN NULL; END IF;
  RETURN NEW;
 END IF;
 NEW.updated_at=now();
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX planting_guidance_review_idx;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_planting_guidance() RETURNS trigger LANGUAGE plpgsql AS $$
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
ALTER TABLE planting_guidance_snapshots DROP COLUMN next_check_at;
DROP TABLE planning_provider_usage;
DROP TABLE knowledge_publications;
