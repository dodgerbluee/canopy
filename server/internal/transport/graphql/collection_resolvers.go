// Collection resolvers: plants, locations, tags, context history and
// unknown-pricing acquisitions (T09). Payloads carry typed problems;
// transport-level errors are reserved for auth and malformed requests.
package graphql

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/99designs/gqlgen/graphql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/collection"
	"canopy/server/internal/media"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

// searchBound enforces the frozen "max 200 chars" bound on free-text
// search inputs (PlantFilter.query, TagFilter.query,
// PurchaseFilter.sellerQuery). Oversized search is a malformed request,
// so it surfaces as a top-level VALIDATION error, not a payload problem.
func searchBound(s *string, field string) error {
	if s != nil && utf8.RuneCountInString(*s) > 200 {
		return &gqlerror.Error{
			Message:    field + " exceeds 200 characters.",
			Extensions: map[string]any{"code": "VALIDATION"},
		}
	}
	return nil
}

func actor(p httpx.Principal) collection.Actor {
	return collection.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}

// ---- scalar mapping --------------------------------------------------------

func dateOut(d pgtype.Date) *scalars.Date {
	if !d.Valid {
		return nil
	}
	s := scalars.Date(d.Time.Format("2006-01-02"))
	return &s
}

func dtOut(t pgtype.Timestamptz) scalars.DateTime {
	if !t.Valid {
		return scalars.DateTime(time.Time{})
	}
	return scalars.DateTime(t.Time)
}

func dtPtrOut(t pgtype.Timestamptz) *scalars.DateTime {
	if !t.Valid {
		return nil
	}
	v := scalars.DateTime(t.Time)
	return &v
}

func decimalOut(n *pgtype.Numeric) (*scalars.Decimal, error) {
	if n == nil || !n.Valid {
		return nil, nil
	}
	s, err := numericString(*n)
	if err != nil {
		return nil, err
	}
	v := scalars.Decimal(s)
	return &v, nil
}

func decimalIn(s *scalars.Decimal) (*pgtype.Numeric, *gql.MutationProblem) {
	if s == nil {
		return nil, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(string(*s)); err != nil || !n.Valid {
		return nil, mutationProblem(gql.ProblemCodeValidation, "input", "malformed decimal")
	}
	return &n, nil
}

func dateIn(s *scalars.Date) (*time.Time, *gql.MutationProblem) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", string(*s))
	if err != nil {
		return nil, mutationProblem(gql.ProblemCodeValidation, "input", "malformed date")
	}
	return &t, nil
}

func mutationProblem(code gql.ProblemCode, field, msg string) *gql.MutationProblem {
	f := field
	return &gql.MutationProblem{Code: code, Field: &f, Message: msg}
}

