// AI analysis GraphQL surface (T29): run polling, capability/budget
// settings reads, and the request/cancel mutations. T33 added the
// suggestion surfaces (suggestions/acceptSuggestion/dismissSuggestion)
// and run.suggestions hydration — advice rows are real, never fabricated.
package graphql

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vektah/gqlparser/v2/gqlerror"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/insights"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

func insightsActor(p httpx.Principal) insights.Actor {
	return insights.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}

func mapInsightsProblems(ps []insights.Problem) []*gql.MutationProblem {
	out := make([]*gql.MutationProblem, 0, len(ps))
	for _, p := range ps {
		mp := &gql.MutationProblem{Code: gql.ProblemCode(p.Code), Message: p.Message}
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

// ---- run output conversion -------------------------------------------------

// runOut converts a stored run row into the contract shape. Evidence is
// reconstructed from the frozen input snapshot (what was actually sent);
// result is parsed from the validated canonical JSON the worker stored,
// mapped by run kind. Suggestions (T33) are hydrated from the advice
// rows the run produced — empty for kinds that never advise.
func (r *Resolver) runOut(ctx context.Context, run dbgen.AnalysisRun) *gql.AnalysisRun {
	out := &gql.AnalysisRun{
		ID:          uuidString(run.ID),
		Revision:    int(run.Revision),
		Kind:        gql.AnalysisKind(run.Kind),
		State:       gql.AnalysisRunState(run.State),
		Attempts:    int(run.Attempts),
		RequestedAt: tsOf(run.RequestedAt),
		StartedAt:   optTS(run.StartedAt),
		FinishedAt:  optTS(run.FinishedAt),
		Suggestions: []*gql.Suggestion{},
	}
	if r.Insights != nil && run.ID.Valid {
		rows, err := r.Insights.RunSuggestions(ctx, insights.Actor{WorkspaceID: run.WorkspaceID}, run.ID)
		if err != nil {
			r.Log.Error("insights: run suggestions read failed", "run_id", out.ID)
		}
		for _, s := range rows {
			out.Suggestions = append(out.Suggestions, mapSuggestion(s))
		}
	}
	if run.PlantID.Valid {
		s := uuidString(run.PlantID)
		out.PlantID = &s
	}
	if run.ReceiptAssetID.Valid {
		s := uuidString(run.ReceiptAssetID)
		out.ReceiptAssetID = &s
	}
	if run.FailureCode.Valid {
		c := gql.AnalysisFailureCode(run.FailureCode.AnalysisFailureCode)
		out.FailureCode = &c
	}
	if run.FailureMessage.Valid {
		m := run.FailureMessage.String
		out.FailureMessage = &m
	}
	for _, p := range []struct {
		src pgtype.Text
		dst **string
	}{
		{run.Provider, &out.Provider}, {run.Model, &out.Model},
		{run.PromptVersion, &out.PromptVersion}, {run.ResultSchemaVersion, &out.ResultSchemaVersion},
	} {
		if p.src.Valid {
			s := p.src.String
			*p.dst = &s
		}
	}
	if run.CostReservedMicroUsd.Valid {
		s := strconv.FormatInt(run.CostReservedMicroUsd.Int64, 10)
		out.CostReservedMicroUsd = &s
	}
	if run.CostActualMicroUsd.Valid {
		s := strconv.FormatInt(run.CostActualMicroUsd.Int64, 10)
		out.CostActualMicroUsd = &s
	}
	if snap, err := insights.ParseSnapshot(run.InputSnapshot); err == nil {
		out.Evidence = &gql.AnalysisEvidence{
			ActivityIds:        idsOf(snap.Activities, func(a insights.SnapActivity) string { return a.ID }),
			MeasurementIds:     idsOf(snap.Measurements, func(m insights.SnapMeasure) string { return m.ID }),
			PhotoIds:           idsOf(snap.Photos, func(p insights.SnapPhoto) string { return p.ID }),
			SourceVersionIds:   idsOf(snap.Guidance, func(g insights.SnapGuidance) string { return g.SourceVersionID }),
			WeatherSnapshotIds: idsOf(snap.Weather, func(w insights.SnapWeather) string { return w.ID }),
			MissingFacts:       snap.MissingFacts,
			InputHash:          run.InputHash,
		}
	} else {
		r.Log.Error("insights: stored snapshot unreadable", "run_id", out.ID)
		out.Evidence = &gql.AnalysisEvidence{MissingFacts: []string{"stored input snapshot unreadable"}, InputHash: run.InputHash}
	}
	if run.State == dbgen.AnalysisRunStateSUCCEEDED && len(run.Result) > 0 {
		if res, err := parseStoredResult(run.Kind, run.Result); err == nil {
			out.Result = res
		} else {
			// Stored results were validated on write; unreadable result JSON
			// is an integrity signal, not user data — log and omit.
			r.Log.Error("insights: stored result unreadable", "run_id", out.ID, "err", err)
		}
	}
	return out
}

func idsOf[T any](items []T, id func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, id(it))
	}
	return out
}

