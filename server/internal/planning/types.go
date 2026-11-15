package planning

import (
	"context"
	"errors"
	"log/slog"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxActions = 100

// Read bounds are shared with the API complexity budget and future ingestion.
const MaxGuidanceFacts = 32
const MaxGuidanceWindows = 8
const MaxGuidanceConditions = 16
const MaxGuidanceAssumptions = 16
const OrderNameAsc = "NAME_ASC"
const OrderUpdatedDesc = "UPDATED_DESC"

var ErrInvalidQuery = errors.New("invalid planning query")
var ErrInvalidCursor = errors.New("invalid planning cursor")

type Actor struct{ WorkspaceID, OwnerID, SessionID pgtype.UUID }
type Problem struct {
	Code            string
	Field           string
	Message         string
	CurrentRevision *int32
}

func invalid(field, message string) []Problem {
	return []Problem{{Code: "VALIDATION", Field: field, Message: message}}
}

type Budget struct {
	MinorUnits string
	Currency   string
}
type Intent struct {
	Name                                          string
	ScientificName, Cultivar                      *string
	Category                                      string
	DesiredQuantity                               *int32
	Method, IntendedLocationID, IntendedPlacement *string
	TargetPeriod                                  *Period
	Budget                                        *Budget
	Notes                                         *string
}
type CreateInput struct {
	Intent
	State string
}
type UpdateInput struct {
	ID               string
	ExpectedRevision int32
	Intent
}
type StateInput struct {
	ID               string
	ExpectedRevision int32
	State            string
}
type RevisionInput struct {
	ID               string
	ExpectedRevision int32
}
type ActionInput struct {
	PlanID                   string
	ID                       string
	ExpectedRevision         int32
	Kind, Title, ScheduledOn string
	ReminderOn, Notes        *string
}
type DeleteActionInput struct {
	PlanID, ID       string
	ExpectedRevision int32
}

// Result is a minimal receipt reference. Responses hydrate its current aggregate,
// following the existing collection command protocol.
type Result struct {
	ID       string `json:"id"`
	Revision int32  `json:"revision"`
}

type Plan struct {
	Row                              dbgen.PlantingPlan
	Actions                          []dbgen.PlantingAction
	LatestGuidance, AcceptedGuidance *dbgen.PlantingGuidanceSnapshot
	Fulfillment                      *dbgen.PlantingFulfillment
}
type Query struct {
	First              int32
	After              *string
	States             []string // nil: active defaults; empty: no states
	Search             string
	Method, LocationID *string
	Order              string
}
type Page struct {
	Plans     []Plan
	EndCursor *string
	HasNext   bool
}

type Service struct {
	Pool     *pgxpool.Pool
	Log      *slog.Logger
	Clock    platform.Clock
	IDs      platform.IDs
	Guidance GuidanceProvider
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{Pool: pool, Log: log, Clock: platform.SystemClock{}, IDs: platform.UUIDv4{}}
}
func (s *Service) q() *dbgen.Queries { return dbgen.New(s.Pool) }
func parseID(raw string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	err := id.Scan(raw)
	return id, err == nil && id.Valid
}
func idString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return id.String()
}
func text(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}
func result(row dbgen.PlantingPlan) Result {
	return Result{ID: idString(row.ID), Revision: row.Revision}
}

// Reads of one aggregate use a single repeatable snapshot, including children.
func (s *Service) Get(ctx context.Context, a Actor, rawID string) (*Plan, error) {
	id, ok := parseID(rawID)
	if !ok {
		return nil, nil
	}
	return s.get(ctx, a, id)
}
