-- +goose Up
CREATE TABLE ai_connections (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
 protocol text NOT NULL CHECK (protocol IN ('OPENAI_COMPATIBLE','ANTHROPIC','GEMINI')),
 endpoint text NOT NULL CHECK (length(endpoint)<=2000),
 model text NOT NULL CHECK (length(model) BETWEEN 1 AND 200),
 deployment_local boolean NOT NULL,
 supports_vision boolean NOT NULL,
 external_consent boolean NOT NULL,
 credential_ciphertext bytea NOT NULL DEFAULT '',
 reasoning_effort text NOT NULL DEFAULT '',
 token_limit_field text NOT NULL CHECK (token_limit_field IN ('MAX_TOKENS','MAX_COMPLETION_TOKENS')),
 revision integer NOT NULL DEFAULT 1,
 last_tested_at timestamptz,
 last_test_succeeded boolean,
 last_test_message text,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (workspace_id,id)
);
CREATE TABLE ai_feature_bindings (
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 feature text NOT NULL CHECK (feature IN ('GARDEN','VISION','RECEIPT')),
 mode text NOT NULL CHECK (mode IN ('DEFAULT','DISABLED','CONNECTION')),
 connection_id uuid,
 revision integer NOT NULL DEFAULT 1,
 PRIMARY KEY(workspace_id,feature),
 FOREIGN KEY(workspace_id,connection_id) REFERENCES ai_connections(workspace_id,id),
 CHECK ((mode='CONNECTION')=(connection_id IS NOT NULL))
);
-- +goose Down
DROP TABLE ai_feature_bindings;
DROP TABLE ai_connections;
