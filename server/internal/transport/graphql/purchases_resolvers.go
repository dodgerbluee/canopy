// Purchase / spending / acquisition-pricing resolvers (T11). Input
// mapping parses MinorUnits into exact ints here; nothing downstream
// ever sees a float.
package graphql

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/collection"
	"canopy/server/internal/money"
	"canopy/server/internal/purchases"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

func pActor(p httpx.Principal) purchases.Actor {
	return purchases.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}

// minorIn parses a MinorUnits literal into an exact int64 amount.
func minorIn(s *scalars.MinorUnits, field string) (int64, *gql.MutationProblem) {
	if s == nil {
		return 0, mutationProblem(gql.ProblemCodeValidation, field, "minorUnits is required")
	}
	v, err := money.Parse(string(*s))
	if err != nil {
		return 0, mutationProblem(gql.ProblemCodeValidation, field,
			"minorUnits must be a canonical non-negative integer string")
	}
	return v, nil
}

func minorReq(s scalars.MinorUnits, field string) (int64, *gql.MutationProblem) {
	return minorIn(&s, field)
}

func initialPurchaseIn(in gql.InitialPurchaseInput) (*collection.InitialPurchaseInput, []*gql.MutationProblem) {
	var probs []*gql.MutationProblem
	out := &collection.InitialPurchaseInput{Currency: in.Currency, Seller: in.Seller}
	var prob *gql.MutationProblem
	if out.Total, prob = minorReq(in.TotalMinor, "input.initialPurchase.totalMinor"); prob != nil {
		probs = append(probs, prob)
	}
	if out.Tax, prob = minorReq(in.TaxMinor, "input.initialPurchase.taxMinor"); prob != nil {
		probs = append(probs, prob)
	}
	if out.Shipping, prob = minorReq(in.ShippingMinor, "input.initialPurchase.shippingMinor"); prob != nil {
		probs = append(probs, prob)
	}
	purchasedOn, prob := dateIn(in.PurchasedOn)
	if prob != nil {
		probs = append(probs, prob)
	}
	out.PurchasedOn = purchasedOn
	if len(probs) > 0 {
		return nil, probs
	}
	return out, nil
}

func newPricedSpec(in gql.NewPricedPurchaseInput) (*purchases.NewPricedSpec, []*gql.MutationProblem) {
	var probs []*gql.MutationProblem
	out := &purchases.NewPricedSpec{
		Currency: in.Currency, Seller: in.Seller, LineDesc: in.LineDescription,
	}
	var prob *gql.MutationProblem
	if out.Total, prob = minorReq(in.TotalMinor, "input.newPurchase.totalMinor"); prob != nil {
		probs = append(probs, prob)
	}
	if out.Tax, prob = minorReq(in.TaxMinor, "input.newPurchase.taxMinor"); prob != nil {
		probs = append(probs, prob)
	}
	if out.Shipping, prob = minorReq(in.ShippingMinor, "input.newPurchase.shippingMinor"); prob != nil {
		probs = append(probs, prob)
	}
	purchasedOn, prob := dateIn(in.PurchasedOn)
	if prob != nil {
		probs = append(probs, prob)
	}
	out.PurchasedOn = purchasedOn
	if len(probs) > 0 {
		return nil, probs
	}
	return out, nil
}

// acquisitionPricing maps recordAcquisition's optional pricing inputs.
func acquisitionPricing(allocID *string, np *gql.NewPricedPurchaseInput) (*collection.AcquisitionPricing, []*gql.MutationProblem) {
	if allocID == nil && np == nil {
		return nil, nil
	}
	out := &collection.AcquisitionPricing{AllocationID: allocID}
	if np != nil {
		spec, probs := newPricedSpec(*np)
		if len(probs) > 0 {
			return nil, probs
		}
		out.NewPurchase = spec
	}
	return out, nil
}

func mapPurchaseProblems(ps []purchases.Problem) []*gql.MutationProblem {
	out := make([]*gql.MutationProblem, 0, len(ps))
	for _, p := range ps {
		mp := &gql.MutationProblem{Code: gql.ProblemCode(p.Code), Message: p.Message}
		if p.Field != "" {
			f := p.Field
			mp.Field = &f
		}
		if p.CurrentRevision != nil {
			rev := int(*p.CurrentRevision)
			mp.CurrentRevision = &rev
		}
		out = append(out, mp)
	}
	return out
}

