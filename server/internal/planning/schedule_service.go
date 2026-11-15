package planning

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func normalizedScheduleAssumptions(in ScheduleAssumptions) ScheduleAssumptions {
	out := ScheduleAssumptions{Anchors: append([]ScheduleAnchor{}, in.Anchors...)}
	if in.Hemisphere != nil {
		v := *in.Hemisphere
		out.Hemisphere = &v
	}
	sort.Slice(out.Anchors, func(i, j int) bool { return out.Anchors[i].Kind < out.Anchors[j].Kind })
	return out
}
func latitudeHemisphere(latitude *pgtype.Numeric) (string, error) {
	if latitude == nil || !latitude.Valid {
		return "", nil
	}
	value, err := latitude.Float64Value()
	if err != nil {
		return "", err
	}
	if value.Float64 > 0 {
		return "NORTHERN", nil
	}
	if value.Float64 < 0 {
		return "SOUTHERN", nil
	}
	return "", nil // Equator alone does not establish a local seasonal model.
}

// Uses the supplied queries inside confirmation's existing transaction: no
// nested pool borrow while holding a plan lock, and no provider/network work.
func scheduleGeography(ctx context.Context, q *dbgen.Queries, row dbgen.PlantingPlan, assumptions ScheduleAssumptions) (ScheduleAssumptions, string, error) {
	if validateScheduleAssumptions(assumptions) != nil {
		return ScheduleAssumptions{}, "", ErrInvalidSchedule
	}
	settings, err := q.KnowledgeSettings(ctx, row.WorkspaceID)
	if err != nil {
		return ScheduleAssumptions{}, "", err
	}
	latitude := settings.Latitude
	if row.IntendedLocationID.Valid {
		location, err := q.GetLocation(ctx, dbgen.GetLocationParams{WorkspaceID: row.WorkspaceID, ID: row.IntendedLocationID})
		if err != nil {
			return ScheduleAssumptions{}, "", err
		}
		if location.Latitude != nil && location.Latitude.Valid {
			latitude = location.Latitude
		}
	}
	hemi, err := latitudeHemisphere(latitude)
	if err != nil {
		return ScheduleAssumptions{}, "", err
	}
	out := normalizedScheduleAssumptions(assumptions)
	if hemi != "" {
		if out.Hemisphere != nil && *out.Hemisphere != hemi {
			return ScheduleAssumptions{}, "", ErrInvalidSchedule
		}
		out.Hemisphere = &hemi
	}
	return out, settings.Timezone, nil
}
func DecodeScheduleSelection(raw []byte) (*ScheduleSelection, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if len(raw) > 8192 {
		return nil, ErrInvalidSchedule
	}
	var selection ScheduleSelection
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&selection); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, ErrInvalidSchedule
	}
	if selection.Version != ScheduleVersion || selection.FactID == "" || len(selection.FactID) > 200 || selection.WindowIndex < 0 || selection.WindowIndex >= MaxGuidanceWindows || !validYear(selection.Year) || selection.SupportedPeriod == nil || len(ValidatePeriod(selection.SupportedPeriod)) > 0 || selection.ConfirmedAt.IsZero() || validateScheduleAssumptions(selection.Assumptions) != nil {
		return nil, ErrInvalidSchedule
	}
	return &selection, nil
}
func sourceValidatedForSchedule(f GuidanceFact) bool {
	return f.SourceID != "" && len(f.SourceID) <= 200 && f.ExtractionVersion > 0 && sha256Pattern.MatchString(f.ExtractionModel) && len(f.Evidence) >= 10 && len(f.Evidence) <= 500 && len(f.TimingText) <= 500 && strings.Contains(f.Evidence, f.TimingText)
}
func snapshotCurrentForSchedule(s *Service, row *dbgen.PlantingGuidanceSnapshot, c *GuidanceContext) bool {
	return row != nil && row.State == "READY" && row.RetrievedAt.Valid && c != nil && c.State == "PENDING" && row.ContextSha256 == c.Fingerprint && !row.CreatedAt.Time.Before(s.Clock.Now().Add(-30*24*time.Hour))
}

// Schedule reads one owned aggregate and projects it. It never refreshes a
// source, inserts a job, changes chosen dates or calls Guidance.Research.
func (s *Service) Schedule(ctx context.Context, a Actor, in ScheduleQuery) (*SchedulePage, error) {
	if in.First < 1 || in.First > 100 || validateScheduleRange(in.Range) != nil || validateScheduleAssumptions(in.Assumptions) != nil {
		return nil, ErrInvalidSchedule
	}
	plan, err := s.Get(ctx, a, in.PlanID)
	if err != nil || plan == nil {
		return nil, err
	}
	return s.schedulePlan(ctx, a, *plan, in)
}