// parseStoredResult maps the canonical validated JSON to the contract
// result union by run kind.
func parseStoredResult(kind dbgen.AnalysisKind, raw []byte) (gql.AnalysisResult, error) {
	switch kind {
	case dbgen.AnalysisKindTEXT, dbgen.AnalysisKindVISION, dbgen.AnalysisKindSYNTHESIS:
		var v insights.ObsResult
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		res := &gql.ObservationAnalysisResult{
			VisibleObservations: []*gql.Observation{},
			Hypotheses:          []*gql.Hypothesis{},
			MissingInformation:  v.MissingInformation,
			FollowUpChecks:      v.FollowUpChecks,
			Citations:           []*gql.Citation{},
		}
		for _, o := range v.VisibleObservations {
			res.VisibleObservations = append(res.VisibleObservations,
				&gql.Observation{Text: o.Text, EvidenceIds: nz(o.EvidenceIDs)})
		}
		for _, h := range v.Hypotheses {
			res.Hypotheses = append(res.Hypotheses, &gql.Hypothesis{
				Text: h.Text, Confidence: gql.Confidence(h.Confidence), EvidenceIds: nz(h.EvidenceIDs)})
		}
		for _, c := range v.Citations {
			cit := &gql.Citation{
				SourceID: c.SourceID, SourceVersionID: c.SourceVersionID, Title: c.Title,
			}
			if c.Publisher != "" {
				s := c.Publisher
				cit.Publisher = &s
			}
			if c.RetrievedOn != "" {
				d := scalars.Date(c.RetrievedOn)
				cit.RetrievedOn = &d
			}
			if c.Region != "" {
				s := c.Region
				cit.Region = &s
			}
			if c.License != "" {
				s := c.License
				cit.License = &s
			}
			res.Citations = append(res.Citations, cit)
		}
		return res, nil
	case dbgen.AnalysisKindIDENTIFICATION:
		var v insights.IdentResult
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		res := &gql.IdentificationResult{
			Candidates:        []*gql.IdentificationCandidate{},
			CultivarEvidenced: v.CultivarEvidenced,
		}
		if v.Cultivar != "" {
			s := v.Cultivar
			res.Cultivar = &s
		}
		for _, c := range v.Candidates {
			cand := &gql.IdentificationCandidate{
				Rank: c.Rank, ScientificName: c.ScientificName, Confidence: gql.Confidence(c.Confidence),
			}
			if c.CommonName != "" {
				s := c.CommonName
				cand.CommonName = &s
			}
			if c.EvidenceNote != "" {
				s := c.EvidenceNote
				cand.EvidenceNote = &s
			}
			res.Candidates = append(res.Candidates, cand)
		}
		return res, nil
	case dbgen.AnalysisKindRECEIPTEXTRACTION:
		var v insights.ReceiptResult
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		res := &gql.ReceiptExtractionResult{
			Lines:               []*gql.ExtractedLine{},
			NeedsReconciliation: v.NeedsReconciliation,
		}
		for _, f := range []struct {
			src string
			dst **string
		}{
			{v.Currency, &res.Currency}, {v.TotalMinor, &res.TotalMinor},
			{v.ShippingMinor, &res.ShippingMinor}, {v.TaxMinor, &res.TaxMinor},
			{v.Seller, &res.Seller},
		} {
			if f.src != "" {
				s := f.src
				*f.dst = &s
			}
		}
		if v.PurchasedOn != "" {
			d := scalars.Date(v.PurchasedOn)
			res.PurchasedOn = &d
		}
		for _, l := range v.Lines {
			line := &gql.ExtractedLine{Description: l.Description}
			if l.AmountMinor != "" {
				s := l.AmountMinor
				line.AmountMinor = &s
			}
			res.Lines = append(res.Lines, line)
		}
		return res, nil
	}
	return nil, &insights.ValidationError{Msg: "unknown kind"}
}

