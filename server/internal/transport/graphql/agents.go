package graphql

import (
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	"context"
	"encoding/json"
	"errors"
	"time"
)

func agentGrantOut(g agentaccess.Grant) *gql.AgentGrant {
	out := &gql.AgentGrant{ID: g.ID, Name: g.Name, Scopes: g.Scopes, PlantIds: g.PlantIDs, ExpiresAt: scalars.DateTime(g.ExpiresAt), Revision: g.Revision, LastTool: g.LastTool, CreatedAt: scalars.DateTime(g.CreatedAt)}
	if g.RevokedAt != nil {
		v := scalars.DateTime(*g.RevokedAt)
		out.RevokedAt = &v
	}
	if g.LastUsedAt != nil {
		v := scalars.DateTime(*g.LastUsedAt)
		out.LastUsedAt = &v
	}
	return out
}
func agentProblems(err error) []*gql.MutationProblem {
	if err == nil {
		return []*gql.MutationProblem{}
	}
	var p *agentaccess.Problem
	if errors.As(err, &p) {
		return []*gql.MutationProblem{{Code: gql.ProblemCode(p.Code), Message: p.Message}}
	}
	if errors.Is(err, agentaccess.ErrDenied) {
		return []*gql.MutationProblem{{Code: gql.ProblemCodeForbidden, Message: "Agent connection is unavailable or not permitted."}}
	}
	return []*gql.MutationProblem{{Code: gql.ProblemCodeValidation, Message: "Agent operation could not be completed. Reload the connection or proposal before retrying."}}
}
func (q queryResolver) AgentSettings(ctx context.Context) (*gql.AgentSettings, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.AgentAccess == nil {
		return nil, capabilityUnavailable("agent connections")
	}
	grants, err := q.r.AgentAccess.List(ctx, p.WorkspaceID)
	if err != nil {
		return nil, internalError(ctx, errors.New("Agent settings could not be read"))
	}
	actions, err := q.r.AgentAccess.Actions(ctx, p.WorkspaceID, "")
	if err != nil {
		return nil, internalError(ctx, errors.New("Agent proposals could not be read"))
	}
	out := &gql.AgentSettings{Enabled: q.r.AgentEnabled, Endpoint: q.r.AgentEndpoint, ScopeOptions: agentaccess.Scopes, Grants: []*gql.AgentGrant{}, Actions: []*gql.AgentAction{}}
	for _, g := range grants {
		out.Grants = append(out.Grants, agentGrantOut(g))
	}
	for _, a := range actions {
		out.Actions = append(out.Actions, &gql.AgentAction{ID: a.ID, GrantID: a.GrantID, GrantName: a.GrantName, OperationID: a.OperationID, Tool: a.Tool, Input: string(a.Input), Before: string(a.Before), State: a.State, CreatedAt: scalars.DateTime(a.CreatedAt), ExpiresAt: scalars.DateTime(a.ExpiresAt)})
	}
	return out, nil
}
func (m mutationResolver) SaveAgentGrant(ctx context.Context, in gql.SaveAgentGrantInput) (*gql.AgentGrantPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.AgentAccess == nil {
		return nil, capabilityUnavailable("agent connections")
	}
	issued, err := m.r.AgentAccess.Save(ctx, p.WorkspaceID, agentaccess.SaveInput{ID: in.ID, Name: in.Name, Scopes: in.Scopes, PlantIDs: in.PlantIds, ExpiresAt: time.Time(in.ExpiresAt), ExpectedRevision: in.ExpectedRevision})
	out := &gql.AgentGrantPayload{Problems: agentProblems(err)}
	if err == nil {
		out.Grant = agentGrantOut(issued.Grant)
		if issued.Token != "" {
			out.Token = &issued.Token
		}
	}
	return out, nil
}
func (m mutationResolver) RotateAgentCredential(ctx context.Context, in gql.ChangeAgentCredentialInput) (*gql.AgentGrantPayload, error) {
	return m.agentCredential(ctx, in, false)
}
func (m mutationResolver) RevokeAgentGrant(ctx context.Context, in gql.ChangeAgentCredentialInput) (*gql.AgentGrantPayload, error) {
	return m.agentCredential(ctx, in, true)
}
func (m mutationResolver) agentCredential(ctx context.Context, in gql.ChangeAgentCredentialInput, revoke bool) (*gql.AgentGrantPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.AgentAccess == nil {
		return nil, capabilityUnavailable("agent connections")
	}
	issued, err := m.r.AgentAccess.ChangeCredential(ctx, p.WorkspaceID, agentaccess.ID(in.ID), in.ExpectedRevision, revoke)
	out := &gql.AgentGrantPayload{Problems: agentProblems(err)}
	if err == nil {
		out.Grant = agentGrantOut(issued.Grant)
		if issued.Token != "" {
			out.Token = &issued.Token
		}
	}
	return out, nil
}
func (m mutationResolver) DecideAgentAction(ctx context.Context, in gql.DecideAgentActionInput) (*gql.AgentActionPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.AgentAccess == nil {
		return nil, capabilityUnavailable("agent actions")
	}
	result, err := m.r.AgentAccess.Decide(ctx, p.WorkspaceID, in.ID, in.Approve)
	applied := false
	if err == nil && in.Approve {
		b, _ := json.Marshal(result)
		var r struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(b, &r)
		applied = r.Status == "applied" || r.Status == "queued"
	}
	return &gql.AgentActionPayload{Applied: applied, Problems: agentProblems(err)}, nil
}