// ---- object mapping --------------------------------------------------------

func toPurchase(p dbgen.Purchase) *gql.Purchase {
	out := &gql.Purchase{
		ID:        uuidString(p.ID),
		Currency:  p.Currency,
		Revision:  int(p.Revision),
		CreatedAt: dtOut(p.CreatedAt),
		UpdatedAt: dtOut(p.UpdatedAt),
	}
	if p.Seller.Valid {
		s := p.Seller.String
		out.Seller = &s
	}
	if p.PurchasedOn.Valid {
		d := dateOut(p.PurchasedOn)
		out.PurchasedOn = d
	}
	if t, err := numericString(p.TotalMinor); err == nil {
		out.TotalMinor = scalars.MinorUnits(t)
	}
	if t, err := numericString(p.ShippingMinor); err == nil {
		out.ShippingMinor = scalars.MinorUnits(t)
	}
	if t, err := numericString(p.TaxMinor); err == nil {
		out.TaxMinor = scalars.MinorUnits(t)
	}
	if p.VoidedAt.Valid {
		v := dtOut(p.VoidedAt)
		out.VoidedAt = &v
	}
	return out
}

func toPurchaseLine(l dbgen.PurchaseLine, allocs []*gql.PurchaseAllocation) (*gql.PurchaseLine, error) {
	qty, err := numericString(l.Quantity)
	if err != nil {
		return nil, err
	}
	lt, err := numericString(l.LineTotalMinor)
	if err != nil {
		return nil, err
	}
	return &gql.PurchaseLine{
		ID:             uuidString(l.ID),
		PurchaseID:     uuidString(l.PurchaseID),
		Description:    l.Description,
		Quantity:       scalars.Decimal(qty),
		LineTotalMinor: scalars.MinorUnits(lt),
		Allocations:    allocs,
	}, nil
}

func toAssetRow(a dbgen.Asset) *gql.Asset {
	out := &gql.Asset{
		ID:        uuidString(a.ID),
		Purpose:   gql.AssetPurpose(a.Purpose),
		State:     gql.AssetState(a.State),
		ByteCount: int(a.ByteCount),
		Sha256:    a.Sha256,
		CreatedAt: dtOut(a.CreatedAt),
		UpdatedAt: dtOut(a.UpdatedAt),
		Variants:  []*gql.MediaVariant{},
	}
	if a.MimeType.Valid {
		m := a.MimeType.String
		out.MimeType = &m
	}
	if a.DisplayName.Valid {
		d := a.DisplayName.String
		out.DisplayName = &d
	}
	if a.ErrorCode.Valid {
		e := a.ErrorCode.String
		out.ErrorCode = &e
	}
	if a.ExpiresAt.Valid {
		e := dtOut(a.ExpiresAt)
		out.ExpiresAt = &e
	}
	return out
}

