package planning

import (
	"context"
	"errors"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5"
)

func subjectFromRow(row dbgen.PlantingPlan) GuidanceSubject {
	out := GuidanceSubject{Name: row.Name, ScientificName: row.ScientificName.String, Cultivar: row.Cultivar.String, Category: string(row.Category), Method: string(row.Method.PlantingMethod), Placement: string(row.IntendedPlacement.ContextPlacement), LocationID: idString(row.IntendedLocationID), Notes: row.Notes.String}
	if row.DesiredQuantity.Valid {
		v := row.DesiredQuantity.Int32
		out.Quantity = &v
	}
	return out
}
func (s *Service) insertGuidance(ctx context.Context, q *dbgen.Queries, row dbgen.PlantingPlan, c GuidanceContext) (dbgen.PlantingGuidanceSnapshot, error) {
	if !sha256Pattern.MatchString(c.Fingerprint) || !contains(c.State, "PENDING", "NEEDS_LOCATION", "DISABLED", "UNSUPPORTED", "FAILED") || len(c.Message) > 1000 || len(c.Assumptions) > MaxGuidanceAssumptions {
		return dbgen.PlantingGuidanceSnapshot{}, errors.New("invalid guidance context")
	}
	if c.Assumptions == nil {
		c.Assumptions = []string{}
	}
	snapshot, err := q.InsertPlanningGuidance(ctx, dbgen.InsertPlanningGuidanceParams{ID: s.IDs.New(), WorkspaceID: row.WorkspaceID, PlanID: row.ID, Generation: row.GuidanceGeneration, ContextSha256: c.Fingerprint, State: dbgen.PlantingGuidanceState(c.State), Message: c.Message, Assumptions: c.Assumptions})

	return snapshot, err
}

// Context is resolved before the command transaction so concurrent saves do
// not hold every pool connection while asking another pool query for settings.
func (s *Service) describeIntentGuidance(ctx context.Context, a Actor, in Intent) (*GuidanceContext, error) {
	if s.Guidance == nil {
		return nil, nil
	}
	p, problems := validateIntent(in)
	if len(problems) > 0 {
		return nil, nil
	}
	row := dbgen.PlantingPlan{Name: p.Name, ScientificName: p.ScientificName, Cultivar: p.Cultivar, Category: p.Category, DesiredQuantity: p.DesiredQuantity, Method: p.Method, IntendedLocationID: p.IntendedLocationID, IntendedPlacement: p.IntendedPlacement, Notes: p.Notes}
	c, err := s.Guidance.Context(ctx, a.WorkspaceID, subjectFromRow(row))
	return &c, err
}
func (s *Service) describeStoredGuidance(ctx context.Context, a Actor, rawID string) (*GuidanceContext, error) {
	if s.Guidance == nil {
		return nil, nil
	}
	id, ok := parseID(rawID)
	if !ok {
		return nil, nil
	}
	row, err := s.q().GetPlantingPlan(ctx, dbgen.GetPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := s.Guidance.Context(ctx, a.WorkspaceID, subjectFromRow(row))
	return &c, err
}
func (s *Service) queueGuidance(ctx context.Context, q *dbgen.Queries, row dbgen.PlantingPlan, c *GuidanceContext) error {
	if c == nil {
		return nil
	}
	_, err := s.insertGuidance(ctx, q, row, *c)
	return err
}
func (s *Service) pauseGuidance(ctx context.Context, q *dbgen.Queries, row dbgen.PlantingPlan) (dbgen.PlantingPlan, error) {
	if s.Guidance == nil {
		return row, nil
	}
	snapshot, err := q.GetCurrentPlanningGuidance(ctx, dbgen.GetCurrentPlanningGuidanceParams{WorkspaceID: row.WorkspaceID, PlanID: row.ID, Generation: row.GuidanceGeneration})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, nil
	}
	if err != nil {
		return row, err
	}
	if snapshot.State != "PENDING" && snapshot.State != "PROCESSING" {
		return row, nil
	}
	if err = q.AbandonPlanningGuidance(ctx, dbgen.AbandonPlanningGuidanceParams{WorkspaceID: row.WorkspaceID, PlanID: row.ID, ID: snapshot.ID}); err != nil {
		return row, err
	}
	return q.AdvancePlanningGuidance(ctx, dbgen.AdvancePlanningGuidanceParams{WorkspaceID: row.WorkspaceID, ID: row.ID})
}

// RefreshGuidance records a request, not synchronous model work. Keeping pending
// duplicates stable prevents repeated taps from creating new work/generations.
func (s *Service) RefreshGuidance(ctx context.Context, a Actor, in RevisionInput, key *string) (Result, []Problem, error) {
	description, err := s.describeStoredGuidance(ctx, a, in.ID)
	if err != nil {
		return Result{}, nil, err
	}
	return s.mutate(ctx, a, "refreshPlantingGuidance", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		row, ps, err := lockPlan(ctx, q, a, in.ID, in.ExpectedRevision, "input.id")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		if !active(row.State) {
			return Result{}, invalid("input.id", "Guidance can be refreshed on active plans."), nil
		}
		if s.Guidance == nil {
			return Result{}, []Problem{{Code: "CAPABILITY_UNAVAILABLE", Field: "input.id", Message: "Regional research is not configured."}}, nil
		}
		if description == nil {
			return Result{}, invalid("input.id", "The plan changed while requesting guidance. Reload it."), nil
		}
		c := *description
		prior, err := q.GetCurrentPlanningGuidance(ctx, dbgen.GetCurrentPlanningGuidanceParams{WorkspaceID: a.WorkspaceID, PlanID: row.ID, Generation: row.GuidanceGeneration})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Result{}, nil, err
		}
		if err == nil && prior.ContextSha256 == c.Fingerprint && (prior.State == "PENDING" || prior.State == "PROCESSING") {
			return result(row), nil, nil
		}
		old := row
		row, err = q.AdvancePlanningGuidance(ctx, dbgen.AdvancePlanningGuidanceParams{WorkspaceID: a.WorkspaceID, ID: row.ID, EditRevision: true})
		if err != nil {
			return Result{}, nil, err
		}
		if _, err = s.insertGuidance(ctx, q, row, c); err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "refreshPlantingGuidance", row, old); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}

// A stale source result may be kept only as failed history, never as current READY.
func (s *Service) abandonGuidance(ctx context.Context, q *dbgen.Queries, snapshot dbgen.PlantingGuidanceSnapshot) error {
	return q.AbandonPlanningGuidance(ctx, dbgen.AbandonPlanningGuidanceParams{WorkspaceID: snapshot.WorkspaceID, PlanID: snapshot.PlanID, ID: snapshot.ID})
}
