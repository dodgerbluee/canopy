-- +goose Up
-- 00006_weather_evidence.sql — T26: weather provider, freshness and
-- context evaluation. Additive only (00004 stays reserved for the AI
-- lane).
--
-- An advisory result references the EXACT snapshot that backed it
-- (03-contracts: typed evidence, never free-form only). The occurrence
-- stores the snapshot id chosen at materialization time; ON DELETE SET
-- NULL keeps the advice honest when a snapshot ages out of retention —
-- the typed reason text survives, the evidence entry simply goes away.
ALTER TABLE care_occurrences
  ADD COLUMN weather_snapshot_id uuid REFERENCES weather_snapshots(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE care_occurrences DROP COLUMN IF EXISTS weather_snapshot_id;