// buildPurchase assembles the full purchase object (lines with their
// allocations, receipt attachments). Commands cap lines at 100, so the
// connection always fits the frozen first:100 request.
func (r *Resolver) buildPurchase(ctx context.Context, p dbgen.Purchase) (*gql.Purchase, error) {
	out := toPurchase(p)
	lines, err := r.Purch.ListLines(ctx, p.ID, 100, nil)
	if err != nil {
		return nil, err
	}
	ids := make([]pgtype.UUID, 0, len(lines.Rows))
	for _, l := range lines.Rows {
		ids = append(ids, l.ID)
	}
	allocs, err := r.Purch.AllocationsForLines(ctx, ids)
	if err != nil {
		return nil, err
	}
	byLine := map[string][]*gql.PurchaseAllocation{}
	for _, al := range allocs {
		g, err := toAllocation(al)
		if err != nil {
			return nil, err
		}
		k := uuidString(al.PurchaseLineID)
		byLine[k] = append(byLine[k], g)
	}
	items := make([]*gql.PurchaseLine, 0, len(lines.Rows))
	for _, l := range lines.Rows {
		al := byLine[uuidString(l.ID)]
		if al == nil {
			al = []*gql.PurchaseAllocation{}
		}
		g, err := toPurchaseLine(l, al)
		if err != nil {
			return nil, err
		}
		items = append(items, g)
	}
	hasNext := lines.HasNext
	out.Lines = &gql.PurchaseLineConnection{Items: items, PageInfo: &gql.PageInfo{
		EndCursor: lines.EndCursor, HasNextPage: hasNext,
	}}
	if r.Media != nil {
		// T15 path: receipt links with full asset views including the
		// deterministic derivatives produced by the processor
		rcpts, err := r.Media.ReceiptsForPurchases(ctx, p.WorkspaceID, []pgtype.UUID{p.ID})
		if err != nil {
			return nil, err
		}
		out.Receipts = []*gql.PurchaseReceipt{}
		for _, rv := range rcpts[uuidString(p.ID)] {
			out.Receipts = append(out.Receipts, receiptOut(rv))
		}
		return out, nil
	}
	receipts, err := r.Purch.ReceiptsForPurchase(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out.Receipts = []*gql.PurchaseReceipt{}
	for _, rc := range receipts {
		asset, err := r.Purch.GetAsset(ctx, p.WorkspaceID, rc.AssetID)
		if err != nil {
			continue // attachment pointing at a deleted asset never fabricates one
		}
		out.Receipts = append(out.Receipts, &gql.PurchaseReceipt{
			ID: uuidString(rc.ID), PurchaseID: uuidString(rc.PurchaseID), Asset: toAssetRow(asset),
		})
	}
	return out, nil
}

// purchasePayload re-reads the aggregate after a successful command so
// payloads always carry server truth.
func (m mutationResolver) purchasePayload(ctx context.Context, a purchases.Actor, id string) *gql.Purchase {
	uid, ok := scanUUID(id)
	if !ok {
		return nil
	}
	row, err := m.r.Purch.GetPurchase(ctx, a, uid)
	if err != nil {
		return nil
	}
	out, err := m.r.buildPurchase(ctx, row)
	if err != nil {
		return nil
	}
	return out
}

// ---- queries ----------------------------------------------------------------

func (q queryResolver) Purchases(ctx context.Context, first *int, after *string, filter *gql.PurchaseFilter) (*gql.PurchaseConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := pActor(p)
	qz := purchases.PurchaseQuery{}
	if first != nil {
		qz.First = int32(*first)
	}
	qz.After = after
	if filter != nil {
		if err := searchBound(filter.SellerQuery, "filter.sellerQuery"); err != nil {
			return nil, err
		}
		qz.SellerQuery = filter.SellerQuery
		if filter.From != nil {
			t, prob := dateIn(filter.From)
			if prob != nil {
				return nil, invalidFilterError()
			}
			qz.From = t
		}
		if filter.To != nil {
			t, prob := dateIn(filter.To)
			if prob != nil {
				return nil, invalidFilterError()
			}
			qz.To = t
		}
		qz.Currency = filter.Currency
		if filter.IncludeVoided != nil {
			qz.IncludeVoided = *filter.IncludeVoided
		}
	}
	page, err := q.r.Purch.ListPurchases(ctx, a, qz)
	if err != nil {
		if errors.Is(err, purchases.ErrInvalidCursor) {
			return nil, invalidCursorError()
		}
		return nil, internalError(ctx, err)
	}
	items := make([]*gql.Purchase, 0, len(page.Rows))
	for _, row := range page.Rows {
		g, err := q.r.buildPurchase(ctx, row)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		items = append(items, g)
	}
	return &gql.PurchaseConnection{
		Items:    items,
		PageInfo: &gql.PageInfo{EndCursor: page.EndCursor, HasNextPage: page.HasNext},
	}, nil
}

func (q queryResolver) Purchase(ctx context.Context, id string) (*gql.Purchase, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := pActor(p)
	uid, ok := scanUUID(id)
	if !ok {
		return nil, nil // malformed ids behave as absent (NOT_FOUND semantics)
	}
	row, err := q.r.Purch.GetPurchase(ctx, a, uid)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, internalError(ctx, err)
	}
	out, err := q.r.buildPurchase(ctx, row)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return out, nil
}

