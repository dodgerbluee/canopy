// Purchase journal projection (T18): a purchase projects one entry per
// plant-bearing allocation, keyed by allocation id. Cards carry no money
// totals — workspace monetary aggregates come from the purchase rows,
// never summed from journal entries.
package journal

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// purchaseOccurredAt: an unknown purchase date is never invented. The
// entry orders by the record's creation instant and stays explicit —
// occurredAt equals createdAt and purchase.purchasedOn remains null.
func purchaseOccurredAt(p dbgen.Purchase) pgtype.Timestamptz {
	if p.PurchasedOn.Valid {
		d := p.PurchasedOn.Time
		t := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		return pgtype.Timestamptz{Time: t, Valid: true}
	}
	return p.CreatedAt
}

// PurchaseSummary names the seller when known; amounts live on the
// allocation object, never in the card.
func PurchaseSummary(p dbgen.Purchase) string {
	if p.Seller.Valid && strings.TrimSpace(p.Seller.String) != "" {
		return truncateRunes("Purchased from "+p.Seller.String, summaryMax)
	}
	return "Purchased"
}

// ProjectPurchaseEntries replaces the purchase's PURCHASE entries with
// one row per plant-bearing allocation. Call inside the purchase
// mutation's tx so entries and allocations never disagree.
func ProjectPurchaseEntries(ctx context.Context, q *dbgen.Queries, newID func() pgtype.UUID,
	p dbgen.Purchase) error {
	if err := q.DeletePurchaseJournalEntries(ctx, dbgen.DeletePurchaseJournalEntriesParams{
		WorkspaceID: p.WorkspaceID, PurchaseID: p.ID,
	}); err != nil {
		return err
	}
	allocs, err := q.ListPurchaseAllocationsForPurchase(ctx, dbgen.ListPurchaseAllocationsForPurchaseParams{
		PurchaseID: p.ID, WorkspaceID: p.WorkspaceID,
	})
	if err != nil {
		return err
	}
	occ := purchaseOccurredAt(p)
	sum := PurchaseSummary(p)
	for _, al := range allocs {
		if !al.PlantID.Valid {
			continue // unallocated supplies carry no plant card
		}
		if _, err := q.UpsertJournalEntry(ctx, dbgen.UpsertJournalEntryParams{
			ID: newID(), WorkspaceID: p.WorkspaceID, PlantID: al.PlantID,
			EntryType: dbgen.JournalEntryTypePURCHASE, SourceID: al.ID,
			OccurredAt: occ, Summary: sum,
		}); err != nil {
			return err
		}
	}
	return nil
}

// TombstonePurchaseEntries marks every PURCHASE entry of a voided
// purchase tombstoned (visible only with includeTombstoned).
func TombstonePurchaseEntries(ctx context.Context, q *dbgen.Queries, ws, purchaseID pgtype.UUID) error {
	_, err := q.TombstonePurchaseJournalEntries(ctx, dbgen.TombstonePurchaseJournalEntriesParams{
		WorkspaceID: ws, PurchaseID: purchaseID,
	})
	return err
}

// ---- read model: PURCHASE union-member hydration -----------------------

// AllocationsByIDs batch-loads the allocation rows behind a page's
// PURCHASE entries (one query per page).
func (s *Service) AllocationsByIDs(ctx context.Context, ws pgtype.UUID, ids []pgtype.UUID) (map[string]dbgen.PurchaseAllocation, error) {
	out := map[string]dbgen.PurchaseAllocation{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListPurchaseAllocationsByIDs(ctx, dbgen.ListPurchaseAllocationsByIDsParams{
		WorkspaceID: ws, Ids: ids,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[uuidString(r.ID)] = r
	}
	return out, nil
}
