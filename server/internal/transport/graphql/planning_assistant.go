package graphql

import (
	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"context"
	"errors"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func (m mutationResolver) AskPlantingPlanner(ctx context.Context, question string, planIds []string, year int) (*gql.PlantingAssistantReply, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Planning == nil {
		return nil, capabilityUnavailable("planning")
	}
	result, err := m.r.Planning.AskPlanner(ctx, planningActor(p), planning.AssistantQuery{Question: question, PlanIDs: planIds, Year: year})
	if err != nil {
		if errors.Is(err, planning.ErrInvalidQuery) {
			e := gqlerror.Errorf("Choose 1–20 unique plans in this garden, a year between 1900 and 2200 and a question of 3–2000 characters.")
			setCode(e, "VALIDATION")
			return nil, e
		}
		if errors.Is(err, planning.ErrAssistantUnavailable) || errors.Is(err, planning.ErrAssistantAnswer) || errors.Is(err, planning.ErrAssistantChanged) {
			e := gqlerror.Errorf("%s", err.Error())
			setCode(e, "CAPABILITY_UNAVAILABLE")
			return nil, e
		}
		return nil, knowledgeError(err)
	}
	out := &gql.PlantingAssistantReply{Answer: result.Answer, IsAi: result.IsAI, PlanIds: result.PlanIDs, Sources: []*gql.PlantingAssistantCitation{}}
	for _, c := range result.Sources {
		out.Sources = append(out.Sources, &gql.PlantingAssistantCitation{Ref: c.Ref, PlanID: c.PlanID, PlanName: c.PlanName, Fact: plantingGuidanceFactOut(c.Fact)})
	}
	if n := result.Note; n != nil {
		out.Note = &gql.PlantingAssistantNote{PlanID: n.PlanID, ExpectedRevision: int(n.ExpectedRevision), Text: n.Text}
	}
	return out, nil
}