func mapProblems(ps []collection.Problem) []*gql.MutationProblem {
	out := make([]*gql.MutationProblem, 0, len(ps))
	for _, p := range ps {
		mp := &gql.MutationProblem{
			Code:    gql.ProblemCode(p.Code),
			Message: p.Message,
		}
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

func invalidCursorError() error {
	return &gqlerror.Error{
		Message:    "Invalid pagination cursor.",
		Extensions: map[string]any{"code": "VALIDATION"},
	}
}

func invalidFilterError() error {
	return &gqlerror.Error{
		Message:    "Invalid filter value.",
		Extensions: map[string]any{"code": "VALIDATION"},
	}
}

// inputFieldPresent distinguishes "omitted" from "explicit null" for the
// fields whose frozen semantics depend on presence (the frozen
// UpdateLocation query always re-assigns parent_id and label). It inspects
// the inline object literal when the argument is written inline, and the
// raw JSON variables when the argument is a variable reference (JSON key
// presence is exactly the client's intent).
func inputFieldPresent(ctx context.Context, name string) bool {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || fc.Field.Arguments == nil {
		return false
	}
	for _, arg := range fc.Field.Arguments {
		if arg.Name != "input" || arg.Value == nil {
			continue
		}
		v := arg.Value
		switch v.Kind {
		case ast.ObjectValue:
			for _, child := range v.Children {
				if child.Name == name {
					return true
				}
			}
		case ast.Variable:
			op := graphql.GetOperationContext(ctx)
			if raw, ok := op.Variables[v.Raw].(map[string]any); ok {
				if _, present := raw[name]; present {
					return true
				}
			}
		}
	}
	return false
}

// ---- object mapping ---------------------------------------------------------

func toTag(t dbgen.Tag) *gql.Tag {
	return &gql.Tag{
		ID:         uuidString(t.ID),
		Label:      t.Label,
		ArchivedAt: dtPtrOut(t.ArchivedAt),
		Revision:   int(t.Revision),
		CreatedAt:  dtOut(t.CreatedAt),
		UpdatedAt:  dtOut(t.UpdatedAt),
	}
}

func toAcquisition(ctx context.Context, db *dbgen.Queries, ws pgtype.UUID, a dbgen.Acquisition) (*gql.Acquisition, error) {
	out := &gql.Acquisition{
		ID:           uuidString(a.ID),
		AcquiredOn:   dateOut(a.AcquiredOn),
		PricingState: gql.PricingState(a.PricingState),
		Revision:     int(a.Revision),
	}
	if a.Seller.Valid {
		s := a.Seller.String
		out.Seller = &s
	}
	if a.InitialPurchaseAllocationID.Valid {
		row, err := db.GetPurchaseAllocation(ctx, dbgen.GetPurchaseAllocationParams{
			ID: a.InitialPurchaseAllocationID, WorkspaceID: ws,
		})
		if err != nil {
			return nil, err
		}
		alloc, err := toAllocation(dbgen.PurchaseAllocation{
			ID: row.ID, WorkspaceID: row.WorkspaceID, PurchaseLineID: row.PurchaseLineID,
			PlantID: row.PlantID, LineMinor: row.LineMinor, TaxMinor: row.TaxMinor,
			ShippingMinor: row.ShippingMinor,
		})
		if err != nil {
			return nil, err
		}
		out.InitialPurchaseAllocation = alloc
	}
	return out, nil
}

// toAllocation maps one purchase allocation; amounts leave as canonical
// minor-unit strings (exact, never float-formatted).
func toAllocation(al dbgen.PurchaseAllocation) (*gql.PurchaseAllocation, error) {
	ln, err := numericString(al.LineMinor)
	if err != nil {
		return nil, err
	}
	tx, err := numericString(al.TaxMinor)
	if err != nil {
		return nil, err
	}
	sh, err := numericString(al.ShippingMinor)
	if err != nil {
		return nil, err
	}
	out := &gql.PurchaseAllocation{
		ID:             uuidString(al.ID),
		PurchaseLineID: uuidString(al.PurchaseLineID),
		LineMinor:      scalars.MinorUnits(ln),
		TaxMinor:       scalars.MinorUnits(tx),
		ShippingMinor:  scalars.MinorUnits(sh),
	}
	if al.PlantID.Valid {
		p := uuidString(al.PlantID)
		out.PlantID = &p
	}
	return out, nil
}

// locationChain resolves a location with its parent chain. The cache keeps
// a page of locations cheap; the visited set bounds pathological depth.
func (q queryResolver) locationChain(ctx context.Context, a collection.Actor, id pgtype.UUID,
	cache map[string]*gql.Location, visited map[string]bool,
) (*gql.Location, error) {
	key := uuidString(id)
	if loc, ok := cache[key]; ok {
		return loc, nil
	}
	if visited[key] {
		return nil, nil // defensive: cycles are rejected on write
	}
	visited[key] = true
	row, err := q.r.Coll.GetLocation(ctx, a, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, internalError(ctx, err)
	}
	loc, err := q.locationFromRow(ctx, a, row, cache, visited)
	if err != nil {
		return nil, err
	}
	cache[key] = loc
	return loc, nil
}

func (q queryResolver) locationFromRow(ctx context.Context, a collection.Actor, row dbgen.Location,
	cache map[string]*gql.Location, visited map[string]bool,
) (*gql.Location, error) {
	lat, err := decimalOut(row.Latitude)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	lng, err := decimalOut(row.Longitude)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	loc := &gql.Location{
		ID:        uuidString(row.ID),
		Name:      row.Name,
		Situation: gql.LocationSituation(row.Situation),
		Latitude:  lat,
		Longitude: lng,
		Revision:  int(row.Revision),
		CreatedAt: dtOut(row.CreatedAt),
		UpdatedAt: dtOut(row.UpdatedAt),
	}
	if row.Label.Valid {
		l := row.Label.String
		loc.Label = &l
	}
	if row.ParentID.Valid {
		parent, err := q.locationChain(ctx, a, row.ParentID, cache, visited)
		if err != nil {
			return nil, err
		}
		loc.Parent = parent
	}
	return loc, nil
}

// toPlant assembles the full contract object graph for one plant row:
// tags, current context (+ location chain), acquisition, photo count and
// cover photo (T15). photoCount is honest; cover is nil when the media
// service is unwired or no cover is set.
func (q queryResolver) toPlant(ctx context.Context, a collection.Actor, row dbgen.Plant, photoCount int, cover *gql.PlantPhoto) (*gql.Plant, error) {
	out := &gql.Plant{
		ID:         uuidString(row.ID),
		Name:       row.Name,
		Type:       gql.PlantType(row.Type),
		Category:   gql.PlantCategory(row.Category),
		AcquiredOn: dateOut(row.AcquiredOn),
		PlantedOn:  dateOut(row.PlantedOn),
		Status:     gql.PlantStatus(row.Status),
		Revision:   int(row.Revision),
		CreatedAt:  dtOut(row.CreatedAt),
		UpdatedAt:  dtOut(row.UpdatedAt),
		PhotoCount: photoCount,
		CoverPhoto: cover,
		Tags:       []*gql.Tag{},
	}
	if row.ScientificName.Valid {
		s := row.ScientificName.String
		out.ScientificName = &s
	}
	if row.Cultivar.Valid {
		s := row.Cultivar.String
		out.Cultivar = &s
	}
	if row.Rootstock.Valid {
		s := row.Rootstock.String
		out.Rootstock = &s
	}
	if row.Count.Valid {
		c := int(row.Count.Int32)
		out.Count = &c
	}
	age, err := decimalOut(row.EstimatedAgeYears)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out.EstimatedAgeYears = age

	tags, err := q.r.Coll.ListTagsForPlant(ctx, row.ID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	for _, t := range tags {
		out.Tags = append(out.Tags, toTag(t))
	}

	pc, err := q.r.Coll.GetCurrentContext(ctx, row.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, internalError(ctx, err)
	} else if err == nil {
		ctxOut, err := q.toContext(ctx, a, pc)
		if err != nil {
			return nil, err
		}
		out.CurrentContext = ctxOut
	}

	acq, err := q.r.Coll.GetAcquisitionByPlant(ctx, a, row.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, internalError(ctx, err)
	} else if err == nil {
		aOut, err := toAcquisition(ctx, q.r.DB, a.WorkspaceID, acq)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		out.Acquisition = aOut
	}
	return out, nil
}

func (q queryResolver) toContext(ctx context.Context, a collection.Actor, pc dbgen.PlantContext) (*gql.PlantContext, error) {
	out := &gql.PlantContext{
		ID:            uuidString(pc.ID),
		EffectiveFrom: dtOut(pc.EffectiveFrom),
		Placement:     gql.ContextPlacement(pc.Placement),
		CreatedAt:     dtOut(pc.CreatedAt),
	}
	vol, err := decimalOut(pc.PotVolumeLiters)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out.PotVolumeLiters = vol
	if pc.MediumNote.Valid {
		s := pc.MediumNote.String
		out.MediumNote = &s
	}
	if pc.LightNote.Valid {
		s := pc.LightNote.String
		out.LightNote = &s
	}
	if pc.LocationID.Valid {
		loc, err := q.locationChain(ctx, a, pc.LocationID, map[string]*gql.Location{}, map[string]bool{})
		if err != nil {
			return nil, err
		}
		out.Location = loc
	}
	return out, nil
}

// ---- queries -----------------------------------------------------------------

func (q queryResolver) Locations(ctx context.Context) ([]*gql.Location, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	rows, err := q.r.Coll.ListLocations(ctx, a)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	cache := map[string]*gql.Location{}
	out := make([]*gql.Location, 0, len(rows))
	for _, row := range rows {
		key := uuidString(row.ID)
		if _, ok := cache[key]; ok {
			continue
		}
		loc, err := q.locationFromRow(ctx, a, row, cache, map[string]bool{})
		if err != nil {
			return nil, err
		}
		cache[key] = loc
		out = append(out, loc)
	}
	return out, nil
}

func (q queryResolver) Tags(ctx context.Context, first *int, after *string, filter *gql.TagFilter) (*gql.TagConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	qz := collection.TagQuery{First: firstInt32(first), After: after}
	if filter != nil {
		if filter.Query != nil {
			if err := searchBound(filter.Query, "filter.query"); err != nil {
				return nil, err
			}
			qz.Query = filter.Query
		}
		if filter.IncludeArchived != nil {
			qz.IncludeArchived = *filter.IncludeArchived
		}
	}
	page, err := q.r.Coll.ListTags(ctx, actor(p), qz)
	if err != nil {
		if errors.Is(err, collection.ErrInvalidCursor) {
			return nil, invalidCursorError()
		}
		return nil, internalError(ctx, err)
	}
	items := make([]*gql.Tag, 0, len(page.Rows))
	for _, t := range page.Rows {
		items = append(items, toTag(t))
	}
	return &gql.TagConnection{
		Items:    items,
		PageInfo: &gql.PageInfo{EndCursor: page.EndCursor, HasNextPage: page.HasNext},
	}, nil
}

func firstInt32(first *int) int32 {
	if first == nil {
		return 0 // service applies the SDL default (25)
	}
	return int32(*first)
}

func (q queryResolver) Plants(ctx context.Context, first *int, after *string, filter *gql.PlantFilter, order *gql.PlantOrder) (*gql.PlantConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	qz := collection.PlantQuery{First: firstInt32(first), After: after, Order: collection.OrderNameAsc}
	if order != nil {
		switch *order {
		case gql.PlantOrderNameAsc:
			qz.Order = collection.OrderNameAsc
		case gql.PlantOrderNameDesc:
			qz.Order = collection.OrderNameDesc
		case gql.PlantOrderAcquiredOnDesc:
			qz.Order = collection.OrderAcquiredDes
		case gql.PlantOrderCreatedDesc:
			qz.Order = collection.OrderCreatedDesc
		}
	}
	if filter != nil {
		if filter.Query != nil {
			if err := searchBound(filter.Query, "filter.query"); err != nil {
				return nil, err
			}
			qz.Query = filter.Query
		}
		if filter.Category != nil {
			c := dbgen.PlantCategory(*filter.Category)
			qz.Category = &c
		}
		if len(filter.Status) > 0 {
			// Explicit statuses override the default ACTIVE+DEAD visibility.
			for _, s := range filter.Status {
				qz.Statuses = append(qz.Statuses, string(s))
			}
		} else {
			qz.Statuses = []string{"ACTIVE", "DEAD"}
		}
		if filter.LocationID != nil {
			u, ok := scanUUID(*filter.LocationID)
			if !ok {
				return nil, invalidFilterError()
			}
			qz.LocationID = &u
		}
		if len(filter.TagIds) > 0 {
			for _, s := range filter.TagIds {
				u, ok := scanUUID(s)
				if !ok {
					return nil, invalidFilterError()
				}
				qz.TagIDs = append(qz.TagIDs, u)
			}
		}
	} else {
		qz.Statuses = []string{"ACTIVE", "DEAD"}
	}
	page, err := q.r.Coll.ListPlants(ctx, actor(p), qz)
	if err != nil {
		if errors.Is(err, collection.ErrInvalidCursor) {
			return nil, invalidCursorError()
		}
		return nil, internalError(ctx, err)
	}
	ids := make([]pgtype.UUID, 0, len(page.Rows))
	for _, row := range page.Rows {
		ids = append(ids, row.ID)
	}
	counts, err := q.r.Coll.PhotoCounts(ctx, actor(p), ids)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	covers, err := q.covers(ctx, p.WorkspaceID, ids)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	items := make([]*gql.Plant, 0, len(page.Rows))
	for _, row := range page.Rows {
		pl, err := q.toPlant(ctx, actor(p), row, int(counts[uuidString(row.ID)]), coverFor(covers, row.ID))
		if err != nil {
			return nil, err
		}
		items = append(items, pl)
	}
	return &gql.PlantConnection{
		Items:    items,
		PageInfo: &gql.PageInfo{EndCursor: page.EndCursor, HasNextPage: page.HasNext},
	}, nil
}

func (q queryResolver) Plant(ctx context.Context, id string) (*gql.Plant, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	uid, ok := scanUUID(id)
	if !ok {
		return nil, nil // malformed id behaves like absent
	}
	row, err := q.r.Coll.GetPlant(ctx, actor(p), uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, internalError(ctx, err)
	}
	counts, err := q.r.Coll.PhotoCounts(ctx, actor(p), []pgtype.UUID{row.ID})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	covers, err := q.covers(ctx, p.WorkspaceID, []pgtype.UUID{row.ID})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out, err := q.toPlant(ctx, actor(p), row, int(counts[uuidString(row.ID)]), coverFor(covers, row.ID))
	if err != nil {
		return nil, err
	}
	// carePlan (T24): attached on the single-plant read only; list views
	// stay N+1-free and the field is nullable in the contract.
	if q.r.Care != nil {
		plan, err := q.r.DB.GetCarePlanByPlant(ctx, row.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, internalError(ctx, err)
		case plan.WorkspaceID == p.WorkspaceID:
			pv, probs, err := q.r.Care.PlanViewOf(ctx, careActor(p), plan)
			if err != nil {
				return nil, internalError(ctx, err)
			}
			if len(probs) == 0 {
				co, err := planOut(pv)
				if err != nil {
					return nil, internalError(ctx, err)
				}
				out.CarePlan = co
			}
		}
	}
	return out, nil
}

// covers batch-loads cover photos; nil map when the media service is
// unwired (capability-degraded deployments keep coverPhoto null).
func (q queryResolver) covers(ctx context.Context, ws pgtype.UUID, ids []pgtype.UUID) (map[string]media.PhotoView, error) {
	if q.r.Media == nil {
		return nil, nil
	}
	return q.r.Media.CoverPhotos(ctx, ws, ids)
}

func coverFor(covers map[string]media.PhotoView, id pgtype.UUID) *gql.PlantPhoto {
	if v, ok := covers[uuidString(id)]; ok {
		out := photoOut(v)
		return out
	}
	return nil
}

func scanUUID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}

// ---- mutations -----------------------------------------------------------------

// plantPayload re-reads the aggregate after commit so the payload always
// reflects committed state (also correct for idempotent replays).
func (m mutationResolver) plantPayload(ctx context.Context, a collection.Actor, id string) *gql.Plant {
	uid, ok := scanUUID(id)
	if !ok {
		return nil
	}
	row, err := m.r.Coll.GetPlant(ctx, a, uid)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			m.r.Log.Error("payload re-read", "err", err)
		}
		return nil
	}
	counts, err := m.r.Coll.PhotoCounts(ctx, a, []pgtype.UUID{row.ID})
	if err != nil {
		return nil
	}
	q := queryResolver{m.r}
	covers, err := q.covers(ctx, a.WorkspaceID, []pgtype.UUID{row.ID})
	if err != nil {
		return nil
	}
	pl, err := q.toPlant(ctx, a, row, int(counts[uuidString(row.ID)]), coverFor(covers, row.ID))
	if err != nil {
		return nil
	}
	return pl
}

