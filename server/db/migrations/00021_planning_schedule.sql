-- +goose Up
-- Nullable for populated pre-21 backups, including historical accepted pointers.
ALTER TABLE planting_plans ADD COLUMN accepted_schedule jsonb;
ALTER TABLE planting_plans ADD CONSTRAINT planting_schedule_selection CHECK (
 accepted_schedule IS NULL OR (
  jsonb_typeof(accepted_schedule)='object' AND octet_length(accepted_schedule::text)<=8192
  AND accepted_guidance_id IS NOT NULL AND target_precision IS NOT NULL
 )
);

-- +goose Down
ALTER TABLE planting_plans DROP CONSTRAINT planting_schedule_selection;
ALTER TABLE planting_plans DROP COLUMN accepted_schedule;
