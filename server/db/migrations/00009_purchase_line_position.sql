-- +goose Up
-- Purchase lines and allocations displayed in entry order, not UUID order.
-- Before this, every read sorted by id (random UUID), so the edit form could
-- shuffle a user's lines between sessions — and shuffled again after each
-- line-replacement edit. position captures the owner's entry order.
ALTER TABLE purchase_lines ADD COLUMN position smallint NOT NULL DEFAULT 0;
ALTER TABLE purchase_allocations ADD COLUMN position smallint NOT NULL DEFAULT 0;

-- Backfill existing rows deterministically: the order users last saw was id
-- order, so preserve that rather than invent a new one.
WITH ranked AS (
  SELECT id, (ROW_NUMBER() OVER (PARTITION BY purchase_id ORDER BY id)) - 1 AS rn
  FROM purchase_lines
)
UPDATE purchase_lines pl SET position = ranked.rn FROM ranked WHERE pl.id = ranked.id;

WITH ranked AS (
  SELECT pa.id, (ROW_NUMBER() OVER (PARTITION BY pa.purchase_line_id ORDER BY pa.id)) - 1 AS rn
  FROM purchase_allocations pa
)
UPDATE purchase_allocations pa SET position = ranked.rn FROM ranked WHERE pa.id = ranked.id;

-- +goose Down
ALTER TABLE purchase_allocations DROP COLUMN position;
ALTER TABLE purchase_lines DROP COLUMN position;
