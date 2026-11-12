-- +goose Up
-- T27: email delivery for notification intents. The intent row is the
-- durable record; delivery_attempts stays the append-only attempt log.
-- scheduled_at is the local-time delivery gate (digest_date at the
-- plan's notification time, DST-normalized); delivered_at acks the
-- first successful send. Message-ID is stable per intent (best effort,
-- not an exactly-once promise: a crash after send-before-ack redelivers).

ALTER TABLE notifications
  ADD COLUMN scheduled_at timestamptz,
  ADD COLUMN message_id text
    CHECK (message_id IS NULL OR char_length(message_id) BETWEEN 1 AND 512),
  ADD COLUMN delivered_at timestamptz;

-- Sweep surface: undelivered email intents, oldest gate first.
CREATE INDEX notifications_email_undelivered_idx
  ON notifications(scheduled_at, id)
  WHERE channel = 'EMAIL' AND delivered_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS notifications_email_undelivered_idx;
ALTER TABLE notifications
  DROP COLUMN IF EXISTS delivered_at,
  DROP COLUMN IF EXISTS message_id,
  DROP COLUMN IF EXISTS scheduled_at;
