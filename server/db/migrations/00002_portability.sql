-- +goose Up
-- T38.a portability: the deletion ledger (minimal tombstones), the
-- maintenance quiesce flag, and the RESOURCE_GONE marker on idempotency
-- receipts whose stored response touched a purged entity.

ALTER TABLE mutation_receipts ADD COLUMN gone boolean NOT NULL DEFAULT false;

-- Minimal tombstones only: opaque deleted id, timestamp/action/actor and
-- the privacy epoch at purge time. Never any copied content (03 deletion
-- matrix). This table is the live side of the separately retained
-- deletion ledger that backups export and restores re-apply.
CREATE TABLE deletion_tombstones (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  entity_kind text NOT NULL,
  entity_id uuid NOT NULL,
  action text NOT NULL,
  actor_id uuid,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  privacy_epoch bigint NOT NULL
);
CREATE UNIQUE INDEX deletion_tombstones_entity_idx
  ON deletion_tombstones(workspace_id, entity_kind, entity_id);
CREATE INDEX deletion_tombstones_time_idx
  ON deletion_tombstones(workspace_id, occurred_at);

-- Single-row singleton: the quiesce flag a backup sets while it runs so
-- the API refuses writes/uploads and the operator can drain jobs.
CREATE TABLE system_state (
  id boolean PRIMARY KEY DEFAULT true CHECK (id),
  maintenance boolean NOT NULL DEFAULT false,
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO system_state (id, maintenance) VALUES (true, false);

-- +goose Down
DROP TABLE IF EXISTS system_state;
DROP TABLE IF EXISTS deletion_tombstones;
ALTER TABLE mutation_receipts DROP COLUMN IF EXISTS gone;