// Page composition reuses its already hydrated aggregates, avoiding a second
// plan/actions/snapshots read per row. Provider context inspection stays local.
func (s *Service) schedulePlan(ctx context.Context, a Actor, plan Plan, in ScheduleQuery) (*SchedulePage, error) {
	selection, err := DecodeScheduleSelection(plan.Row.AcceptedSchedule)
	if err != nil {
		return nil, err
	}
	var c *GuidanceContext
	if s.Guidance != nil {
		value, err := s.Guidance.Context(ctx, a.WorkspaceID, subjectFromRow(plan.Row))
		if err != nil {
			return nil, err
		}
		c = &value
	}
	provided := in.Assumptions
	usingSaved := false
	if !in.AssumptionsExplicit && len(provided.Anchors) == 0 && provided.Hemisphere == nil && selection != nil && c != nil && plan.AcceptedGuidance != nil && plan.AcceptedGuidance.ContextSha256 == c.Fingerprint {
		provided = selection.Assumptions
		usingSaved = true
	}
	assumptions, timezone, err := scheduleGeography(ctx, s.q(), plan.Row, provided)
	if usingSaved && errors.Is(err, ErrInvalidSchedule) {
		// An automatic default must not block the user's manual plan when known
		// site geography conflicts with old assumptions. Explicit inputs still fail.
		assumptions, timezone, err = scheduleGeography(ctx, s.q(), plan.Row, in.Assumptions)
	}
	if err != nil {
		return nil, err
	}
	zone, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	out := &SchedulePage{PlanID: idString(plan.Row.ID), Revision: plan.Row.Revision, Generation: plan.Row.GuidanceGeneration, Timezone: timezone, Today: s.Clock.Now().In(zone).Format(time.DateOnly), TargetPeriod: PeriodFromRow(plan.Row), Selection: selection, Assumptions: assumptions, GuidanceState: "PENDING", Message: "Waiting for current regional guidance."}
	if selection != nil {
		out.TargetHemisphere = selection.Assumptions.Hemisphere
	} else {
		out.TargetHemisphere = assumptions.Hemisphere
	}
	facts := []GuidanceFact{}
	if snapshot := plan.LatestGuidance; snapshot != nil {
		id := idString(snapshot.ID)
		out.SourceSnapshotID = &id
		out.GuidanceState, out.Message = string(snapshot.State), snapshot.Message
		out.GuidanceCurrent = snapshotCurrentForSchedule(s, snapshot, c)
		if out.GuidanceCurrent {
			facts, err = DecodeGuidanceFacts(snapshot.Facts)
			if err != nil {
				return nil, err
			}
			for _, fact := range facts {
				if !sourceValidatedForSchedule(fact) {
					out.GuidanceCurrent = false
					out.Message = "Refresh guidance to obtain verified schedule evidence. Your chosen period is preserved."
					break
				}
			}
		}
		if !out.GuidanceCurrent && snapshot.State == "READY" {
			out.Message = "Regional guidance needs a verified refresh. Your chosen period is preserved."
			facts = []GuidanceFact{}
		}
	}
	out.ScheduleProjection, err = ProjectSchedule(ScheduleInput{Range: in.Range, Assumptions: assumptions, Today: out.Today, Method: string(plan.Row.Method.PlantingMethod), Target: out.TargetPeriod, TargetHemisphere: out.TargetHemisphere, Facts: facts})
	if err != nil {
		return nil, err
	}
	sourceScope := ""
	if snapshot := plan.LatestGuidance; snapshot != nil {
		sourceScope, _ = requestHash([]any{snapshot.ID, snapshot.Generation, snapshot.ContextSha256, snapshot.State, snapshot.Facts})
	}
	scope, err := requestHash([]any{ScheduleVersion, idString(a.WorkspaceID), out.PlanID, out.Revision, out.Generation, sourceScope, c, in.Range, assumptions, out.Today, out.TargetHemisphere})
	if err != nil {
		return nil, err
	}
	start, err := scheduleCursorOffset(in.After, scope, len(out.Windows))
	if err != nil {
		return nil, err
	}
	end := min(start+in.First, len(out.Windows))
	out.HasNext = end < len(out.Windows)
	out.Windows = out.Windows[start:end]
	if end > start {
		raw, _ := json.Marshal(scheduleCursor{Scope: scope, Offset: end})
		cursor := base64.RawURLEncoding.EncodeToString(raw)
		out.EndCursor = &cursor
	}
	return out, nil
}

type scheduleCursor struct {
	Scope  string `json:"scope"`
	Offset int    `json:"offset"`
}

func scheduleCursorOffset(raw *string, scope string, total int) (int, error) {
	if raw == nil {
		return 0, nil
	}
	if len(*raw) > 1024 {
		return 0, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(*raw)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	var cursor scheduleCursor
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&cursor); err != nil || cursor.Scope != scope || cursor.Offset < 1 || cursor.Offset > total || cursor.Offset > MaxProjectedWindows {
		return 0, ErrInvalidCursor
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return 0, ErrInvalidCursor
	}
	return cursor.Offset, nil
}

