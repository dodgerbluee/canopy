-- +goose Up
-- Ephemeral operator capability. Never included in portable garden backups.
CREATE TABLE registration_handover (
  owner_id uuid PRIMARY KEY REFERENCES owner(id) ON DELETE CASCADE,
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE registration_handover;
