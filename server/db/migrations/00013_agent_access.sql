-- +goose Up
CREATE TABLE agent_grants (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 120),
 scopes text[] NOT NULL, plant_ids uuid[] NOT NULL DEFAULT '{}',
 expires_at timestamptz NOT NULL, revoked_at timestamptz, revision integer NOT NULL DEFAULT 1,
 last_used_at timestamptz, last_tool text, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id)
);
CREATE TABLE agent_credentials (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, grant_id uuid NOT NULL,
 verifier bytea NOT NULL, revoked_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,grant_id) REFERENCES agent_grants(workspace_id,id) ON DELETE CASCADE
);
CREATE INDEX agent_credentials_grant ON agent_credentials(grant_id);
-- +goose Down
DROP TABLE agent_credentials;
DROP TABLE agent_grants;
