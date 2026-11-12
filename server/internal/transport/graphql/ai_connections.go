package graphql

import (
	"canopy/server/internal/aiconnection"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	"context"
	"errors"
)

func aiInput(in gql.SaveAIConnectionInput) aiconnection.SaveInput {
	var copyFrom *string
	if in.CopyDeploymentKeyFrom != nil {
		v := string(*in.CopyDeploymentKeyFrom)
		copyFrom = &v
	}
	return aiconnection.SaveInput{ID: in.ID, Name: in.Name, Protocol: string(in.Protocol), Endpoint: in.Endpoint, Model: in.Model, DeploymentLocal: in.DeploymentLocal, SupportsVision: in.SupportsVision, ExternalConsent: in.ExternalConsent, APIKey: in.APIKey, ClearAPIKey: in.ClearAPIKey != nil && *in.ClearAPIKey, CopyDeploymentKeyFrom: copyFrom, ReasoningEffort: in.ReasoningEffort, TokenLimitField: string(in.TokenLimitField), ExpectedRevision: in.ExpectedRevision}
}
func aiConnectionOut(c aiconnection.Connection) *gql.AIConnection {
	out := &gql.AIConnection{ID: c.ID, Name: c.Name, Protocol: gql.AIProviderProtocol(c.Protocol), Endpoint: c.Endpoint, Model: c.Model, DeploymentLocal: c.DeploymentLocal, SupportsVision: c.SupportsVision, ExternalConsent: c.ExternalConsent, HasAPIKey: c.HasAPIKey, ReasoningEffort: c.ReasoningEffort, TokenLimitField: gql.AITokenLimitField(c.TokenLimitField), Revision: c.Revision, LastTestSucceeded: c.LastTestSucceeded, LastTestMessage: c.LastTestMessage}
	if c.LastTestedAt != nil {
		v := scalars.DateTime(*c.LastTestedAt)
		out.LastTestedAt = &v
	}
	return out
}
func aiSettingsOut(s aiconnection.Settings) *gql.AIConnectionSettings {
	out := &gql.AIConnectionSettings{Connections: []*gql.AIConnection{}, Bindings: []*gql.AIFeatureBinding{}, DeploymentDefaults: []*gql.AIDeploymentConnection{}}
	for _, c := range s.Connections {
		out.Connections = append(out.Connections, aiConnectionOut(c))
	}
	for _, b := range s.Bindings {
		v := &gql.AIFeatureBinding{Feature: gql.AIConnectionFeature(b.Feature), Mode: gql.AIBindingMode(b.Mode), Revision: b.Revision}
		if b.ConnectionID != "" {
			v.ConnectionID = &b.ConnectionID
		}
		out.Bindings = append(out.Bindings, v)
	}
	for _, d := range s.Defaults {
		c := d.Config
		out.DeploymentDefaults = append(out.DeploymentDefaults, &gql.AIDeploymentConnection{Feature: gql.AIConnectionFeature(d.Feature), Protocol: gql.AIProviderProtocol(c.Protocol), Endpoint: c.Endpoint, Model: c.Model, DeploymentLocal: c.DeploymentLocal, SupportsVision: c.SupportsVision, HasAPIKey: d.HasAPIKey, ReasoningEffort: c.ReasoningEffort, TokenLimitField: gql.AITokenLimitField(c.TokenLimitField)})
	}
	return out
}
func aiProblems(ctx context.Context, err error) ([]*gql.MutationProblem, error) {
	if err == nil {
		return []*gql.MutationProblem{}, nil
	}
	if errors.Is(err, aiconnection.ErrCredential) {
		field := "apiKey"
		return []*gql.MutationProblem{{Code: gql.ProblemCodeValidation, Field: &field, Message: "The saved key could not be opened. Re-enter it or explicitly remove it."}}, nil
	}
	var p *aiconnection.Problem
	if errors.As(err, &p) {
		v := &gql.MutationProblem{Code: gql.ProblemCode(p.Code), Message: p.Message, CurrentRevision: p.Revision}
		if p.Field != "" {
			v.Field = &p.Field
		}
		return []*gql.MutationProblem{v}, nil
	}
	// Never surface transport/credential/internal errors or provider bodies.
	return nil, internalError(ctx, errors.New("AI connection operation failed"))
}
func (q queryResolver) AiConnections(ctx context.Context) (*gql.AIConnectionSettings, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.AIConnections == nil {
		return nil, capabilityUnavailable("AI connection settings")
	}
	s, err := q.r.AIConnections.Settings(ctx, p.WorkspaceID)
	if err != nil {
		return nil, internalError(ctx, errors.New("AI settings could not be loaded"))
	}
	return aiSettingsOut(s), nil
}
func (m mutationResolver) SaveAIConnection(ctx context.Context, in gql.SaveAIConnectionInput, key *string) (*gql.AIConnectionPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.AIConnections == nil {
		return nil, capabilityUnavailable("AI connection settings")
	}
	c, err := m.r.AIConnections.Save(ctx, p.WorkspaceID, aiInput(in), key)
	problems, fatal := aiProblems(ctx, err)
	out := &gql.AIConnectionPayload{Problems: problems, ClientMutationID: key}
	if err == nil {
		out.Connection = aiConnectionOut(c)
	}
	return out, fatal
}
func (m mutationResolver) SaveAIFeatureBindings(ctx context.Context, in []*gql.AIFeatureBindingInput, key *string) (*gql.AIConnectionSettingsPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.AIConnections == nil {
		return nil, capabilityUnavailable("AI connection settings")
	}
	inputs := []aiconnection.BindingInput{}
	for _, v := range in {
		id := ""
		if v.ConnectionID != nil {
			id = *v.ConnectionID
		}
		inputs = append(inputs, aiconnection.BindingInput{Feature: string(v.Feature), Mode: string(v.Mode), ConnectionID: id, ExpectedRevision: v.ExpectedRevision})
	}
	_, err = m.r.AIConnections.SaveBindings(ctx, p.WorkspaceID, inputs, key)
	problems, fatal := aiProblems(ctx, err)
	out := &gql.AIConnectionSettingsPayload{Problems: problems, ClientMutationID: key}
	if err == nil {
		s, e := m.r.AIConnections.Settings(ctx, p.WorkspaceID)
		if e != nil {
			return nil, internalError(ctx, errors.New("AI settings could not be loaded"))
		}
		out.Settings = aiSettingsOut(s)
	}
	return out, fatal
}
func (m mutationResolver) DeleteAIConnection(ctx context.Context, in gql.DeleteAIConnectionInput, key *string) (*gql.AIConnectionSettingsPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.AIConnections == nil {
		return nil, capabilityUnavailable("AI connection settings")
	}
	err = m.r.AIConnections.Delete(ctx, p.WorkspaceID, in.ID, in.ExpectedRevision, key)
	problems, fatal := aiProblems(ctx, err)
	out := &gql.AIConnectionSettingsPayload{Problems: problems, ClientMutationID: key}
	if err == nil {
		s, e := m.r.AIConnections.Settings(ctx, p.WorkspaceID)
		if e != nil {
			return nil, internalError(ctx, errors.New("AI settings could not be loaded"))
		}
		out.Settings = aiSettingsOut(s)
	}
	return out, fatal
}
func (m mutationResolver) TestAIConnection(ctx context.Context, in gql.TestAIConnectionInput) (*gql.AIConnectionTestPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.AIConnections == nil {
		return nil, capabilityUnavailable("AI connection settings")
	}
	input := aiconnection.TestInput{ExpectedRevision: in.ExpectedRevision, TestVision: in.TestVision != nil && *in.TestVision}
	if in.ID != nil {
		input.ID = *in.ID
	}
	if in.Draft != nil {
		d := aiInput(*in.Draft)
		input.Draft = &d
	}
	result, err := m.r.AIConnections.Test(ctx, p.WorkspaceID, input)
	problems, fatal := aiProblems(ctx, err)
	out := &gql.AIConnectionTestPayload{Problems: problems}
	if err == nil {
		out.Result = &gql.AIConnectionTest{Succeeded: result.Succeeded, Message: result.Message, TestedAt: scalars.DateTime(result.TestedAt), VisionRequest: result.VisionRequest}
	}
	return out, fatal
}
