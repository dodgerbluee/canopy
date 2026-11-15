package graphql

import (
	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"context"
)

func (m mutationResolver) AddPlantingToGarden(ctx context.Context, in gql.AddPlantingToGardenInput, key *string) (*gql.PlantingPlanPayload, error) {
	input := planning.AddToGardenInput{PlanID: in.PlanID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision), ExistingPlantID: in.ExistingPlantID, ActionID: in.ActionID, CompletedOn: plantingString(in.CompletedOn), ExpectedPlantRevision: plantingQuantity(in.ExpectedPlantRevision)}
	if in.NewPlant != nil {
		plant, ps, err := createPlantInput(ctx, *in.NewPlant)
		if err != nil {
			return nil, err
		}
		if len(ps) > 0 {
			return &gql.PlantingPlanPayload{Problems: ps, ClientMutationID: key}, nil
		}
		input.NewPlant = &plant
	}
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.AddToGarden(ctx, a, input, key)
	})
}
func (m mutationResolver) CompletePlantingAction(ctx context.Context, in gql.CompletePlantingActionInput, key *string) (*gql.PlantingPlanPayload, error) {
	input := planning.CompleteActionInput{PlanID: in.PlanID, ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision), CompletedOn: string(in.CompletedOn), ExpectedPlantRevision: plantingQuantity(in.ExpectedPlantRevision)}
	if in.ActualContext != nil {
		context, ps, err := contextInput(ctx, in.ActualContext)
		if err != nil {
			return nil, err
		}
		if len(ps) > 0 {
			return &gql.PlantingPlanPayload{Problems: ps, ClientMutationID: key}, nil
		}
		input.ActualContext = context
	}
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.CompleteAction(ctx, a, input, key)
	})
}
func (m mutationResolver) DismissPlantingAction(ctx context.Context, in gql.DismissPlantingActionInput, key *string) (*gql.PlantingPlanPayload, error) {
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.DismissAction(ctx, a, planning.DeleteActionInput{PlanID: in.PlanID, ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision)}, key)
	})
}
