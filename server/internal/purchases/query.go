package purchases

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// ErrInvalidCursor maps to a top-level VALIDATION transport error,
// matching the frozen errors.json fixture.
var ErrInvalidCursor = errors.New("invalid pagination cursor")

const (
	orderPurchDesc          = "purchDesc"
	defaultPageSize   int32 = 25
	maxPageSize       int32 = 100
	defaultLinesLimit int32 = 100
)

// PurchaseQuery carries mapped GraphQL filter/pagination inputs.
type PurchaseQuery struct {
	First         int32
	After         *string
	SellerQuery   *string
	From, To      *time.Time
	Currency      *string
	IncludeVoided bool
}

// PurchasePage is one keyset page of purchases ordered by
// (purchased_on DESC NULLS LAST, id DESC).
type PurchasePage struct {
	Rows      []dbgen.Purchase
	EndCursor *string
	HasNext   bool
}

func clampFirst(first int32) int32 {
	if first <= 0 {
		return defaultPageSize
	}
	if first > maxPageSize {
		return maxPageSize
	}
	return first
}

// Cursor wire format: base64url("purchDesc|<yyyy-mm-dd or ”>|<uuid>").
// The empty date marks the NULL-date segment (sorted last).
func encodeCursor(sortVal, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(orderPurchDesc + "|" + sortVal + "|" + id))
}

func decodeCursor(cur string) (pgtype.Date, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return pgtype.Date{}, pgtype.UUID{}, ErrInvalidCursor
	}
	s := string(raw)
	i := strings.IndexByte(s, '|')
	j := strings.LastIndexByte(s, '|')
	if i < 0 || j <= i || s[:i] != orderPurchDesc {
		return pgtype.Date{}, pgtype.UUID{}, ErrInvalidCursor
	}
	id, ok := parseUUID(s[j+1:])
	if !ok {
		return pgtype.Date{}, pgtype.UUID{}, ErrInvalidCursor
	}
	v := s[i+1 : j]
	if v == "" {
		return pgtype.Date{}, id, nil
	}
	t, terr := time.Parse("2006-01-02", v)
	if terr != nil {
		return pgtype.Date{}, pgtype.UUID{}, ErrInvalidCursor
	}
	return pgtype.Date{Time: t, Valid: true}, id, nil
}

// ListPurchases runs the keyset page.
func (s *Service) ListPurchases(ctx context.Context, a Actor, qz PurchaseQuery) (PurchasePage, error) {
	first := clampFirst(qz.First)
	params := dbgen.ListPurchasesParams{
		WorkspaceID:   a.WorkspaceID,
		IncludeVoided: qz.IncludeVoided,
		LimitCount:    first + 1,
	}
	if qz.SellerQuery != nil {
		params.SellerQuery = pgtype.Text{String: *qz.SellerQuery, Valid: true}
	}
	if qz.From != nil {
		params.FromDate = pgtype.Date{Time: *qz.From, Valid: true}
	}
	if qz.To != nil {
		params.ToDate = pgtype.Date{Time: *qz.To, Valid: true}
	}
	if qz.Currency != nil {
		params.Currency = pgtype.Text{String: *qz.Currency, Valid: true}
	}
	if qz.After != nil {
		afterDate, afterID, err := decodeCursor(*qz.After)
		if err != nil {
			return PurchasePage{}, err
		}
		params.AfterPurchasedOn, params.AfterID = afterDate, afterID
	}
	rows, err := s.q().ListPurchases(ctx, params)
	if err != nil {
		return PurchasePage{}, err
	}
	page := PurchasePage{Rows: rows}
	if int32(len(rows)) > first {
		page.Rows = rows[:first]
		page.HasNext = true
		last := page.Rows[len(page.Rows)-1]
		sortVal := ""
		if last.PurchasedOn.Valid {
			sortVal = last.PurchasedOn.Time.Format("2006-01-02")
		}
		c := encodeCursor(sortVal, uuidString(last.ID))
		page.EndCursor = &c
	}
	return page, nil
}

// GetPurchase is a read passthrough; pgx.ErrNoRows signals absence.
func (s *Service) GetPurchase(ctx context.Context, a Actor, id pgtype.UUID) (dbgen.Purchase, error) {
	return s.q().GetPurchase(ctx, dbgen.GetPurchaseParams{ID: id, WorkspaceID: a.WorkspaceID})
}

// LinePage is one keyset page of purchase lines (ordered by entry position).
type LinePage struct {
	Rows      []dbgen.PurchaseLine
	EndCursor *string
	HasNext   bool
}

