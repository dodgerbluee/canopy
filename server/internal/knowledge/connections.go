package knowledge

import (
	"canopy/server/internal/aiconnection"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Service) selectedModel(ctx context.Context, ws pgtype.UUID) (Model, error) {
	if s.ResolveModel != nil {
		return s.ResolveModel(ctx, ws)
	}
	return s.Model, nil
}
func (s *Service) UseConnections(m *aiconnection.Manager) {
	s.ResolveModel = func(ctx context.Context, ws pgtype.UUID) (Model, error) {
		selected, err := m.Resolve(ctx, ws, aiconnection.Garden)
		if err != nil {
			return nil, errors.New("The garden AI connection is unavailable. Review AI connections in Settings.")
		}
		if selected.Config == nil || selected.Disabled {
			return nil, nil
		}
		c := *selected.Config
		model := NewModel(c.Origin(), c.APIKey, c.Model, c.ReasoningEffort)
		model.HTTP = m.Client(c)
		return model, nil
	}
}
