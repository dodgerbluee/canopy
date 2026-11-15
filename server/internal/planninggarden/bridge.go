// Package planninggarden connects planning commands to collection transactions.
// Planning owns intent/replay; collection retains actual plant validation/audit.
package planninggarden

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/collection"
	"canopy/server/internal/collection/commands"
	"canopy/server/internal/planning"
	"context"
)

type Bridge struct{ Collection *collection.Service }

func New(s *collection.Service) *Bridge { return &Bridge{Collection: s} }
func actor(a planning.Actor) collection.Actor {
	return collection.Actor{WorkspaceID: a.WorkspaceID, OwnerID: a.OwnerID, SessionID: a.SessionID}
}
func problems(ps []collection.Problem) []planning.Problem {
	out := make([]planning.Problem, 0, len(ps))
	for _, p := range ps {
		out = append(out, planning.Problem{Code: p.Code, Field: p.Field, Message: p.Message, CurrentRevision: p.CurrentRevision})
	}
	return out
}
func (b *Bridge) Create(ctx context.Context, q *dbgen.Queries, a planning.Actor, in commands.CreatePlantInput) (planning.GardenPlantResult, []planning.Problem, error) {
	r, ps := b.Collection.CreatePlantInTransaction(ctx, q, actor(a), in)
	return planning.GardenPlantResult{ID: r.ID, Revision: r.Revision}, problems(ps), nil
}
func (b *Bridge) RecordPlanting(ctx context.Context, q *dbgen.Queries, a planning.Actor, in planning.ActualPlantingInput) (planning.GardenPlantResult, []planning.Problem, error) {
	r, ps := b.Collection.UpdatePlantInTransaction(ctx, q, actor(a), collection.UpdatePlantInput{ID: in.PlantID, ExpectedRevision: in.ExpectedRevision, PlantedOn: &in.On})
	if len(ps) > 0 {
		return planning.GardenPlantResult{}, problems(ps), nil
	}
	if in.Context != nil {
		c, ps := b.Collection.SetPlantContextInTransaction(ctx, q, actor(a), collection.SetPlantContextInput{PlantID: in.PlantID, ExpectedRevision: r.Revision, Context: in.Context})
		if len(ps) > 0 {
			return planning.GardenPlantResult{}, problems(ps), nil
		}
		r.Revision = c.Revision
	}
	return planning.GardenPlantResult{ID: r.ID, Revision: r.Revision}, nil, nil
}
