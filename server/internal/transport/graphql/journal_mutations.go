// Activity and measurement mutations (T17). The service layer
// (internal/journal) owns validation, revision guards, idempotency and
// the transactional journal projection; these resolvers only translate
// between the frozen GraphQL shapes and the service views.
package graphql

import (
	"context"
	"time"

	"canopy/server/internal/journal"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

func jActor(p httpx.Principal) journal.Actor {
	return journal.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}

func mapJournalProblems(ps []journal.Problem) []*gql.MutationProblem {
	out := make([]*gql.MutationProblem, 0, len(ps))
	for _, p := range ps {
		mp := &gql.MutationProblem{
			Code:    gql.ProblemCode(p.Code),
			Message: p.Message,
		}
		if p.Field != "" {
			f := p.Field
			mp.Field = &f
		}
		if p.CurrentRevision != nil {
			rev := int(*p.CurrentRevision)
			mp.CurrentRevision = &rev
		}
		out = append(out, mp)
	}
	return out
}

func activityOut(v journal.ActivityView) *gql.Activity {
	a := &gql.Activity{
		ID:         v.ID,
		PlantID:    v.PlantID,
		Kind:       gql.ActivityKind(v.Kind),
		OccurredAt: scalars.DateTime(v.OccurredAt),
		Timezone:   v.Timezone,
		Revision:   int(v.Revision),
		CreatedAt:  scalars.DateTime(v.CreatedAt),
		UpdatedAt:  scalars.DateTime(v.UpdatedAt),
		Note:       v.Note,
		Product:    v.Product,
	}
	if v.Quantity != nil {
		q := scalars.Decimal(*v.Quantity)
		a.Quantity = &q
	}
	if v.Unit != nil {
		u := gql.Unit(*v.Unit)
		a.Unit = &u
	}
	if v.VoidedAt != nil {
		t := scalars.DateTime(*v.VoidedAt)
		a.VoidedAt = &t
	}
	return a
}

func measurementOut(v journal.MeasurementView) *gql.Measurement {
	m := &gql.Measurement{
		ID:           v.ID,
		PlantID:      v.PlantID,
		QuantityKind: gql.QuantityKind(v.QuantityKind),
		Value:        scalars.Decimal(v.Value),
		Unit:         gql.Unit(v.Unit),
		ObservedAt:   scalars.DateTime(v.ObservedAt),
		Method:       gql.MeasurementMethod(v.Method),
		Revision:     int(v.Revision),
		CreatedAt:    scalars.DateTime(v.CreatedAt),
		UpdatedAt:    scalars.DateTime(v.UpdatedAt),
		ActivityID:   v.ActivityID,
	}
	if v.Uncertainty != nil {
		u := scalars.Decimal(*v.Uncertainty)
		m.Uncertainty = &u
	}
	return m
}

func decPtr(s *scalars.Decimal) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

func unitPtr(u *gql.Unit) *string {
	if u == nil {
		return nil
	}
	v := string(*u)
	return &v
}

func dtPtr(d *scalars.DateTime) *time.Time {
	if d == nil {
		return nil
	}
	v := time.Time(*d)
	return &v
}

func (m mutationResolver) requireJournal() error {
	if m.r.Journal == nil {
		return capabilityUnavailable("journal")
	}
	return nil
}

func (m mutationResolver) RecordActivity(ctx context.Context, in gql.RecordActivityInput, c *string) (*gql.RecordActivityPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, problems, err := m.r.Journal.RecordActivity(ctx, jActor(p), journal.ActivityInput{
		PlantID:    in.PlantID,
		Kind:       string(in.Kind),
		OccurredAt: time.Time(in.OccurredAt),
		Timezone:   in.Timezone,
		Note:       in.Note,
		Quantity:   decPtr(in.Quantity),
		Unit:       unitPtr(in.Unit),
		Product:    in.Product,
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.RecordActivityPayload{Problems: mapJournalProblems(problems), ClientMutationID: c}
	if len(problems) == 0 {
		payload.Activity = activityOut(v)
	}
	return payload, nil
}

func (m mutationResolver) UpdateActivity(ctx context.Context, in gql.UpdateActivityInput, c *string) (*gql.UpdateActivityPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, problems, err := m.r.Journal.UpdateActivity(ctx, jActor(p), journal.ActivityUpdate{
		ID:               in.ID,
		ExpectedRevision: int32(in.ExpectedRevision),
		OccurredAt:       dtPtr(in.OccurredAt),
		Timezone:         in.Timezone,
		Note:             in.Note,
		Quantity:         decPtr(in.Quantity),
		Unit:             unitPtr(in.Unit),
		Product:          in.Product,
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.UpdateActivityPayload{Problems: mapJournalProblems(problems), ClientMutationID: c}
	if len(problems) == 0 {
		payload.Activity = activityOut(v)
	}
	return payload, nil
}

func (m mutationResolver) VoidActivity(ctx context.Context, in gql.VoidActivityInput, c *string) (*gql.VoidActivityPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, problems, err := m.r.Journal.VoidActivity(ctx, jActor(p), in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.VoidActivityPayload{Problems: mapJournalProblems(problems), ClientMutationID: c}
	if len(problems) == 0 {
		payload.Activity = activityOut(v)
	}
	return payload, nil
}

func (m mutationResolver) RecordMeasurement(ctx context.Context, in gql.RecordMeasurementInput, c *string) (*gql.RecordMeasurementPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, problems, err := m.r.Journal.RecordMeasurement(ctx, jActor(p), journal.MeasurementInput{
		PlantID:      in.PlantID,
		QuantityKind: string(in.QuantityKind),
		Value:        string(in.Value),
		Unit:         string(in.Unit),
		ObservedAt:   time.Time(in.ObservedAt),
		Method:       string(in.Method),
		Uncertainty:  decPtr(in.Uncertainty),
		ActivityID:   in.ActivityID,
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.RecordMeasurementPayload{Problems: mapJournalProblems(problems), ClientMutationID: c}
	if len(problems) == 0 {
		payload.Measurement = measurementOut(v)
	}
	return payload, nil
}

func (m mutationResolver) UpdateMeasurement(ctx context.Context, in gql.UpdateMeasurementInput, c *string) (*gql.UpdateMeasurementPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, problems, err := m.r.Journal.UpdateMeasurement(ctx, jActor(p), journal.MeasurementUpdate{
		ID:               in.ID,
		ExpectedRevision: int32(in.ExpectedRevision),
		Value:            decPtr(in.Value),
		Uncertainty:      decPtr(in.Uncertainty),
		ObservedAt:       dtPtr(in.ObservedAt),
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.UpdateMeasurementPayload{Problems: mapJournalProblems(problems), ClientMutationID: c}
	if len(problems) == 0 {
		payload.Measurement = measurementOut(v)
	}
	return payload, nil
}

func (m mutationResolver) DeleteMeasurement(ctx context.Context, in gql.DeleteMeasurementInput, c *string) (*gql.DeleteMeasurementPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	deleted, problems, err := m.r.Journal.DeleteMeasurement(ctx, jActor(p), in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.DeleteMeasurementPayload{Problems: mapJournalProblems(problems), ClientMutationID: c}
	if len(problems) == 0 {
		payload.DeletedMeasurementID = &deleted
	}
	return payload, nil
}
