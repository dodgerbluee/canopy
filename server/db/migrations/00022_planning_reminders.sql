-- +goose Up
ALTER TYPE notification_ref_kind ADD VALUE IF NOT EXISTS 'PLANTING_ACTION';

-- +goose Down
DELETE FROM notifications WHERE ref_kind::text='PLANTING_ACTION';
-- PostgreSQL enum labels are retained on rollback, as with the care enums.
