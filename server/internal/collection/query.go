package collection

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// ErrInvalidCursor is returned when an `after` value is malformed or was
// issued for a different ordering; the transport maps it to a top-level
// VALIDATION error (matching the frozen errors.json fixture).
var ErrInvalidCursor = errors.New("invalid pagination cursor")

// Cursor order keys (wire format: base64url("<order>|<sortValue>|<uuid>")).
const (
	OrderNameAsc     = "nameAsc"
	OrderNameDesc    = "nameDesc"
	OrderAcquiredDes = "acquiredDesc"
	OrderCreatedDesc = "createdDesc"
	OrderLabelAsc    = "labelAsc"
)

const (
	defaultPageSize int32 = 25
	maxPageSize     int32 = 100
)

// PlantPage is one keyset page of plants.
type PlantPage struct {
	Rows      []dbgen.Plant
	EndCursor *string
	HasNext   bool
}

// TagPage is one keyset page of tags.
type TagPage struct {
	Rows      []dbgen.Tag
	EndCursor *string
	HasNext   bool
}

// PlantQuery carries the mapped GraphQL filter/order/pagination inputs.
// Statuses are plain strings ("ACTIVE" etc.); the SQL casts them to the
// enum array so pgx never needs a custom enum-array codec.
type PlantQuery struct {
	First      int32
	After      *string
	Statuses   []string
	Category   *dbgen.PlantCategory
	Query      *string
	LocationID *pgtype.UUID
	TagIDs     []pgtype.UUID
	Order      string
}

// TagQuery carries the mapped tag list inputs.
type TagQuery struct {
	First           int32
	After           *string
	Query           *string
	IncludeArchived bool
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

func encodeCursor(order, sortVal, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(order + "|" + sortVal + "|" + id))
}

