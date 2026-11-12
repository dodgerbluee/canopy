-- +goose Up
-- 00005_care_scheduler.sql — T23: durable occurrence materialization and
-- the completion/skip/snooze/correct flows. Additive only (ADR-0002
-- addendum: 00004 stays reserved for the AI lane).

-- Per-plan materialization watermark: the persisted scheduler cursor that
-- makes startup catch-up and duplicate ticks harmless (03: "A persisted
-- scheduler cursor enables startup catch-up; application uniqueness keys
-- make duplicate ticks harmless").
ALTER TABLE care_plans ADD COLUMN last_generated_through date;

-- Engine output persisted on each occurrence: the logical ordinal (drives
-- cursor replay for undo), optimistic revision for the resolution inputs,
-- typed reason material (OccurrenceReason in the SDL), and the annual
-- window end.
ALTER TABLE care_occurrences ADD COLUMN ordinal bigint
  CHECK (ordinal IS NULL OR ordinal >= 0);
ALTER TABLE care_occurrences ADD COLUMN revision integer NOT NULL DEFAULT 1
  CHECK (revision > 0);
ALTER TABLE care_occurrences ADD COLUMN reason_text text
  CHECK (reason_text IS NULL OR char_length(reason_text) <= 4000);
ALTER TABLE care_occurrences ADD COLUMN missing_facts text[] NOT NULL DEFAULT '{}';
ALTER TABLE care_occurrences ADD COLUMN evidence_notes text[] NOT NULL DEFAULT '{}';
ALTER TABLE care_occurrences ADD COLUMN window_end date;

-- +goose Down
ALTER TABLE care_occurrences DROP COLUMN IF EXISTS window_end;
ALTER TABLE care_occurrences DROP COLUMN IF EXISTS evidence_notes;
ALTER TABLE care_occurrences DROP COLUMN IF EXISTS missing_facts;
ALTER TABLE care_occurrences DROP COLUMN IF EXISTS reason_text;
ALTER TABLE care_occurrences DROP COLUMN IF EXISTS revision;
ALTER TABLE care_occurrences DROP COLUMN IF EXISTS ordinal;
ALTER TABLE care_plans DROP COLUMN IF EXISTS last_generated_through;