func (q queryResolver) Spending(ctx context.Context, filter *gql.SpendingFilter) (*gql.Spending, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := pActor(p)
	f := purchases.SpendingFilter{}
	if filter != nil {
		if filter.PlantID != nil {
			uid, ok := scanUUID(*filter.PlantID)
			if !ok {
				return nil, invalidFilterError()
			}
			f.PlantID = &uid
		}
		if filter.From != nil {
			t, prob := dateIn(filter.From)
			if prob != nil {
				return nil, invalidFilterError()
			}
			f.From = t
		}
		if filter.To != nil {
			t, prob := dateIn(filter.To)
			if prob != nil {
				return nil, invalidFilterError()
			}
			f.To = t
		}
	}
	res, err := q.r.Purch.Spending(ctx, a, f)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out := &gql.Spending{
		KnownSpendingByCurrency:       []*gql.CurrencyTotal{},
		UnknownAcquisitionCostCount:   int(res.UnknownCostCount),
		MissingAcquisitionRecordCount: int(res.MissingRecordCount),
	}
	for _, c := range res.ByCurrency {
		out.KnownSpendingByCurrency = append(out.KnownSpendingByCurrency, &gql.CurrencyTotal{
			Currency: c.Currency,
			Total:    &gql.Money{MinorUnits: scalars.MinorUnits(money.Format(c.Total)), Currency: c.Currency},
		})
	}
	return out, nil
}

// ---- mutations ----------------------------------------------------------------

func purchaseLineInputs(in []*gql.PurchaseLineInput) ([]purchases.LineInput, []*gql.MutationProblem) {
	var probs []*gql.MutationProblem
	out := make([]purchases.LineInput, 0, len(in))
	for i, l := range in {
		li := purchases.LineInput{ID: l.ID, Description: l.Description, Quantity: string(l.Quantity)}
		var prob *gql.MutationProblem
		lbase := "input.lines[" + strconv.Itoa(i) + "]"
		if li.LineTotal, prob = minorReq(l.LineTotalMinor, lbase+".lineTotalMinor"); prob != nil {
			probs = append(probs, prob)
		}
		for j, al := range l.Allocations {
			ai := purchases.AllocationInput{ID: al.ID, PlantID: al.PlantID}
			fbase := lbase + ".allocations[" + strconv.Itoa(j) + "]"
			if ai.Line, prob = minorReq(al.LineMinor, fbase+".lineMinor"); prob != nil {
				probs = append(probs, prob)
			}
			if ai.Tax, prob = minorReq(al.TaxMinor, fbase+".taxMinor"); prob != nil {
				probs = append(probs, prob)
			}
			if ai.Shipping, prob = minorReq(al.ShippingMinor, fbase+".shippingMinor"); prob != nil {
				probs = append(probs, prob)
			}
			li.Allocations = append(li.Allocations, ai)
		}
		out = append(out, li)
	}
	if len(probs) > 0 {
		return nil, probs
	}
	return out, nil
}