func nz(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// ---- Query resolvers -------------------------------------------------------

func (q queryResolver) AnalysisRun(ctx context.Context, id string) (*gql.AnalysisRun, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Insights == nil {
		return nil, nil // no analysis lane: behaves as NOT_FOUND
	}
	run, err := q.r.Insights.GetRun(ctx, p.WorkspaceID, id)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if run == nil {
		return nil, nil
	}
	return q.r.runOut(ctx, *run), nil
}

func (q queryResolver) ProviderCapabilities(ctx context.Context) (*gql.ProviderCapabilities, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Insights == nil {
		return nil, capabilityUnavailable("providerCapabilities")
	}
	caps, view, err := q.r.Insights.Capabilities(ctx, p.WorkspaceID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	capOf := func(c insights.Capability) *gql.ProviderCapability {
		st := caps[c]
		out := &gql.ProviderCapability{
			Available: st.Available, ExternalData: st.ExternalData,
		}
		if st.Model != "" {
			m := st.Model
			out.Model = &m
		}
		if !st.Available && st.Reason != "" {
			r := st.Reason
			out.UnavailableReason = &r
		}
		return out
	}
	budget := &gql.AnalysisBudgetStatus{
		DailyRunsUsed:         int(view.DailyRunsUsed),
		DailyRunLimit:         int(view.DailyRunLimit),
		ConcurrentRunLimit:    int(view.ConcurrentRunLimit),
		MonthUtc:              view.MonthUTC,
		MonthReservedMicroUsd: strconv.FormatInt(view.MonthEncumberedMicroUSD, 10),
		MonthSpentMicroUsd:    strconv.FormatInt(view.MonthSpentMicroUSD, 10),
	}
	if view.MonthCeilingMicroUSD > 0 {
		s := strconv.FormatInt(view.MonthCeilingMicroUSD, 10)
		budget.MonthCeilingMicroUsd = &s
	}
	return &gql.ProviderCapabilities{
		Text:              capOf(insights.CapTextAnalyzer),
		Vision:            capOf(insights.CapVisionAnalyzer),
		Identification:    capOf(insights.CapPlantIdentifier),
		ReceiptExtraction: capOf(insights.CapReceiptExtractor),
		Budget:            budget,
	}, nil
}

// Suggestions pages the owner's advice rows (T33): newest first, keyset
// cursors bound to the filter signature.
func (q queryResolver) Suggestions(ctx context.Context, filter *gql.SuggestionFilter, first *int, after *string) (*gql.SuggestionConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Insights == nil {
		return nil, capabilityUnavailable("suggestions")
	}
	qz := insights.SuggestionQuery{After: after}
	if first != nil {
		qz.First = int32(*first)
	}
	if filter != nil {
		if filter.PlantID != nil {
			id, ok := journalUUID(*filter.PlantID)
			if !ok {
				return nil, &gqlerror.Error{
					Message:    "plantId must be a UUID.",
					Extensions: map[string]any{"code": "VALIDATION"},
				}
			}
			qz.PlantID = &id
		}
		for _, s := range filter.Status {
			qz.Statuses = append(qz.Statuses, string(s))
		}
		for _, k := range filter.Kinds {
			qz.Kinds = append(qz.Kinds, string(k))
		}
	}
	page, probs, err := q.r.Insights.ListSuggestions(ctx, insightsActor(p), qz)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return nil, &gqlerror.Error{
			Message:    probs[0].Message,
			Extensions: map[string]any{"code": probs[0].Code},
		}
	}
	items := make([]*gql.Suggestion, 0, len(page.Items))
	for _, s := range page.Items {
		items = append(items, mapSuggestion(s))
	}
	pi := &gql.PageInfo{HasNextPage: page.HasNext}
	if page.EndCursor != nil {
		ec := *page.EndCursor
		pi.EndCursor = &ec
	}
	return &gql.SuggestionConnection{Items: items, PageInfo: pi}, nil
}

// ---- Mutation resolvers ----------------------------------------------------

func (m mutationResolver) RequestAnalysis(ctx context.Context, in gql.RequestAnalysisInput, clientMutationID *string) (*gql.RequestAnalysisPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Insights == nil {
		return nil, capabilityUnavailable("requestAnalysis")
	}
	svcIn := insights.RequestInput{Kind: string(in.Kind)}
	if in.PlantID != nil {
		svcIn.PlantID = *in.PlantID
	}
	if in.ReceiptAssetID != nil {
		svcIn.ReceiptAssetID = *in.ReceiptAssetID
	}
	svcIn.PhotoIDs = in.PhotoIds
	if in.ForceRerun != nil {
		svcIn.ForceRerun = *in.ForceRerun
	}
	run, probs, err := m.r.Insights.Request(ctx, insightsActor(p), svcIn, clientMutationID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	pl := &gql.RequestAnalysisPayload{Problems: mapInsightsProblems(probs), ClientMutationID: clientMutationID}
	// IDEMPOTENCY_CONFLICT carries the existing run alongside the problem.
	if len(probs) == 0 || run.ID.Valid {
		pl.Run = m.r.runOut(ctx, run)
	}
	return pl, nil
}

func (m mutationResolver) CancelAnalysis(ctx context.Context, in gql.CancelAnalysisInput, clientMutationID *string) (*gql.CancelAnalysisPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Insights == nil {
		return nil, capabilityUnavailable("cancelAnalysis")
	}
	run, probs, err := m.r.Insights.Cancel(ctx, insightsActor(p), in.RunID, int32(in.ExpectedRevision), clientMutationID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	pl := &gql.CancelAnalysisPayload{Problems: mapInsightsProblems(probs), ClientMutationID: clientMutationID}
	// CONFLICT carries the current (terminal or newer) run + revision.
	if len(probs) == 0 || run.ID.Valid {
		pl.Run = m.r.runOut(ctx, run)
	}
	return pl, nil
}

// AcceptSuggestion applies the frozen accept contract (T33): exactly one
// one-off task on success, CONFLICT on revision moves, VALIDATION for
// expired rows or a missing care plan, and the REVIEW_REQUIRED
// confirmation touch for advice whose source or context changed.
func (m mutationResolver) AcceptSuggestion(ctx context.Context, in gql.AcceptSuggestionInput, clientMutationID *string) (*gql.AcceptSuggestionPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Insights == nil {
		return nil, capabilityUnavailable("acceptSuggestion")
	}
	svcIn := insights.AcceptInput{
		SuggestionID:     in.SuggestionID,
		ExpectedRevision: int32(in.ExpectedRevision),
	}
	if in.DueOn != nil {
		d, err := time.Parse("2006-01-02", string(*in.DueOn))
		if err != nil {
			return nil, &gqlerror.Error{
				Message:    "dueOn must be a calendar date (YYYY-MM-DD).",
				Extensions: map[string]any{"code": "VALIDATION"},
			}
		}
		svcIn.DueOn = &d
	}
	sugg, probs, err := m.r.Insights.Accept(ctx, insightsActor(p), svcIn, clientMutationID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	pl := &gql.AcceptSuggestionPayload{Problems: mapInsightsProblems(probs), ClientMutationID: clientMutationID}
	if sugg.ID.Valid {
		pl.Suggestion = mapSuggestion(sugg)
		if occ := m.acceptedOccurrence(ctx, p.WorkspaceID, sugg); occ != nil {
			pl.Occurrence = occ
		}
	}
	return pl, nil
}

// acceptedOccurrence loads the task an acceptance created (payload
// convenience; the suggestion's acceptedTaskId stays the durable link).
func (m mutationResolver) acceptedOccurrence(ctx context.Context, ws pgtype.UUID, sugg dbgen.Suggestion) *gql.CareOccurrence {
	if !sugg.AcceptedTaskID.Valid {
		return nil
	}
	occ, err := m.r.DB.GetCareOccurrence(ctx, sugg.AcceptedTaskID)
	if err != nil || occ.WorkspaceID != ws {
		m.r.Log.Error("insights: accepted task unreadable", "suggestion_id", uuidString(sugg.ID))
		return nil
	}
	out := &gql.CareOccurrence{
		ID: uuidString(occ.ID), PlanID: uuidString(occ.PlanID),
		PlantID:         uuidString(sugg.PlantID),
		RuleKey:         occ.RuleKey,
		Kind:            gql.RuleKind(occ.Kind),
		OccurrenceKey:   occ.OccurrenceKey,
		OriginalDueDate: dateOf(occ.OriginalDueDate),
		CurrentDueDate:  dateOf(occ.CurrentDueDate),
		Status:          gql.OccurrenceStatus(occ.Status),
		PlanTimezone:    occ.PlanTimezone,
	}
	if occ.ActivityKind.Valid {
		ak := gql.ActivityKind(occ.ActivityKind.ActivityKind)
		out.ActivityKind = &ak
	}
	return out
}

// DismissSuggestion records the owner's rejection with an optional
// bounded reason; dismissal is final for PROPOSED rows only.
func (m mutationResolver) DismissSuggestion(ctx context.Context, in gql.DismissSuggestionInput, clientMutationID *string) (*gql.DismissSuggestionPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Insights == nil {
		return nil, capabilityUnavailable("dismissSuggestion")
	}
	svcIn := insights.DismissInput{
		SuggestionID:     in.SuggestionID,
		ExpectedRevision: int32(in.ExpectedRevision),
		Reason:           in.Reason,
	}
	sugg, probs, err := m.r.Insights.Dismiss(ctx, insightsActor(p), svcIn, clientMutationID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	pl := &gql.DismissSuggestionPayload{Problems: mapInsightsProblems(probs), ClientMutationID: clientMutationID}
	if sugg.ID.Valid {
		pl.Suggestion = mapSuggestion(sugg)
	}
	return pl, nil
}

// ---- suggestion output ------------------------------------------------------

// mapSuggestion converts a stored advice row into the contract shape;
// jsonb text arrays degrade to empty rather than breaking the page.
func mapSuggestion(s dbgen.Suggestion) *gql.Suggestion {
	out := &gql.Suggestion{
		ID: uuidString(s.ID), Revision: int(s.Revision), PlantID: uuidString(s.PlantID),
		Kind: gql.SuggestionKind(s.Kind), Severity: gql.SuggestionSeverity(s.Severity),
		Status: gql.SuggestionStatus(s.Status), Action: s.Action,
		EvidenceIds: jsonStrings(s.EvidenceIds), MissingFacts: jsonStrings(s.MissingFacts),
		CreatedAt: tsOf(s.CreatedAt), NeedsReview: s.NeedsReview,
	}
	if s.RunID.Valid {
		v := uuidString(s.RunID)
		out.RunID = &v
	}
	if s.Observation.Valid {
		v := s.Observation.String
		out.Observation = &v
	}
	if s.Hypothesis.Valid {
		v := s.Hypothesis.String
		out.Hypothesis = &v
	}
	if s.ExpiresAt.Valid {
		d := tsOf(s.ExpiresAt)
		out.ExpiresAt = &d
	}
	if s.AcceptedTaskID.Valid {
		v := uuidString(s.AcceptedTaskID)
		out.AcceptedTaskID = &v
	}
	if s.DismissReason.Valid {
		v := s.DismissReason.String
		out.DismissReason = &v
	}
	return out
}

func jsonStrings(raw []byte) []string {
	out := []string{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}
