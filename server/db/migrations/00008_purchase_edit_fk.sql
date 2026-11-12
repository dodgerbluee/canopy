-- +goose Up
-- REPAIR-02: editing a purchase that RETAINS a linked allocation (the web
-- form always resends lines, so even a date-only edit replaces the line
-- tree) failed with "line replacement failed": updatePurchase deletes all
-- lines and re-inserts them with retained ids in one transaction, but the
-- acquisition -> purchase_allocations FK was ON DELETE RESTRICT, and
-- RESTRICT constraints are NEVER deferred — the transient delete aborted
-- even though the same id is re-inserted before COMMIT.
--
-- NO ACTION DEFERRABLE INITIALLY DEFERRED re-checks at COMMIT, after the
-- re-insert has landed. Genuine removals stay protected: replaceLines
-- refuses to drop a linked allocation without an explicit unlink (which
-- nulls the reference first), and any dangling reference that slipped
-- through would still abort the transaction at COMMIT.

ALTER TABLE acquisitions DROP CONSTRAINT acquisitions_initial_purchase_allocation_id_fkey;
ALTER TABLE acquisitions ADD CONSTRAINT acquisitions_initial_purchase_allocation_id_fkey
  FOREIGN KEY (initial_purchase_allocation_id) REFERENCES purchase_allocations(id)
  ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED;

-- +goose Down
ALTER TABLE acquisitions DROP CONSTRAINT acquisitions_initial_purchase_allocation_id_fkey;
ALTER TABLE acquisitions ADD CONSTRAINT acquisitions_initial_purchase_allocation_id_fkey
  FOREIGN KEY (initial_purchase_allocation_id) REFERENCES purchase_allocations(id)
  ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
