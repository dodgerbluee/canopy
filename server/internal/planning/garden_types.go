package planning

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/collection/commands"
	"context"
	"time"
)

// GardenWriter joins existing collection rules to this command's transaction.
// It must not begin/commit a second transaction or retain an unreviewed intent.
type GardenWriter interface {
	Create(context.Context, *dbgen.Queries, Actor, commands.CreatePlantInput) (GardenPlantResult, []Problem, error)
	RecordPlanting(context.Context, *dbgen.Queries, Actor, ActualPlantingInput) (GardenPlantResult, []Problem, error)
}
type GardenPlantResult struct {
	ID       string
	Revision int32
}
type ActualPlantingInput struct {
	PlantID          string
	ExpectedRevision int32
	On               time.Time
	Context          *commands.ContextInput
}
type AddToGardenInput struct {
	PlanID                string
	ExpectedRevision      int32
	NewPlant              *commands.CreatePlantInput
	ExistingPlantID       *string
	ActionID              *string
	CompletedOn           *string
	ExpectedPlantRevision *int32
}
type CompleteActionInput struct {
	PlanID, ID            string
	ExpectedRevision      int32
	CompletedOn           string
	ExpectedPlantRevision *int32
	ActualContext         *commands.ContextInput
}
