// Package graphql serves the frozen contract SDL (T01.r1) over HTTP:
// a gqlgen handler with strict scalars, query-cost limits and the
// session / workspace-settings resolvers. Everything not yet owned by a
// shipped task answers honestly with CAPABILITY_UNAVAILABLE — never
// fake data, never a silent zero value.
package graphql

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/aiconnection"
	"canopy/server/internal/care"
	"canopy/server/internal/collection"
	"canopy/server/internal/insights"
	"canopy/server/internal/journal"
	"canopy/server/internal/knowledge"
	"canopy/server/internal/media"
	"canopy/server/internal/portability"
	"canopy/server/internal/purchases"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

// Resolver is the hand-written ResolverRoot. The generated stub file
// (resolvers_gen.go) is scaffolding and is never hand-edited.
type Resolver struct {
	DB      *dbgen.Queries
	Coll    *collection.Service
	Purch   *purchases.Service
	Media   *media.Service
	Journal *journal.Service
	Care    *care.Service
	Port    *portability.Service
	// Insights is the AI analysis lane (T29). It is wired only when the
	// operator config warrants it (provider + queue); nil means the
	// analysis surfaces answer honestly, never fake data.
	AgentAccess   *agentaccess.Manager
	AgentEnabled  bool
	AgentEndpoint string
	AIConnections *aiconnection.Manager
	Knowledge     *knowledge.Service
	Insights      *insights.Service
	Log           *slog.Logger
}

func NewResolver(db *dbgen.Queries, log *slog.Logger) *Resolver {
	return &Resolver{DB: db, Log: log}
}

// NewResolverFromPool keeps the db/generated import boundary intact: cmd/*
// hands over only the pool (import rules, AGENTS.md), the queries type is
// constructed inside internal. The care service is the collection hook so
// plant archive/restore/context edits drive the care plan in the same tx.
func NewResolverFromPool(pool *pgxpool.Pool, log *slog.Logger) *Resolver {
	careSvc := care.New(pool, log)
	return &Resolver{
		DB:        dbgen.New(pool),
		Knowledge: knowledge.New(pool),
		Coll:      collection.New(pool, log, care.CollectionHook{Svc: careSvc}),
		Purch:     purchases.New(pool, log),
		Journal:   journal.New(pool),
		Care:      careSvc,
		Log:       log,
	}
}

func (r *Resolver) Query() gql.QueryResolver       { return queryResolver{r} }
func (r *Resolver) Mutation() gql.MutationResolver { return mutationResolver{r} }

type queryResolver struct{ r *Resolver }
type mutationResolver struct{ r *Resolver }

// ---- shared helpers -------------------------------------------------------

func principal(ctx context.Context) (httpx.Principal, error) {
	p, ok := httpx.PrincipalFrom(ctx)
	if !ok {
		// Defense in depth: the endpoint sits behind RequirePrincipal, so
		// this only fires on wiring mistakes.
		return httpx.Principal{}, &gqlerror.Error{
			Message:    "Not authenticated.",
			Extensions: map[string]any{"code": "UNAUTHENTICATED"},
		}
	}
	return p, nil
}

func capabilityUnavailable(field string) error {
	return &gqlerror.Error{
		Message:    fmt.Sprintf("%s is not available in this release.", field),
		Extensions: map[string]any{"code": "CAPABILITY_UNAVAILABLE"},
	}
}

