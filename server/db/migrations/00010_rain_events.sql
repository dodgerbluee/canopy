-- +goose Up
CREATE TABLE rain_events (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    event_date date NOT NULL,
    amount_mm numeric CHECK (amount_mm >= 0 AND amount_mm <= 10000),
    note text CHECK (char_length(note) <= 2000),
    revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
    voided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rain_events_calendar ON rain_events(workspace_id, event_date, id) WHERE voided_at IS NULL;
-- +goose Down
DROP TABLE rain_events;
