package collection

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// maxLocationDepth matches the contract: parent chains deeper than 5 are
// rejected with VALIDATION (the DB comment defers this to the service).
const maxLocationDepth = 5

// LocationResult identifies the affected location aggregate.
type LocationResult struct {
	ID       string `json:"id"`
	Revision int32  `json:"revision"`
}

// CreateLocationInput mirrors CreateLocationInput. ParentLocationID nil =
// top level. Situation is required by the contract.
type CreateLocationInput struct {
	Name           string
	ParentLocation *string
	Situation      string
	Latitude       *pgtype.Numeric
	Longitude      *pgtype.Numeric
	Label          *string
}

// UpdateLocationInput mirrors UpdateLocationInput. The *Present flags carry
// GraphQL presence: the frozen query always re-assigns parent_id and label,
// so an omitted field must pass the current value through while an explicit
// null clears it.
type UpdateLocationInput struct {
	ID                    string
	ExpectedRevision      int32
	Name                  *string
	ParentLocation        *string
	ParentLocationPresent bool
	Situation             *string
	Latitude              *pgtype.Numeric
	Longitude             *pgtype.Numeric
	Label                 *string
	LabelPresent          bool
}

// CreateLocation inserts a location after validating membership, depth and
// coordinate ranges.
func (s *Service) CreateLocation(ctx context.Context, a Actor, in CreateLocationInput, cmid *string) (LocationResult, []Problem, error) {
	return mutate[LocationResult](s, ctx, a, "createLocation", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (LocationResult, []Problem) {
			var problems []Problem
			name := strings.TrimSpace(in.Name)
			if name == "" || len(name) > 160 {
				problems = append(problems, problem(CodeValidation, "input.name", "name must be 1-160 characters"))
			}
			situation, ok := parseSituation(in.Situation)
			if !ok {
				problems = append(problems, problem(CodeValidation, "input.situation", "unknown situation"))
			}
			if in.Label != nil && len(*in.Label) > 160 {
				problems = append(problems, problem(CodeValidation, "input.label", "label must be at most 160 characters"))
			}
			problems = append(problems, coordProblems(in.Latitude, in.Longitude)...)
			var parentID pgtype.UUID
			if in.ParentLocation != nil {
				pid, ok := parseUUID(*in.ParentLocation)
				if !ok {
					problems = append(problems, problem(CodeValidation, "input.parentLocationId", "malformed id"))
				} else {
					if _, err := q.GetLocation(ctx, dbgen.GetLocationParams{ID: pid, WorkspaceID: a.WorkspaceID}); err != nil {
						problems = append(problems, problem(CodeNotFound, "input.parentLocationId", "location not found"))
					} else {
						depth, err := ancestorCount(ctx, q, pid, a.WorkspaceID)
						if err != nil {
							s.Log.Error("ancestor walk", "err", err)
							problems = append(problems, problem(CodeValidation, "", "depth check failed"))
						} else if depth+1 > maxLocationDepth {
							problems = append(problems, problem(CodeValidation, "input.parentLocationId",
								"location depth would exceed 5"))
						}
						parentID = pid
					}
				}
			}
			if len(problems) > 0 {
				return LocationResult{}, problems
			}
			row, err := q.InsertLocation(ctx, dbgen.InsertLocationParams{
				ID:          s.uuid.New(),
				WorkspaceID: a.WorkspaceID,
				Name:        name,
				ParentID:    parentID,
				Situation:   situation,
				Latitude:    in.Latitude,
				Longitude:   in.Longitude,
				Label:       textPtr(in.Label),
			})
			if err != nil {
				s.Log.Error("createLocation insert", "err", err)
				return LocationResult{}, []Problem{problem(CodeValidation, "input", "location could not be stored")}
			}
			if err := s.audit(ctx, q, a, "createLocation", "location", row.ID, row.Revision, nil); err != nil {
				return LocationResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return LocationResult{ID: uuidString(row.ID), Revision: row.Revision}, nil
		})
}

// UpdateLocation applies a guarded revision bump with cycle and depth
// enforcement over the prospective parent chain.
func (s *Service) UpdateLocation(ctx context.Context, a Actor, in UpdateLocationInput, cmid *string) (LocationResult, []Problem, error) {
	return mutate[LocationResult](s, ctx, a, "updateLocation", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (LocationResult, []Problem) {
			id, ok := parseUUID(in.ID)
			if !ok {
				return LocationResult{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}
			}
			cur, err := q.GetLocation(ctx, dbgen.GetLocationParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return LocationResult{}, notFound("input.id", "location not found")
			}
			var problems []Problem
			if in.Name != nil {
				name := strings.TrimSpace(*in.Name)
				if name == "" || len(name) > 160 {
					problems = append(problems, problem(CodeValidation, "input.name", "name must be 1-160 characters"))
				} else {
					in.Name = &name
				}
			}
			if in.LabelPresent && in.Label != nil && len(*in.Label) > 160 {
				problems = append(problems, problem(CodeValidation, "input.label", "label must be at most 160 characters"))
			}
			problems = append(problems, coordProblems(in.Latitude, in.Longitude)...)

			// Prospective parent: presence decides keep vs clear vs move.
			newParent := cur.ParentID
			if in.ParentLocationPresent {
				if in.ParentLocation == nil {
					newParent = pgtype.UUID{} // explicit null -> top level
				} else {
					pid, ok := parseUUID(*in.ParentLocation)
					switch {
					case !ok:
						problems = append(problems, problem(CodeValidation, "input.parentLocationId", "malformed id"))
					case pid == id:
						problems = append(problems, problem(CodeValidation, "input.parentLocationId",
							"a location cannot be its own parent"))
					default:
						if _, err := q.GetLocation(ctx, dbgen.GetLocationParams{ID: pid, WorkspaceID: a.WorkspaceID}); err != nil {
							problems = append(problems, problem(CodeNotFound, "input.parentLocationId", "location not found"))
						} else {
							// Cycle check: the new parent must not be inside our subtree.
							anc, err := q.LocationAncestors(ctx, dbgen.LocationAncestorsParams{ID: pid, WorkspaceID: a.WorkspaceID})
							if err != nil {
								s.Log.Error("ancestor walk", "err", err)
								problems = append(problems, problem(CodeValidation, "", "cycle check failed"))
							}
							for _, x := range anc {
								if x.ID == id {
									problems = append(problems, problem(CodeValidation, "input.parentLocationId",
										"re-parenting would create a cycle"))
									break
								}
							}
							// Depth check: subtree under us + chain above new parent.
							if len(problems) == 0 {
								sub, err := q.LocationSubtreeDepth(ctx, dbgen.LocationSubtreeDepthParams{ID: id, WorkspaceID: a.WorkspaceID})
								if err != nil {
									s.Log.Error("subtree depth", "err", err)
									problems = append(problems, problem(CodeValidation, "", "depth check failed"))
								} else if int(sub)+len(anc) > maxLocationDepth {
									problems = append(problems, problem(CodeValidation, "input.parentLocationId",
										"re-parenting would exceed depth 5"))
								}
							}
							newParent = pid
						}
					}
				}
			}
			if len(problems) > 0 {
				return LocationResult{}, problems
			}

			label := cur.Label
			if in.LabelPresent {
				label = textPtr(in.Label)
			}
			params := dbgen.UpdateLocationParams{
				ID:               id,
				WorkspaceID:      a.WorkspaceID,
				ExpectedRevision: in.ExpectedRevision,
				Name:             textPtr(in.Name),
				ParentID:         newParent,
				Latitude:         in.Latitude,
				Longitude:        in.Longitude,
				Label:            label,
			}
			if in.Situation != nil {
				sit, ok := parseSituation(*in.Situation)
				if !ok {
					return LocationResult{}, []Problem{problem(CodeValidation, "input.situation", "unknown situation")}
				}
				params.Situation = dbgen.NullLocationSituation{LocationSituation: sit, Valid: true}
			}
			updated, err := q.UpdateLocation(ctx, params)
			if err != nil {
				return LocationResult{}, s.conflictProblem(ctx, q, "locations", id, a.WorkspaceID, err)
			}
			if err := s.audit(ctx, q, a, "updateLocation", "location", id, updated.Revision, cur); err != nil {
				return LocationResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return LocationResult{ID: uuidString(id), Revision: updated.Revision}, nil
		})
}

// ancestorCount counts the chain above (and including) loc, i.e. the depth
// of loc itself with root = 1.
func ancestorCount(ctx context.Context, q *dbgen.Queries, loc, ws pgtype.UUID) (int, error) {
	anc, err := q.LocationAncestors(ctx, dbgen.LocationAncestorsParams{ID: loc, WorkspaceID: ws})
	if err != nil {
		return 0, err
	}
	return len(anc), nil
}

// coordProblems validates decimal coordinate ranges before hitting the DB
// check constraint, so the client gets a clean VALIDATION problem.
func coordProblems(lat, lng *pgtype.Numeric) []Problem {
	var problems []Problem
	if lat != nil && !coordInRange(*lat, 90) {
		problems = append(problems, problem(CodeValidation, "input.latitude", "latitude must be within [-90, 90]"))
	}
	if lng != nil && !coordInRange(*lng, 180) {
		problems = append(problems, problem(CodeValidation, "input.longitude", "longitude must be within [-180, 180]"))
	}
	return problems
}

func coordInRange(n pgtype.Numeric, bound int64) bool {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite {
		return false
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return false // overflow of float64 is far outside any coordinate range
	}
	v := f.Float64
	return v >= -float64(bound) && v <= float64(bound)
}

func parseSituation(s string) (dbgen.LocationSituation, bool) {
	switch s {
	case "INDOOR", "OUTDOOR", "COVERED":
		return dbgen.LocationSituation(s), true
	}
	return "", false
}

// notFoundErr maps an absent row lookup.
func notFoundErr(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