// decodeCursor validates the order binding: a cursor issued for one
// ordering is invalid for another.
func decodeCursor(cur, order string) (string, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	s := string(raw)
	i := strings.IndexByte(s, '|')
	j := strings.LastIndexByte(s, '|')
	if i < 0 || j <= i || s[:i] != order {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	id, ok := parseUUID(s[j+1:])
	if !ok {
		return "", pgtype.UUID{}, ErrInvalidCursor
	}
	return s[i+1 : j], id, nil
}

// ListPlants runs the keyset page for the requested order.
func (s *Service) ListPlants(ctx context.Context, a Actor, qz PlantQuery) (PlantPage, error) {
	first := clampFirst(qz.First)
	limit := first + 1 // one extra row answers hasNextPage honestly

	base := func() (statuses []string, category dbgen.NullPlantCategory,
		query pgtype.Text, locID pgtype.UUID, tagIDs []pgtype.UUID) {
		statuses = qz.Statuses
		if qz.Category != nil {
			category = dbgen.NullPlantCategory{PlantCategory: *qz.Category, Valid: true}
		}
		if qz.Query != nil {
			query = pgtype.Text{String: *qz.Query, Valid: true}
		}
		if qz.LocationID != nil {
			locID = *qz.LocationID
		}
		tagIDs = qz.TagIDs
		return
	}

	var rows []dbgen.Plant
	var err error
	switch qz.Order {
	case OrderNameAsc:
		afterName, afterID := pgtype.Text{}, pgtype.UUID{}
		if qz.After != nil {
			v, id, derr := decodeCursor(*qz.After, OrderNameAsc)
			if derr != nil {
				return PlantPage{}, derr
			}
			afterName, afterID = pgtype.Text{String: v, Valid: true}, id
		}
		st, cat, qry, loc, tags := base()
		rows, err = s.q().ListPlantsNameAsc(ctx, dbgen.ListPlantsNameAscParams{
			WorkspaceID: a.WorkspaceID, Statuses: st, Category: cat, Query: qry,
			LocationID: loc, TagIds: tags, AfterName: afterName, AfterID: afterID, LimitCount: limit,
		})
	case OrderNameDesc:
		afterName, afterID := pgtype.Text{}, pgtype.UUID{}
		if qz.After != nil {
			v, id, derr := decodeCursor(*qz.After, OrderNameDesc)
			if derr != nil {
				return PlantPage{}, derr
			}
			afterName, afterID = pgtype.Text{String: v, Valid: true}, id
		}
		st, cat, qry, loc, tags := base()
		rows, err = s.q().ListPlantsNameDesc(ctx, dbgen.ListPlantsNameDescParams{
			WorkspaceID: a.WorkspaceID, Statuses: st, Category: cat, Query: qry,
			LocationID: loc, TagIds: tags, AfterName: afterName, AfterID: afterID, LimitCount: limit,
		})
	case OrderAcquiredDes:
		afterDate, afterID := pgtype.Date{}, pgtype.UUID{}
		if qz.After != nil {
			v, id, derr := decodeCursor(*qz.After, OrderAcquiredDes)
			if derr != nil {
				return PlantPage{}, derr
			}
			t, terr := time.Parse("2006-01-02", v)
			if terr != nil {
				return PlantPage{}, ErrInvalidCursor
			}
			afterDate, afterID = pgtype.Date{Time: t, Valid: true}, id
		}
		st, cat, qry, loc, tags := base()
		rows, err = s.q().ListPlantsAcquiredOnDesc(ctx, dbgen.ListPlantsAcquiredOnDescParams{
			WorkspaceID: a.WorkspaceID, Statuses: st, Category: cat, Query: qry,
			LocationID: loc, TagIds: tags, AfterAcquiredOn: afterDate, AfterID: afterID, LimitCount: limit,
		})
	case OrderCreatedDesc:
		afterTS, afterID := pgtype.Timestamptz{}, pgtype.UUID{}
		if qz.After != nil {
			v, id, derr := decodeCursor(*qz.After, OrderCreatedDesc)
			if derr != nil {
				return PlantPage{}, derr
			}
			t, terr := time.Parse(time.RFC3339Nano, v)
			if terr != nil {
				return PlantPage{}, ErrInvalidCursor
			}
			afterTS, afterID = pgtype.Timestamptz{Time: t, Valid: true}, id
		}
		st, cat, qry, loc, tags := base()
		rows, err = s.q().ListPlantsCreatedDesc(ctx, dbgen.ListPlantsCreatedDescParams{
			WorkspaceID: a.WorkspaceID, Statuses: st, Category: cat, Query: qry,
			LocationID: loc, TagIds: tags, AfterCreatedAt: afterTS, AfterID: afterID, LimitCount: limit,
		})
	default:
		return PlantPage{}, ErrInvalidCursor
	}
	if err != nil {
		return PlantPage{}, err
	}

	page := PlantPage{Rows: rows}
	if int32(len(rows)) > first {
		page.Rows = rows[:first]
		page.HasNext = true
		last := page.Rows[len(page.Rows)-1]
		c := encodeCursor(qz.Order, plantSortValue(qz.Order, last), uuidString(last.ID))
		page.EndCursor = &c
	}
	return page, nil
}

func plantSortValue(order string, p dbgen.Plant) string {
	switch order {
	case OrderAcquiredDes:
		if p.AcquiredOn.Valid {
			return p.AcquiredOn.Time.Format("2006-01-02")
		}
		return "0001-01-01"
	case OrderCreatedDesc:
		return p.CreatedAt.Time.Format(time.RFC3339Nano)
	default:
		return strings.ToLower(p.Name)
	}
}

// ListTags runs the label-ascending keyset page.
func (s *Service) ListTags(ctx context.Context, a Actor, qz TagQuery) (TagPage, error) {
	first := clampFirst(qz.First)
	afterLabel, afterID := pgtype.Text{}, pgtype.UUID{}
	if qz.After != nil {
		v, id, derr := decodeCursor(*qz.After, OrderLabelAsc)
		if derr != nil {
			return TagPage{}, derr
		}
		afterLabel, afterID = pgtype.Text{String: v, Valid: true}, id
	}
	query := pgtype.Text{}
	if qz.Query != nil {
		query = pgtype.Text{String: *qz.Query, Valid: true}
	}
	rows, err := s.q().ListTags(ctx, dbgen.ListTagsParams{
		WorkspaceID: a.WorkspaceID, IncludeArchived: qz.IncludeArchived, Query: query,
		AfterLabel: afterLabel, AfterID: afterID, LimitCount: first + 1,
	})
	if err != nil {
		return TagPage{}, err
	}
	page := TagPage{Rows: rows}
	if int32(len(rows)) > first {
		page.Rows = rows[:first]
		page.HasNext = true
		last := page.Rows[len(page.Rows)-1]
		c := encodeCursor(OrderLabelAsc, strings.ToLower(last.Label), uuidString(last.ID))
		page.EndCursor = &c
	}
	return page, nil
}

// ListLocations returns the full location set (the contract exposes a flat
// list; the client assembles the tree).
func (s *Service) ListLocations(ctx context.Context, a Actor) ([]dbgen.Location, error) {
	return s.q().ListLocations(ctx, a.WorkspaceID)
}

// GetPlant / GetLocation / GetTag are read passthroughs; pgx.ErrNoRows
// signals absence and callers translate it per contract.
func (s *Service) GetPlant(ctx context.Context, a Actor, id pgtype.UUID) (dbgen.Plant, error) {
	return s.q().GetPlant(ctx, dbgen.GetPlantParams{ID: id, WorkspaceID: a.WorkspaceID})
}

func (s *Service) GetLocation(ctx context.Context, a Actor, id pgtype.UUID) (dbgen.Location, error) {
	return s.q().GetLocation(ctx, dbgen.GetLocationParams{ID: id, WorkspaceID: a.WorkspaceID})
}

func (s *Service) GetTag(ctx context.Context, a Actor, id pgtype.UUID) (dbgen.Tag, error) {
	return s.q().GetTag(ctx, dbgen.GetTagParams{ID: id, WorkspaceID: a.WorkspaceID})
}

func (s *Service) GetAcquisitionByPlant(ctx context.Context, a Actor, plantID pgtype.UUID) (dbgen.Acquisition, error) {
	return s.q().GetAcquisitionByPlant(ctx, dbgen.GetAcquisitionByPlantParams{PlantID: plantID, WorkspaceID: a.WorkspaceID})
}

func (s *Service) GetCurrentContext(ctx context.Context, plantID pgtype.UUID) (dbgen.PlantContext, error) {
	return s.q().GetCurrentContext(ctx, plantID)
}

func (s *Service) ListTagsForPlant(ctx context.Context, plantID pgtype.UUID) ([]dbgen.Tag, error) {
	return s.q().ListTagsForPlant(ctx, plantID)
}

// PhotoCounts batch-counts live photos for a page of plants.
func (s *Service) PhotoCounts(ctx context.Context, a Actor, plantIDs []pgtype.UUID) (map[string]int32, error) {
	if len(plantIDs) == 0 {
		return map[string]int32{}, nil
	}
	rows, err := s.q().CountPhotosByPlant(ctx, dbgen.CountPhotosByPlantParams{
		WorkspaceID: a.WorkspaceID, PlantIds: plantIDs,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]int32, len(rows))
	for _, r := range rows {
		out[uuidString(r.PlantID)] = r.PhotoCount
	}
	return out, nil
}