func (s *Service) ConfirmSchedule(ctx context.Context, a Actor, in ConfirmScheduleInput, key *string) (Result, []Problem, error) {
	if in.TargetPeriod == nil || len(ValidatePeriod(in.TargetPeriod)) > 0 || validateScheduleAssumptions(in.Assumptions) != nil || in.WindowIndex < 0 || in.WindowIndex >= MaxGuidanceWindows || !validYear(in.Year) || in.Generation < 1 {
		return Result{}, invalid("input", "Choose a valid supported occurrence, assumptions and target period."), nil
	}
	snapshotID, ok := parseID(in.SnapshotID)
	if !ok {
		return Result{}, invalid("input.snapshotId", "Choose a valid source snapshot."), nil
	}
	in.SnapshotID = idString(snapshotID)
	in.Assumptions = normalizedScheduleAssumptions(in.Assumptions)
	// Configuration inspection is local and occurs before borrowing the mutation
	// transaction, so concurrent confirmations cannot exhaust a small pool.
	description, err := s.describeStoredGuidance(ctx, a, in.PlanID)
	if err != nil {
		return Result{}, nil, err
	}
	return s.mutate(ctx, a, "confirmPlantingSchedule", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		row, ps, err := lockPlan(ctx, q, a, in.PlanID, in.ExpectedRevision, "input.planId")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		if !active(row.State) {
			return Result{}, invalid("input.planId", "Choose a schedule on an active plan."), nil
		}
		snapshot, err := q.GetCurrentPlanningGuidance(ctx, dbgen.GetCurrentPlanningGuidanceParams{WorkspaceID: a.WorkspaceID, PlanID: row.ID, Generation: row.GuidanceGeneration})
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, invalid("input.snapshotId", "Refresh and review current guidance before confirming."), nil
		}
		if err != nil {
			return Result{}, nil, err
		}
		if idString(snapshot.ID) != in.SnapshotID || snapshot.Generation != in.Generation || !snapshotCurrentForSchedule(s, &snapshot, description) {
			return Result{}, []Problem{{Code: "CONFLICT", Field: "input.snapshotId", Message: "Guidance or its context changed. Refresh and review before choosing dates."}}, nil
		}
		facts, err := DecodeGuidanceFacts(snapshot.Facts)
		if err != nil {
			return Result{}, nil, err
		}
		var selected *GuidanceFact
		for i := range facts {
			if facts[i].ID == in.FactID {
				if selected != nil {
					return Result{}, invalid("input.factId", "The source fact is ambiguous; refresh guidance."), nil
				}
				selected = &facts[i]
			}
		}
		if selected == nil || !sourceValidatedForSchedule(*selected) || in.WindowIndex >= len(selected.Windows) {
			return Result{}, invalid("input.factId", "Choose a verified source window from current guidance."), nil
		}
		assumptions, timezone, err := scheduleGeography(ctx, q, row, in.Assumptions)
		if errors.Is(err, ErrInvalidSchedule) {
			return Result{}, invalid("input.assumptions", "Review the anchor dates and hemisphere for this site."), nil
		}
		if err != nil {
			return Result{}, nil, err
		}
		window := selected.Windows[in.WindowIndex]
		if assumptions.Hemisphere == nil && window.Hemisphere != nil {
			v := *window.Hemisphere
			assumptions.Hemisphere = &v
		}
		zone, err := time.LoadLocation(timezone)
		if err != nil {
			return Result{}, nil, err
		}
		projected := projectGuidanceWindow(*selected, in.WindowIndex, in.Year, assumptions, s.Clock.Now().In(zone).Format(time.DateOnly), string(row.Method.PlantingMethod))
		if projected.Resolution != "RESOLVED" || !projected.Selectable || !periodContained(in.TargetPeriod, projected.Period, hemisphereValue(assumptions.Hemisphere)) {
			return Result{}, invalid("input.targetPeriod", "Choose a period inside this resolved planting window for the plan's starting method."), nil
		}
		selection := ScheduleSelection{Version: ScheduleVersion, FactID: in.FactID, WindowIndex: in.WindowIndex, Year: in.Year, Assumptions: assumptions, SupportedPeriod: projected.Period, ConfirmedAt: s.Clock.Now().UTC()}
		raw, err := json.Marshal(selection)
		if err != nil {
			return Result{}, nil, err
		}
		if _, err = DecodeScheduleSelection(raw); err != nil || len(raw) > 8192 {
			return Result{}, invalid("input", "The schedule selection exceeded its supported bounds."), nil
		}
		params := dbgen.InsertPlantingPlanParams{}
		putPeriod(&params, in.TargetPeriod)
		old := row
		row, err = q.ConfirmPlanningSchedule(ctx, dbgen.ConfirmPlanningScheduleParams{WorkspaceID: a.WorkspaceID, ID: row.ID, SnapshotID: snapshot.ID, AcceptedSchedule: raw, TargetPrecision: params.TargetPrecision, TargetFrom: params.TargetFrom, TargetTo: params.TargetTo, TargetFromMonth: params.TargetFromMonth, TargetToMonth: params.TargetToMonth, TargetYear: params.TargetYear, TargetSeason: params.TargetSeason})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "confirmPlantingSchedule", row, old); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