func internalError(ctx context.Context, err error) error {
	var path ast.Path
	if fc := graphql.GetFieldContext(ctx); fc != nil {
		path = fc.Path()
	}
	return &gqlerror.Error{
		Message:    "Internal error.",
		Path:       path,
		Extensions: map[string]any{"code": "INTERNAL"},
	}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	h := hex.EncodeToString(u.Bytes[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// numericString renders a finite pgtype.Numeric as a canonical decimal
// string (no exponent, no trailing zeros) so it satisfies the Decimal
// scalar on the way out.
func numericString(n pgtype.Numeric) (string, error) {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return "", errors.New("not a finite numeric")
	}
	neg := n.Int.Sign() < 0
	digits := new(big.Int).Abs(n.Int).String()
	switch {
	case n.Exp > 0:
		digits += strings.Repeat("0", int(n.Exp))
	case n.Exp < 0:
		shift := int(-n.Exp)
		if len(digits) <= shift {
			digits = strings.Repeat("0", shift-len(digits)+1) + digits
		}
		cut := len(digits) - shift
		frac := strings.TrimRight(digits[cut:], "0")
		if frac == "" {
			digits = digits[:cut]
		} else {
			digits = digits[:cut] + "." + frac
		}
	}
	if neg && digits != "0" {
		digits = "-" + digits
	}
	return digits, nil
}

func (r *Resolver) toWorkspace(w dbgen.Workspace) *gql.Workspace {
	out := &gql.Workspace{
		ID:               uuidString(w.ID),
		DisplayName:      w.DisplayName,
		Timezone:         w.Timezone,
		DefaultCurrency:  w.DefaultCurrency,
		SettingsRevision: int(w.SettingsRevision),
	}
	if w.Latitude != nil && w.Longitude != nil {
		lat, err1 := numericString(*w.Latitude)
		lng, err2 := numericString(*w.Longitude)
		if err1 != nil || err2 != nil {
			// Stored coordinates must be finite; if they are not, say so
			// in the log and omit location rather than emit garbage.
			r.Log.Error("workspace coordinates not finite", "workspace_id", uuidString(w.ID))
			return out
		}
		loc := &gql.WorkspaceLocation{
			Latitude:  scalars.Decimal(lat),
			Longitude: scalars.Decimal(lng),
		}
		if w.LocationLabel.Valid {
			l := w.LocationLabel.String
			loc.Label = &l
		}
		out.Location = loc
	}
	return out
}

// ---- Query resolvers ------------------------------------------------------

func (q queryResolver) Session(ctx context.Context) (*gql.Session, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := q.r.DB.GetWorkspace(ctx, p.OwnerID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return &gql.Session{Authenticated: true, Workspace: q.r.toWorkspace(ws)}, nil
}

func (q queryResolver) Workspace(ctx context.Context) (*gql.Workspace, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := q.r.DB.GetWorkspace(ctx, p.OwnerID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return q.r.toWorkspace(ws), nil
}

// Locations / Tags / Plants / Plant are implemented in collection_resolvers.go.
// Journal is implemented in journal_resolvers.go (T16 photo series).

// Purchases / Purchase / Spending are implemented in purchases_resolvers.go.
// PendingUploads is implemented in media_resolvers.go.

// ExportJob is implemented in export_resolvers.go (T38.a).

// ---- Mutation resolvers ---------------------------------------------------

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func problem(code gql.ProblemCode, field, msg string) *gql.MutationProblem {
	f := field
	return &gql.MutationProblem{Code: code, Field: &f, Message: msg}
}

func decimalInRange(d scalars.Decimal, min, max int64) bool {
	r, err := d.Big()
	if err != nil {
		return false
	}
	lo := new(big.Rat).SetInt64(min)
	hi := new(big.Rat).SetInt64(max)
	return r.Cmp(lo) >= 0 && r.Cmp(hi) <= 0
}

func (m mutationResolver) UpdateWorkspace(ctx context.Context, in gql.UpdateWorkspaceInput, cmid *string) (*gql.UpdateWorkspacePayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := m.r.DB.GetWorkspace(ctx, p.OwnerID)
	if err != nil {
		return nil, internalError(ctx, err)
	}

	var problems []*gql.MutationProblem
	add := func(field, msg string) {
		problems = append(problems, problem(gql.ProblemCodeValidation, field, msg))
	}

	if in.ExpectedRevision < 1 {
		add("input.expectedRevision", "expectedRevision must be >= 1.")
	}
	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if name == "" {
			add("input.displayName", "displayName must not be blank.")
		} else if len(name) > 200 {
			add("input.displayName", "displayName must be at most 200 characters.")
		}
	}
	if in.Timezone != nil {
		if _, err := time.LoadLocation(*in.Timezone); err != nil {
			add("input.timezone", "timezone must be a valid IANA timezone identifier.")
		}
	}
	if in.DefaultCurrency != nil && !currencyRe.MatchString(*in.DefaultCurrency) {
		add("input.defaultCurrency", "defaultCurrency must be a 3-letter ISO 4217 code.")
	}
	if in.Location != nil {
		if !decimalInRange(in.Location.Latitude, -90, 90) {
			add("input.location.latitude", "latitude must be between -90 and 90.")
		}
		if !decimalInRange(in.Location.Longitude, -180, 180) {
			add("input.location.longitude", "longitude must be between -180 and 180.")
		}
		if in.Location.Label != nil && len(*in.Location.Label) > 200 {
			add("input.location.label", "label must be at most 200 characters.")
		}
	}
	if len(problems) > 0 {
		return &gql.UpdateWorkspacePayload{Problems: problems, ClientMutationID: cmid}, nil
	}

	params := dbgen.UpdateWorkspaceSettingsParams{
		ID:               cur.ID,
		OwnerID:          p.OwnerID,
		ExpectedRevision: int32(in.ExpectedRevision),
	}
	if in.DisplayName != nil {
		params.DisplayName = pgtype.Text{String: strings.TrimSpace(*in.DisplayName), Valid: true}
	}
	if in.Timezone != nil {
		params.Timezone = pgtype.Text{String: *in.Timezone, Valid: true}
	}
	if in.DefaultCurrency != nil {
		params.DefaultCurrency = pgtype.Text{String: *in.DefaultCurrency, Valid: true}
	}
	if in.Location != nil {
		var lat, lng pgtype.Numeric
		if err := lat.Scan(string(in.Location.Latitude)); err != nil {
			add("input.location.latitude", "latitude is not a valid numeric.")
			return &gql.UpdateWorkspacePayload{Problems: problems, ClientMutationID: cmid}, nil
		}
		if err := lng.Scan(string(in.Location.Longitude)); err != nil {
			add("input.location.longitude", "longitude is not a valid numeric.")
			return &gql.UpdateWorkspacePayload{Problems: problems, ClientMutationID: cmid}, nil
		}
		params.Latitude = &lat
		params.Longitude = &lng
		if in.Location.Label != nil {
			params.LocationLabel = pgtype.Text{String: *in.Location.Label, Valid: true}
		} else {
			params.LocationLabel = pgtype.Text{Valid: false}
		}
	} else {
		// The query always assigns location_label; pass the current value
		// through so an omitted location input leaves it untouched.
		params.LocationLabel = pgtype.Text{String: cur.LocationLabel.String, Valid: cur.LocationLabel.Valid}
	}

	updated, err := m.r.DB.UpdateWorkspaceSettings(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		// Revision guard failed: nothing changed (single guarded UPDATE).
		rev := int(cur.SettingsRevision)
		conflict := &gql.MutationProblem{
			Code:            gql.ProblemCodeConflict,
			Field:           strPtr("input.expectedRevision"),
			Message:         "Workspace settings were modified by another request.",
			CurrentRevision: &rev,
		}
		return &gql.UpdateWorkspacePayload{Problems: []*gql.MutationProblem{conflict}, ClientMutationID: cmid}, nil
	}
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return &gql.UpdateWorkspacePayload{
		Workspace:        m.r.toWorkspace(updated),
		Problems:         []*gql.MutationProblem{},
		ClientMutationID: cmid,
	}, nil
}

func strPtr(s string) *string { return &s }

// Collection mutations (plants/locations/tags/context/acquisition) are
// implemented in collection_resolvers.go.
// RecordActivity / UpdateActivity / VoidActivity and RecordMeasurement /
// UpdateMeasurement / DeleteMeasurement are implemented in
// journal_mutations.go (T17).

// PriceAcquisition / CreatePurchase / UpdatePurchase / VoidPurchase are
// implemented in purchases_resolvers.go. ReserveUpload / FinalizeUpload
// and the photo/receipt attachment mutations are implemented in
// media_resolvers.go.
// RequestExport is implemented in export_resolvers.go (T38.a).