func (m mutationResolver) CreatePurchase(ctx context.Context, in gql.CreatePurchaseInput, c *string) (*gql.CreatePurchasePayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := pActor(p)
	din := purchases.CreatePurchaseInput{Seller: in.Seller, Currency: in.Currency}
	var probs []*gql.MutationProblem
	var prob *gql.MutationProblem
	if din.Total, prob = minorReq(in.TotalMinor, "input.totalMinor"); prob != nil {
		probs = append(probs, prob)
	}
	if din.Tax, prob = minorReq(in.TaxMinor, "input.taxMinor"); prob != nil {
		probs = append(probs, prob)
	}
	if din.Shipping, prob = minorReq(in.ShippingMinor, "input.shippingMinor"); prob != nil {
		probs = append(probs, prob)
	}
	purchasedOn, prob := dateIn(in.PurchasedOn)
	if prob != nil {
		probs = append(probs, prob)
	}
	din.PurchasedOn = purchasedOn
	lines, lprobs := purchaseLineInputs(in.Lines)
	if len(lprobs) > 0 {
		probs = append(probs, lprobs...)
	}
	din.Lines = lines
	if len(probs) > 0 {
		return &gql.CreatePurchasePayload{Problems: probs, ClientMutationID: c}, nil
	}
	res, problems, err := m.r.Purch.CreatePurchase(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.CreatePurchasePayload{Problems: mapPurchaseProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.CreatePurchasePayload{
		Purchase: m.purchasePayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c,
	}, nil
}

func (m mutationResolver) UpdatePurchase(ctx context.Context, in gql.UpdatePurchaseInput, c *string) (*gql.UpdatePurchasePayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := pActor(p)
	din := purchases.UpdatePurchaseInput{
		ID: in.ID, ExpectedRevision: int32(in.ExpectedRevision),
		Seller: in.Seller, Currency: in.Currency,
		UnlinkAcquisitionIDs: in.UnlinkAcquisitionIds,
	}
	var probs []*gql.MutationProblem
	var prob *gql.MutationProblem
	purchasedOn, prob := dateIn(in.PurchasedOn)
	if prob != nil {
		probs = append(probs, prob)
	}
	din.PurchasedOn = purchasedOn
	if in.TotalMinor != nil {
		v, pr := minorIn(in.TotalMinor, "input.totalMinor")
		if pr != nil {
			probs = append(probs, pr)
		} else {
			din.Total = &v
		}
	}
	if in.TaxMinor != nil {
		v, pr := minorIn(in.TaxMinor, "input.taxMinor")
		if pr != nil {
			probs = append(probs, pr)
		} else {
			din.Tax = &v
		}
	}
	if in.ShippingMinor != nil {
		v, pr := minorIn(in.ShippingMinor, "input.shippingMinor")
		if pr != nil {
			probs = append(probs, pr)
		} else {
			din.Shipping = &v
		}
	}
	if in.Lines != nil {
		lines, lprobs := purchaseLineInputs(in.Lines)
		if len(lprobs) > 0 {
			probs = append(probs, lprobs...)
		} else {
			din.Lines = lines
		}
	}
	if len(probs) > 0 {
		return &gql.UpdatePurchasePayload{Problems: probs, ClientMutationID: c}, nil
	}
	res, problems, err := m.r.Purch.UpdatePurchase(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.UpdatePurchasePayload{Problems: mapPurchaseProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.UpdatePurchasePayload{
		Purchase: m.purchasePayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c,
	}, nil
}

func (m mutationResolver) VoidPurchase(ctx context.Context, in gql.VoidPurchaseInput, c *string) (*gql.VoidPurchasePayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := pActor(p)
	res, problems, err := m.r.Purch.VoidPurchase(ctx, a, in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.VoidPurchasePayload{Problems: mapPurchaseProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.VoidPurchasePayload{
		Purchase: m.purchasePayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c,
	}, nil
}

func (m mutationResolver) PriceAcquisition(ctx context.Context, in gql.PriceAcquisitionInput, c *string) (*gql.PriceAcquisitionPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := pActor(p)
	din := purchases.PriceAcquisitionInput{
		AcquisitionID: in.AcquisitionID, ExpectedRevision: int32(in.ExpectedRevision),
		PurchaseAllocationID: in.PurchaseAllocationID,
	}
	if in.RevertToUnknown != nil {
		din.RevertToUnknown = *in.RevertToUnknown
	}
	if in.NewPurchase != nil {
		np, nprobs := newPricedSpec(*in.NewPurchase)
		if len(nprobs) > 0 {
			return &gql.PriceAcquisitionPayload{Problems: nprobs, ClientMutationID: c}, nil
		}
		din.NewPurchase = np
	}
	res, problems, err := m.r.Purch.PriceAcquisition(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.PriceAcquisitionPayload{Problems: mapPurchaseProblems(problems), ClientMutationID: c}, nil
	}
	out := &gql.PriceAcquisitionPayload{Problems: []*gql.MutationProblem{}, ClientMutationID: c}
	out.Acquisition = m.acquisitionPayload(ctx, collection.Actor{
		WorkspaceID: a.WorkspaceID, OwnerID: a.OwnerID, SessionID: a.SessionID,
	}, res.AcquisitionID)
	if res.PurchaseID != "" {
		out.Purchase = m.purchasePayload(ctx, a, res.PurchaseID)
	}
	return out, nil
}