// ListLines pages the line set of a purchase. The cursor carries the
// (position, id) keyset pair so paging follows the owner's entry order.
func (s *Service) ListLines(ctx context.Context, purchaseID pgtype.UUID, first int32, after *string) (LinePage, error) {
	if first <= 0 || first > maxPageSize {
		first = defaultLinesLimit
		if first > maxPageSize {
			first = maxPageSize
		}
	}
	params := dbgen.ListPurchaseLinesPageParams{
		PurchaseID: purchaseID,
		LimitCount: first + 1,
	}
	if after != nil {
		raw, err := base64.RawURLEncoding.DecodeString(*after)
		if err != nil {
			return LinePage{}, ErrInvalidCursor
		}
		posStr, idStr, ok := strings.Cut(string(raw), ":")
		if !ok {
			return LinePage{}, ErrInvalidCursor
		}
		pos, err := strconv.ParseInt(posStr, 10, 16)
		if err != nil {
			return LinePage{}, ErrInvalidCursor
		}
		id, ok := parseUUID(idStr)
		if !ok {
			return LinePage{}, ErrInvalidCursor
		}
		params.AfterID = id
		params.AfterPosition = pgtype.Int2{Int16: int16(pos), Valid: true}
	}
	rows, err := s.q().ListPurchaseLinesPage(ctx, params)
	if err != nil {
		return LinePage{}, err
	}
	page := LinePage{Rows: rows}
	if int32(len(rows)) > first {
		page.Rows = rows[:first]
		page.HasNext = true
		last := page.Rows[len(page.Rows)-1]
		c := base64.RawURLEncoding.EncodeToString(
			[]byte(fmt.Sprintf("%d:%s", last.Position, uuidString(last.ID))),
		)
		page.EndCursor = &c
	}
	return page, nil
}

// AllocationsForLines batch-loads allocations for a page of lines.
func (s *Service) AllocationsForLines(ctx context.Context, lineIDs []pgtype.UUID) ([]dbgen.PurchaseAllocation, error) {
	if len(lineIDs) == 0 {
		return nil, nil
	}
	return s.q().ListAllocationsForLines(ctx, lineIDs)
}

// ReceiptsForPurchase lists receipt attachments (populated from T15).
func (s *Service) ReceiptsForPurchase(ctx context.Context, purchaseID pgtype.UUID) ([]dbgen.PurchaseReceipt, error) {
	return s.q().ListReceiptsByPurchase(ctx, purchaseID)
}

// GetAsset is exposed for receipt asset resolution (T15 owns the media
// surface; the purchase read model only joins the row).
func (s *Service) GetAsset(ctx context.Context, ws, id pgtype.UUID) (dbgen.Asset, error) {
	return s.q().GetAsset(ctx, dbgen.GetAssetParams{ID: id, WorkspaceID: ws})
}

// CurrencyTotal is one currency bucket of known spending.
type CurrencyTotal struct {
	Currency string
	Total    int64
}

// SpendingResult mirrors the Spending payload type.
type SpendingResult struct {
	ByCurrency         []CurrencyTotal
	UnknownCostCount   int32
	MissingRecordCount int32
}

// SpendingFilter narrows the aggregate to a plant and/or date window.
type SpendingFilter struct {
	PlantID  *pgtype.UUID
	From, To *time.Time
}

// Spending computes known per-currency totals (voided excluded), plus
// the separate unknown-cost and missing-record counts. Unknown costs are
// never silently summed as zero.
func (s *Service) Spending(ctx context.Context, a Actor, f SpendingFilter) (SpendingResult, error) {
	out := SpendingResult{}
	if f.PlantID != nil {
		// Plant-scoped totals are allocation sums: a plant pays its share
		// (line + tax + shipping), never the whole shared purchase.
		plParams := dbgen.PlantLifetimeSpendingByCurrencyParams{
			WorkspaceID: a.WorkspaceID, PlantID: *f.PlantID,
		}
		if f.From != nil {
			plParams.FromDate = pgtype.Date{Time: *f.From, Valid: true}
		}
		if f.To != nil {
			plParams.ToDate = pgtype.Date{Time: *f.To, Valid: true}
		}
		rows, err := s.q().PlantLifetimeSpendingByCurrency(ctx, plParams)
		if err != nil {
			return SpendingResult{}, err
		}
		for _, r := range rows {
			v, ok := numericMinor(r.TotalMinor)
			if !ok {
				continue // defensive: aggregates stay integral
			}
			out.ByCurrency = append(out.ByCurrency, CurrencyTotal{Currency: r.Currency, Total: v})
		}
	} else {
		params := dbgen.SpendingByCurrencyParams{WorkspaceID: a.WorkspaceID}
		if f.From != nil {
			params.FromDate = pgtype.Date{Time: *f.From, Valid: true}
		}
		if f.To != nil {
			params.ToDate = pgtype.Date{Time: *f.To, Valid: true}
		}
		rows, err := s.q().SpendingByCurrency(ctx, params)
		if err != nil {
			return SpendingResult{}, err
		}
		for _, r := range rows {
			v, ok := numericMinor(r.TotalMinor)
			if !ok {
				continue // defensive: aggregates stay integral
			}
			out.ByCurrency = append(out.ByCurrency, CurrencyTotal{Currency: r.Currency, Total: v})
		}
	}
	unknown, err := s.q().UnknownAcquisitionCostCount(ctx, a.WorkspaceID)
	if err != nil {
		return SpendingResult{}, err
	}
	missing, err := s.q().MissingAcquisitionRecordCount(ctx, a.WorkspaceID)
	if err != nil {
		return SpendingResult{}, err
	}
	out.UnknownCostCount, out.MissingRecordCount = int32(unknown), int32(missing)
	return out, nil
}