func (m mutationResolver) locationPayload(ctx context.Context, a collection.Actor, id string) *gql.Location {
	uid, ok := scanUUID(id)
	if !ok {
		return nil
	}
	loc, err := (queryResolver{m.r}).locationChain(ctx, a, uid, map[string]*gql.Location{}, map[string]bool{})
	if err != nil {
		return nil
	}
	return loc
}

func (m mutationResolver) tagPayload(ctx context.Context, a collection.Actor, id string) *gql.Tag {
	uid, ok := scanUUID(id)
	if !ok {
		return nil
	}
	row, err := m.r.Coll.GetTag(ctx, a, uid)
	if err != nil {
		return nil
	}
	return toTag(row)
}

func (m mutationResolver) acquisitionPayload(ctx context.Context, a collection.Actor, id string) *gql.Acquisition {
	uid, ok := scanUUID(id)
	if !ok {
		return nil
	}
	row, err := m.r.DB.GetAcquisition(ctx, dbgen.GetAcquisitionParams{ID: uid, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return nil
	}
	out, err := toAcquisition(ctx, m.r.DB, a.WorkspaceID, row)
	if err != nil {
		return nil
	}
	return out
}

func (m mutationResolver) CreatePlant(ctx context.Context, in gql.CreatePlantInput, c *string) (*gql.CreatePlantPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	din := collection.CreatePlantInput{
		Name:           in.Name,
		ScientificName: in.ScientificName,
		Cultivar:       in.Cultivar,
		Rootstock:      in.Rootstock,
		Type:           string(in.Type),
		Category:       string(in.Category),
		TagIDs:         in.TagIds,
	}
	if in.Count != nil {
		cnt := int32(*in.Count)
		din.Count = &cnt
	}
	var probs []*gql.MutationProblem
	var prob *gql.MutationProblem
	plantedOn, prob := dateIn(in.PlantedOn)
	if prob != nil {
		probs = append(probs, prob)
	}
	din.PlantedOn = plantedOn
	if din.EstimatedAgeYears, prob = decimalIn(in.EstimatedAgeYears); prob != nil {
		probs = append(probs, prob)
	}
	if in.InitialPurchase != nil {
		ip, ipProbs := initialPurchaseIn(*in.InitialPurchase)
		if len(ipProbs) > 0 {
			probs = append(probs, ipProbs...)
		} else {
			din.InitialPurchase = ip
		}
	}
	if len(probs) > 0 {
		return problemPayload(c, probs), nil
	}
	if in.InitialContext != nil {
		ci, cprobs, err := contextInput(ctx, in.InitialContext)
		if err != nil {
			return nil, err
		} else if len(cprobs) > 0 {
			return problemPayload(c, cprobs), nil
		}
		din.InitialContext = ci
	}
	if in.Acquisition != nil {
		acq, aprobs, err := acquisitionInput(ctx, in.Acquisition)
		if err != nil {
			return nil, err
		} else if len(aprobs) > 0 {
			return problemPayload(c, aprobs), nil
		}
		din.Acquisition = acq
	}
	res, problems, err := m.r.Coll.CreatePlant(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return problemPayload(c, mapProblems(problems)), nil
	}
	return &gql.CreatePlantPayload{
		Plant:            m.plantPayload(ctx, a, res.ID),
		Problems:         []*gql.MutationProblem{},
		ClientMutationID: c,
	}, nil
}

// problemPayload builds a payload carrying only problems.
func problemPayload(cmid *string, probs []*gql.MutationProblem) *gql.CreatePlantPayload {
	return &gql.CreatePlantPayload{Problems: probs, ClientMutationID: cmid}
}

func contextInput(ctx context.Context, in *gql.PlantContextInput) (*collection.ContextInput, []*gql.MutationProblem, error) {
	out := &collection.ContextInput{
		LocationID: in.LocationID,
		Placement:  string(in.Placement),
		MediumNote: in.MediumNote,
		LightNote:  in.LightNote,
	}
	if in.EffectiveFrom != nil {
		t := time.Time(*in.EffectiveFrom)
		out.EffectiveFrom = &t
	}
	var prob *gql.MutationProblem
	if out.PotVolumeLiters, prob = decimalIn(in.PotVolumeLiters); prob != nil {
		return nil, []*gql.MutationProblem{prob}, nil
	}
	return out, nil, nil
}

func acquisitionInput(ctx context.Context, in *gql.AcquisitionInput) (*collection.AcquisitionInput, []*gql.MutationProblem, error) {
	out := &collection.AcquisitionInput{
		Seller:       in.Seller,
		PricingState: string(in.PricingState),
	}
	var prob *gql.MutationProblem
	if out.AcquiredOn, prob = dateIn(in.AcquiredOn); prob != nil {
		return nil, []*gql.MutationProblem{prob}, nil
	}
	return out, nil, nil
}

func (m mutationResolver) UpdatePlant(ctx context.Context, in gql.UpdatePlantInput, c *string) (*gql.UpdatePlantPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	din := collection.UpdatePlantInput{
		ID: in.ID, ExpectedRevision: int32(in.ExpectedRevision),
		Name: in.Name, ScientificName: in.ScientificName, Cultivar: in.Cultivar, Rootstock: in.Rootstock,
	}
	if in.Type != nil {
		t := string(*in.Type)
		din.Type = &t
	}
	if in.Category != nil {
		cat := string(*in.Category)
		din.Category = &cat
	}
	if in.Count != nil {
		cnt := int32(*in.Count)
		din.Count = &cnt
	}
	var probs []*gql.MutationProblem
	var prob *gql.MutationProblem
	plantedOn, prob := dateIn(in.PlantedOn)
	if prob != nil {
		probs = append(probs, prob)
	}
	din.PlantedOn = plantedOn
	if din.EstimatedAgeYears, prob = decimalIn(in.EstimatedAgeYears); prob != nil {
		probs = append(probs, prob)
	}
	if len(probs) > 0 {
		return &gql.UpdatePlantPayload{Problems: probs, ClientMutationID: c}, nil
	}
	res, problems, err := m.r.Coll.UpdatePlant(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.UpdatePlantPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.UpdatePlantPayload{Plant: m.plantPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) ArchivePlant(ctx context.Context, in gql.ArchivePlantInput, c *string) (*gql.ArchivePlantPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	res, problems, err := m.r.Coll.ArchivePlant(ctx, a, in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.ArchivePlantPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.ArchivePlantPayload{Plant: m.plantPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) RestorePlant(ctx context.Context, in gql.RestorePlantInput, c *string) (*gql.RestorePlantPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	res, problems, err := m.r.Coll.RestorePlant(ctx, a, in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.RestorePlantPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.RestorePlantPayload{Plant: m.plantPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) PurgePlant(ctx context.Context, in gql.PurgePlantInput, c *string) (*gql.PurgePlantPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	res, problems, err := m.r.Coll.PurgePlant(ctx, a, in.ID, int32(in.ExpectedRevision), string(in.Confirm), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.PurgePlantPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	id := res.PlantID
	return &gql.PurgePlantPayload{PurgedPlantID: &id, Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) SetPlantTags(ctx context.Context, in gql.SetPlantTagsInput, c *string) (*gql.SetPlantTagsPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	res, problems, err := m.r.Coll.SetPlantTags(ctx, a, collection.SetPlantTagsInput{
		PlantID: in.PlantID, ExpectedRevision: int32(in.ExpectedRevision), TagIDs: in.TagIds,
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.SetPlantTagsPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.SetPlantTagsPayload{Plant: m.plantPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) SetPlantContext(ctx context.Context, in gql.SetPlantContextInput, c *string) (*gql.SetPlantContextPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	din := collection.SetPlantContextInput{PlantID: in.PlantID, ExpectedRevision: int32(in.ExpectedRevision)}
	if in.Context != nil {
		ci, probs, err := contextInput(ctx, in.Context)
		if err != nil {
			return nil, err
		} else if len(probs) > 0 {
			return &gql.SetPlantContextPayload{Problems: probs, ClientMutationID: c}, nil
		}
		din.Context = ci
	}
	res, problems, err := m.r.Coll.SetPlantContext(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.SetPlantContextPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	var ctxOut *gql.PlantContext
	if pc, err := m.r.Coll.GetCurrentContext(ctx, mustUUID(res.ContextID)); err == nil {
		ctxOut, err = (queryResolver{m.r}).toContext(ctx, a, pc)
		if err != nil {
			return nil, err
		}
	}
	return &gql.SetPlantContextPayload{
		Plant: m.plantPayload(ctx, a, res.PlantID), Context: ctxOut,
		Problems: []*gql.MutationProblem{}, ClientMutationID: c,
	}, nil
}

func mustUUID(s string) pgtype.UUID {
	u, _ := scanUUID(s)
	return u
}

func (m mutationResolver) CreateLocation(ctx context.Context, in gql.CreateLocationInput, c *string) (*gql.CreateLocationPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	din := collection.CreateLocationInput{
		Name: in.Name, ParentLocation: in.ParentLocationID,
		Situation: string(in.Situation), Label: in.Label,
	}
	var probs []*gql.MutationProblem
	var prob *gql.MutationProblem
	if din.Latitude, prob = decimalIn(in.Latitude); prob != nil {
		probs = append(probs, prob)
	}
	if din.Longitude, prob = decimalIn(in.Longitude); prob != nil {
		probs = append(probs, prob)
	}
	if len(probs) > 0 {
		return &gql.CreateLocationPayload{Problems: probs, ClientMutationID: c}, nil
	}
	res, problems, err := m.r.Coll.CreateLocation(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.CreateLocationPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.CreateLocationPayload{Location: m.locationPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) UpdateLocation(ctx context.Context, in gql.UpdateLocationInput, c *string) (*gql.UpdateLocationPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	din := collection.UpdateLocationInput{
		ID: in.ID, ExpectedRevision: int32(in.ExpectedRevision),
		Name: in.Name, ParentLocation: in.ParentLocationID,
		ParentLocationPresent: inputFieldPresent(ctx, "parentLocationId"),
		Label:                 in.Label,
		LabelPresent:          inputFieldPresent(ctx, "label"),
	}
	if in.Situation != nil {
		s := string(*in.Situation)
		din.Situation = &s
	}
	var probs []*gql.MutationProblem
	var prob *gql.MutationProblem
	if din.Latitude, prob = decimalIn(in.Latitude); prob != nil {
		probs = append(probs, prob)
	}
	if din.Longitude, prob = decimalIn(in.Longitude); prob != nil {
		probs = append(probs, prob)
	}
	if len(probs) > 0 {
		return &gql.UpdateLocationPayload{Problems: probs, ClientMutationID: c}, nil
	}
	res, problems, err := m.r.Coll.UpdateLocation(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.UpdateLocationPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.UpdateLocationPayload{Location: m.locationPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) CreateTag(ctx context.Context, in gql.CreateTagInput, c *string) (*gql.CreateTagPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	res, problems, err := m.r.Coll.CreateTag(ctx, a, in.Label, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.CreateTagPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.CreateTagPayload{Tag: m.tagPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) UpdateTag(ctx context.Context, in gql.UpdateTagInput, c *string) (*gql.UpdateTagPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	res, problems, err := m.r.Coll.UpdateTag(ctx, a, in.ID, int32(in.ExpectedRevision), in.Label, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.UpdateTagPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.UpdateTagPayload{Tag: m.tagPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) ArchiveTag(ctx context.Context, in gql.ArchiveTagInput, c *string) (*gql.ArchiveTagPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	res, problems, err := m.r.Coll.ArchiveTag(ctx, a, in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.ArchiveTagPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.ArchiveTagPayload{Tag: m.tagPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) RecordAcquisition(ctx context.Context, in gql.RecordAcquisitionInput, c *string) (*gql.RecordAcquisitionPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	din := collection.AcquisitionInput{Seller: in.Seller, PricingState: string(in.PricingState)}
	acquiredOn, prob := dateIn(in.AcquiredOn)
	if prob != nil {
		return &gql.RecordAcquisitionPayload{Problems: []*gql.MutationProblem{prob}, ClientMutationID: c}, nil
	}
	din.AcquiredOn = acquiredOn
	pricing, prProbs := acquisitionPricing(in.PurchaseAllocationID, in.NewPurchase)
	if len(prProbs) > 0 {
		return &gql.RecordAcquisitionPayload{Problems: prProbs, ClientMutationID: c}, nil
	}
	res, problems, err := m.r.Coll.RecordAcquisition(ctx, a, din, in.PlantID, pricing, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.RecordAcquisitionPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	out := &gql.RecordAcquisitionPayload{
		Acquisition: m.acquisitionPayload(ctx, a, res.ID),
		Problems:    []*gql.MutationProblem{}, ClientMutationID: c,
	}
	if res.PurchaseID != "" {
		out.Purchase = m.purchasePayload(ctx, pActor(p), res.PurchaseID)
	}
	return out, nil
}

func (m mutationResolver) UpdateAcquisition(ctx context.Context, in gql.UpdateAcquisitionInput, c *string) (*gql.UpdateAcquisitionPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a := actor(p)
	din := collection.UpdateAcquisitionInput{ID: in.ID, ExpectedRevision: int32(in.ExpectedRevision), Seller: in.Seller}
	acquiredOn, prob := dateIn(in.AcquiredOn)
	if prob != nil {
		return &gql.UpdateAcquisitionPayload{Problems: []*gql.MutationProblem{prob}, ClientMutationID: c}, nil
	}
	din.AcquiredOn = acquiredOn
	res, problems, err := m.r.Coll.UpdateAcquisition(ctx, a, din, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.UpdateAcquisitionPayload{Problems: mapProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.UpdateAcquisitionPayload{Acquisition: m.acquisitionPayload(ctx, a, res.ID), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}
