package graphql

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/knowledge"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
	"context"
	"errors"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func knowledgeActor(p httpx.Principal) knowledge.Actor {
	return knowledge.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}
func knowledgeProblems(err error) ([]*gql.MutationProblem, bool) {
	var p *knowledge.Problem
	if !errors.As(err, &p) {
		return nil, false
	}
	out := &gql.MutationProblem{Code: gql.ProblemCode(p.Code), Message: p.Message}
	if p.Field != "" {
		out.Field = &p.Field
	}
	if p.Revision != nil {
		r := int(*p.Revision)
		out.CurrentRevision = &r
	}
	return []*gql.MutationProblem{out}, true
}
func knowledgeError(err error) error {
	var p *knowledge.Problem
	if errors.As(err, &p) {
		return &gqlerror.Error{Message: p.Message, Extensions: map[string]any{"code": p.Code}}
	}
	return gqlerror.Errorf("%s", err.Error())
}
func knowledgeSettingsOut(s dbgen.KnowledgeSettingsRow) *gql.GardenKnowledgeSettings {
	out := &gql.GardenKnowledgeSettings{Country: s.Country, Region: s.Region, Place: s.Place, Timezone: s.Timezone, Enabled: s.Enabled, Revision: int(s.Revision)}
	if s.Latitude != nil {
		v, err := numericString(*s.Latitude)
		if err == nil {
			d := scalars.Decimal(v)
			out.Latitude = &d
		}
	}
	if s.Longitude != nil {
		v, err := numericString(*s.Longitude)
		if err == nil {
			d := scalars.Decimal(v)
			out.Longitude = &d
		}
	}
	if s.HardinessZone.Valid {
		out.HardinessZone = &s.HardinessZone.String
	}
	if s.ZoneSystem.Valid {
		out.ZoneSystem = &s.ZoneSystem.String
	}
	return out
}
func factOut(f knowledge.Fact) *gql.SeasonalFact {
	return &gql.SeasonalFact{ID: f.ID, Kind: gql.SeasonalKind(f.Kind), Label: f.Label, Summary: f.Summary, Months: f.Months, TimingBasis: gql.TimingBasis(f.TimingBasis), Scope: f.Scope, SourceTitle: f.SourceTitle, SourceURL: f.SourceURL, Publisher: f.Publisher, SourceRegion: f.SourceRegion, RetrievedAt: scalars.DateTime(f.RetrievedAt), ContentSha256: f.ContentSHA256}
}
func (q queryResolver) GardenKnowledge(ctx context.Context, year int) (*gql.GardenKnowledge, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Knowledge == nil {
		return nil, capabilityUnavailable("garden knowledge")
	}
	garden, err := q.r.Knowledge.Garden(ctx, p.WorkspaceID, year)
	if err != nil {
		if _, ok := knowledgeProblems(err); ok {
			return nil, knowledgeError(err)
		}
		return nil, internalError(ctx, err)
	}
	out := &gql.GardenKnowledge{Settings: knowledgeSettingsOut(garden.Settings), Plants: []*gql.PlantKnowledge{}, Records: []*gql.GardenMonthRecord{}}
	for _, p := range garden.Plants {
		plant := &gql.PlantKnowledge{PlantID: p.PlantID, Name: p.Name, Cultivar: p.Cultivar, Category: gql.PlantCategory(p.Category), PhotoAssetID: p.PhotoAssetID, State: gql.KnowledgeState(p.State), Message: p.Message, UpdatedAt: scalars.DateTime(p.UpdatedAt), Facts: []*gql.SeasonalFact{}}
		for _, f := range p.Facts {
			plant.Facts = append(plant.Facts, factOut(f))
		}
		out.Plants = append(out.Plants, plant)
	}
	for _, r := range garden.Records {
		out.Records = append(out.Records, &gql.GardenMonthRecord{PlantID: uuidString(r.PlantID), Month: int(r.Month), Waterings: int(r.Waterings), Photos: int(r.Photos), Harvests: int(r.Harvests), Fertilizations: int(r.Fertilizations)})
	}
	return out, nil
}
func (q queryResolver) GardenPlaces(ctx context.Context, search string) ([]*gql.GardenPlace, error) {
	if _, err := principal(ctx); err != nil {
		return nil, err
	}
	if q.r.Knowledge == nil {
		return nil, capabilityUnavailable("garden location search")
	}
	places, err := q.r.Knowledge.Places(ctx, search)
	if err != nil {
		return nil, knowledgeError(err)
	}
	out := []*gql.GardenPlace{}
	for _, p := range places {
		out = append(out, &gql.GardenPlace{Name: p.Name, Country: p.Country, Region: p.Region, Latitude: scalars.Decimal(p.Latitude), Longitude: scalars.Decimal(p.Longitude), Timezone: p.Timezone})
	}
	return out, nil
}
func (m mutationResolver) SaveGardenKnowledgeSettings(ctx context.Context, in gql.SaveGardenKnowledgeSettingsInput, key *string) (*gql.GardenKnowledgeSettingsPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Knowledge == nil {
		return nil, capabilityUnavailable("garden settings")
	}
	if in.ExpectedRevision < 0 || in.ExpectedRevision > 2147483647 {
		return &gql.GardenKnowledgeSettingsPayload{Problems: []*gql.MutationProblem{{Code: gql.ProblemCodeValidation, Message: "Invalid settings revision."}}, ClientMutationID: key}, nil
	}
	settings, err := m.r.Knowledge.SaveSettings(ctx, knowledgeActor(p), knowledge.SettingsInput{Country: in.Country, Region: in.Region, Place: in.Place, Timezone: in.Timezone, Latitude: decPtr(in.Latitude), Longitude: decPtr(in.Longitude), HardinessZone: in.HardinessZone, ZoneSystem: in.ZoneSystem, Enabled: in.Enabled, ExpectedRevision: int32(in.ExpectedRevision)}, key)
	out := &gql.GardenKnowledgeSettingsPayload{Problems: []*gql.MutationProblem{}, ClientMutationID: key}
	if err != nil {
		if problems, ok := knowledgeProblems(err); ok {
			out.Problems = problems
			return out, nil
		}
		return nil, internalError(ctx, err)
	}
	out.Settings = knowledgeSettingsOut(settings)
	return out, nil
}
func (m mutationResolver) RefreshGardenKnowledge(ctx context.Context, plantID *string, key *string) (*gql.RefreshKnowledgePayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Knowledge == nil {
		return nil, capabilityUnavailable("garden knowledge")
	}
	count, err := m.r.Knowledge.Refresh(ctx, knowledgeActor(p), plantID, key)
	out := &gql.RefreshKnowledgePayload{Queued: count, Problems: []*gql.MutationProblem{}, ClientMutationID: key}
	if err != nil {
		if problems, ok := knowledgeProblems(err); ok {
			out.Problems = problems
			return out, nil
		}
		return nil, internalError(ctx, err)
	}
	return out, nil
}
func (m mutationResolver) AskGarden(ctx context.Context, question string, year int) (*gql.GardenAnswer, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Knowledge == nil {
		return nil, capabilityUnavailable("garden chat")
	}
	answer, facts, err := m.r.Knowledge.Ask(ctx, p.WorkspaceID, question, year)
	if err != nil {
		return nil, knowledgeError(err)
	}
	out := &gql.GardenAnswer{Answer: answer.Answer, PlantIds: answer.PlantIDs, Facts: []*gql.SeasonalFact{}}
	for _, f := range facts {
		out.Facts = append(out.Facts, factOut(f))
	}
	return out, nil
}
