package planning

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type pageCursor struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
	ID    string `json:"id"`
}

func normalizeQuery(in Query) (Query, error) {
	if in.First < 1 || in.First > 100 {
		return Query{}, ErrInvalidQuery
	}
	if in.Order == "" {
		in.Order = OrderNameAsc
	}
	if !contains(in.Order, OrderNameAsc, OrderUpdatedDesc) {
		return Query{}, ErrInvalidQuery
	}
	in.Search = strings.TrimSpace(in.Search)
	if utf8.RuneCountInString(in.Search) > 200 {
		return Query{}, ErrInvalidQuery
	}
	if in.States == nil {
		in.States = []string{"IDEA", "PLANNED", "IN_PROGRESS"}
	} else {
		unique := map[string]bool{}
		for _, state := range in.States {
			if !contains(state, "IDEA", "PLANNED", "IN_PROGRESS", "COMPLETED", "CANCELLED") {
				return Query{}, ErrInvalidQuery
			}
			unique[state] = true
		}
		in.States = make([]string, 0, len(unique))
		for state := range unique {
			in.States = append(in.States, state)
		}
	}
	sort.Strings(in.States)
	if in.Method != nil && !contains(*in.Method, "START_INDOORS", "DIRECT_SOW", "NURSERY_PLANT") {
		return Query{}, ErrInvalidQuery
	}
	if in.LocationID != nil {
		id, ok := parseID(*in.LocationID)
		if !ok {
			return Query{}, ErrInvalidQuery
		}
		value := idString(id)
		in.LocationID = &value
	}
	return in, nil
}
func cursorScope(a Actor, in Query) string {
	hash, _ := requestHash(struct {
		Version                  int
		Workspace, Order, Search string
		States                   []string
		Method, LocationID       *string
	}{1, idString(a.WorkspaceID), in.Order, in.Search, in.States, in.Method, in.LocationID})
	return hash
}
func encodeCursor(scope, key, id string) string {
	data, _ := json.Marshal(pageCursor{Scope: scope, Key: key, ID: id})
	return base64.RawURLEncoding.EncodeToString(data)
}
func decodeCursor(raw, scope, order string) (string, pgtype.UUID, error) {
	if len(raw) > 8192 {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	var cursor pageCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cursor); err != nil || cursor.Scope != scope {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	id, ok := parseID(cursor.ID)
	if !ok {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	if order == OrderUpdatedDesc {
		if _, err = time.Parse(time.RFC3339Nano, cursor.Key); err != nil {
			return "", pgtype.UUID{}, ErrInvalidCursor
		}
	} else if n := utf8.RuneCountInString(cursor.Key); n < 1 || n > 160 {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	return cursor.Key, id, nil
}
func (s *Service) List(ctx context.Context, a Actor, input Query) (Page, error) {
	in, err := normalizeQuery(input)
	if err != nil {
		return Page{}, err
	}
	scope := cursorScope(a, in)
	afterKey := ""
	afterID := pgtype.UUID{}
	if in.After != nil {
		afterKey, afterID, err = decodeCursor(*in.After, scope, in.Order)
		if err != nil {
			return Page{}, err
		}
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Page{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	method := dbgen.NullPlantingMethod{}
	if in.Method != nil {
		method = dbgen.NullPlantingMethod{PlantingMethod: dbgen.PlantingMethod(*in.Method), Valid: true}
	}
	loc := pgtype.UUID{}
	if in.LocationID != nil {
		loc, _ = parseID(*in.LocationID)
	}
	var rows []dbgen.PlantingPlan
	if in.Order == OrderNameAsc {
		rows, err = q.ListPlantingPlansNameAsc(ctx, dbgen.ListPlantingPlansNameAscParams{WorkspaceID: a.WorkspaceID, States: in.States, Method: method, LocationID: loc, Query: in.Search, AfterName: pgtype.Text{String: afterKey, Valid: in.After != nil}, AfterID: afterID, LimitCount: in.First + 1})
	} else {
		after := pgtype.Timestamptz{}
		if in.After != nil {
			v, _ := time.Parse(time.RFC3339Nano, afterKey)
			after = pgtype.Timestamptz{Time: v, Valid: true}
		}
		rows, err = q.ListPlantingPlansUpdatedDesc(ctx, dbgen.ListPlantingPlansUpdatedDescParams{WorkspaceID: a.WorkspaceID, States: in.States, Method: method, LocationID: loc, Query: in.Search, AfterUpdatedAt: after, AfterID: afterID, LimitCount: in.First + 1})
	}
	if err != nil {
		return Page{}, err
	}
	page := Page{HasNext: int32(len(rows)) > in.First}
	if page.HasNext {
		rows = rows[:in.First]
	}
	page.Plans, err = hydrate(ctx, q, a, rows)
	if err != nil {
		return Page{}, err
	}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		key := last.Name
		if in.Order == OrderUpdatedDesc {
			key = last.UpdatedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		value := encodeCursor(scope, key, idString(last.ID))
		page.EndCursor = &value
	}
	if err = tx.Commit(ctx); err != nil {
		return Page{}, err
	}
	return page, nil
}
func (s *Service) get(ctx context.Context, a Actor, id pgtype.UUID) (*Plan, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	row, err := q.GetPlantingPlan(ctx, dbgen.GetPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plans, err := hydrate(ctx, q, a, []dbgen.PlantingPlan{row})
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &plans[0], nil
}

// Four queries per page, rather than one query per plan/action/snapshot.
func hydrate(ctx context.Context, q *dbgen.Queries, a Actor, rows []dbgen.PlantingPlan) ([]Plan, error) {
	plans := make([]Plan, len(rows))
	if len(rows) == 0 {
		return plans, nil
	}
	ids := make([]pgtype.UUID, len(rows))
	index := map[pgtype.UUID]int{}
	for i, row := range rows {
		plans[i] = Plan{Row: row, Actions: []dbgen.PlantingAction{}}
		ids[i] = row.ID
		index[row.ID] = i
	}
	actions, err := q.ListPlantingActions(ctx, dbgen.ListPlantingActionsParams{WorkspaceID: a.WorkspaceID, PlanIds: ids})
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		i := index[action.PlanID]
		if len(plans[i].Actions) >= MaxActions {
			return nil, errors.New("stored plan exceeds action limit")
		}
		plans[i].Actions = append(plans[i].Actions, action)
	}
	snapshots, err := q.ListCurrentPlantingGuidance(ctx, dbgen.ListCurrentPlantingGuidanceParams{WorkspaceID: a.WorkspaceID, PlanIds: ids})
	if err != nil {
		return nil, err
	}
	for _, snapshot := range snapshots {
		i := index[snapshot.PlanID]
		if snapshot.Generation == plans[i].Row.GuidanceGeneration {
			plans[i].LatestGuidance = &snapshot
		}
		if snapshot.ID == plans[i].Row.AcceptedGuidanceID {
			plans[i].AcceptedGuidance = &snapshot
		}
	}
	links, err := q.ListPlantingFulfillments(ctx, dbgen.ListPlantingFulfillmentsParams{WorkspaceID: a.WorkspaceID, PlanIds: ids})
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		i := index[link.PlanID]
		plans[i].Fulfillment = &link
	}
	return plans, nil
}
