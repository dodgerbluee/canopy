-- +goose Up
-- Advisory weather checks are independent of care schedules and plant locations.
-- The exact user-entered location/timezone scopes both cache and responses.
CREATE TABLE rain_reviews (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    event_date date NOT NULL,
    latitude numeric(9,6) NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude numeric(9,6) NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    timezone text NOT NULL,
    estimated_mm numeric CHECK (estimated_mm >= 0 AND estimated_mm <= 10000),
    checked_at timestamptz NOT NULL,
    response text NOT NULL DEFAULT 'PENDING' CHECK (response IN ('PENDING','CONFIRMED','DISMISSED')),
    responded_at timestamptz,
    rain_event_id uuid REFERENCES rain_events(id) ON DELETE SET NULL,
    UNIQUE(workspace_id,event_date,latitude,longitude,timezone)
);
CREATE INDEX rain_reviews_retention ON rain_reviews(event_date);
-- +goose Down
DROP TABLE rain_reviews;
