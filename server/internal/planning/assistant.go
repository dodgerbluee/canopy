package planning

import (
	"bytes"
	dbgen "canopy/server/db/generated"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgtype"
	"io"
	"strings"
	"unicode/utf8"
)

var ErrAssistantUnavailable = errors.New("The Garden AI connection is unavailable. Review AI connections in Settings.")
var ErrAssistantAnswer = errors.New("The planning answer could not be verified. Try a more specific question; your saved plans are unchanged.")
var ErrAssistantChanged = errors.New("A selected plan or its guidance changed while answering. Ask again using the latest version.")

type AssistantProvider interface {
	AnswerPlanning(context.Context, pgtype.UUID, AssistantContext) (AssistantDraft, error)
}
type AssistantQuery struct {
	Question string
	PlanIDs  []string
	Year     int
}
type AssistantAction struct{ Kind, Title, On, State string }
type AssistantPlan struct {
	Ref, Name, State, Method         string
	ScientificName, Cultivar, Notes  *string
	IntendedPlace, IntendedPlacement string
	DesiredQuantity                  *int32
	EstimatedBudget                  *string
	AlreadyInGarden                  bool
	ChosenPeriod                     *Period
	GuidanceCurrent                  bool
	GuidanceMessage                  string
	Windows                          []ProjectedWindow
	ProjectionLimited                bool
	Actions                          []AssistantAction
}
type AssistantSource struct {
	Ref, PlanRef string
	Fact         GuidanceFact
}
type AssistantContext struct {
	Question, Today, Timezone string
	Year                      int
	ProposedPlans             []AssistantPlan
	Sources                   []AssistantSource
}

// Short refs are local to this one request; only supplied refs may return.
type AssistantDraft struct {
	Answer       string   `json:"answer"`
	PlanRefs     []string `json:"planRefs"`
	SourceRefs   []string `json:"sourceRefs"`
	NotePlanRef  string   `json:"notePlanRef"`
	ProposedNote string   `json:"proposedNote"`
}
type AssistantCitation struct {
	Ref, PlanID, PlanName string
	Fact                  GuidanceFact
}

func (d *AssistantDraft) UnmarshalJSON(raw []byte) error {
	type value AssistantDraft
	var parsed value
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrAssistantAnswer
	}
	*d = AssistantDraft(parsed)
	return nil
}

type AssistantNote struct {
	PlanID           string
	ExpectedRevision int32
	Text             string
}
type AssistantReply struct {
	Answer  string
	IsAI    bool
	PlanIDs []string
	Sources []AssistantCitation
	Note    *AssistantNote
}

func estimatedBudget(row dbgen.PlantingPlan) *string {
	if !row.BudgetMinor.Valid {
		return nil
	}
	exp := 2
	switch row.BudgetCurrency.String {
	case "JPY", "KRW", "VND", "CLP", "ISK":
		exp = 0
	case "BHD", "KWD", "OMR", "TND", "JOD", "LYD":
		exp = 3
	}
	// Presentation matches the existing exact-money UI; no floating arithmetic.
	divisor := int64(1)
	for range exp {
		divisor *= 10
	}
	value := fmt.Sprintf("%d", row.BudgetMinor.Int64)
	if exp > 0 {
		value = fmt.Sprintf("%d.%0*d", row.BudgetMinor.Int64/divisor, exp, row.BudgetMinor.Int64%divisor)
	}
	label := value + " " + row.BudgetCurrency.String + " (estimate, not actual spending)"
	return &label
}
func optionalText(in pgtype.Text) *string {
	if !in.Valid {
		return nil
	}
	v := in.String
	return &v
}
func assistantScope(p Plan, s *SchedulePage) string {
	value, _ := requestHash([]any{p.Row.Revision, p.Row.GuidanceGeneration, p.Fulfillment, s.SourceSnapshotID, s.GuidanceCurrent, s.Timezone, s.Today, s.Assumptions})
	return value
}
func (s *Service) AskPlanner(ctx context.Context, a Actor, in AssistantQuery) (AssistantReply, error) {
	out := AssistantReply{PlanIDs: []string{}, Sources: []AssistantCitation{}}
	in.Question = strings.TrimSpace(in.Question)
	if utf8.RuneCountInString(in.Question) < 3 || utf8.RuneCountInString(in.Question) > 2000 || len(in.PlanIDs) < 1 || len(in.PlanIDs) > 20 || in.Year < 1900 || in.Year > 2200 {
		return out, ErrInvalidQuery
	}
	scope := map[string]string{}
	plans := map[string]Plan{}
	sourceMap := map[string]AssistantCitation{}
	seen := map[string]bool{}
	context := AssistantContext{Question: in.Question, Year: in.Year, ProposedPlans: []AssistantPlan{}, Sources: []AssistantSource{}}
	for index, id := range in.PlanIDs {
		if seen[id] {
			return out, ErrInvalidQuery
		}
		seen[id] = true
		p, err := s.Get(ctx, a, id)
		if err != nil {
			return out, err
		}
		if p == nil {
			return out, ErrInvalidQuery
		}
		schedule, err := s.schedulePlan(ctx, a, *p, ScheduleQuery{PlanID: id, Range: MonthRange{fmt.Sprintf("%04d-01", in.Year), fmt.Sprintf("%04d-12", in.Year)}, First: 100})
		if err != nil {
			return out, err
		}
		ref := fmt.Sprintf("p%d", index+1)
		plans[ref] = *p
		scope[ref] = assistantScope(*p, schedule)
		context.Today = schedule.Today
		context.Timezone = schedule.Timezone
		place := "Not decided"
		if p.Row.IntendedLocationID.Valid {
			location, err := s.q().GetLocation(ctx, dbgen.GetLocationParams{ID: p.Row.IntendedLocationID, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return out, err
			}
			place = location.Name
		}
		item := AssistantPlan{Ref: ref, Name: p.Row.Name, State: string(p.Row.State), Method: string(p.Row.Method.PlantingMethod), ScientificName: optionalText(p.Row.ScientificName), Cultivar: optionalText(p.Row.Cultivar), Notes: optionalText(p.Row.Notes), IntendedPlace: place, IntendedPlacement: string(p.Row.IntendedPlacement.ContextPlacement), EstimatedBudget: estimatedBudget(p.Row), AlreadyInGarden: p.Fulfillment != nil && p.Fulfillment.PlantID.Valid, ChosenPeriod: PeriodFromRow(p.Row), GuidanceCurrent: schedule.GuidanceCurrent, GuidanceMessage: schedule.Message, Windows: schedule.Windows, ProjectionLimited: schedule.HasNext, Actions: []AssistantAction{}}
		if p.Row.DesiredQuantity.Valid {
			v := p.Row.DesiredQuantity.Int32
			item.DesiredQuantity = &v
		}
		for _, action := range p.Actions {
			item.Actions = append(item.Actions, AssistantAction{Kind: string(action.Kind), Title: action.Title, On: action.ScheduledOn.Time.Format("2006-01-02"), State: string(action.State)})
		}
		context.ProposedPlans = append(context.ProposedPlans, item)
		if schedule.GuidanceCurrent && p.LatestGuidance != nil {
			facts, err := DecodeGuidanceFacts(p.LatestGuidance.Facts)
			if err != nil {
				return out, err
			}
			for i, f := range facts {
				token := fmt.Sprintf("%sf%d", ref, i+1)
				context.Sources = append(context.Sources, AssistantSource{Ref: token, PlanRef: ref, Fact: f})
				sourceMap[token] = AssistantCitation{Ref: token, PlanID: idString(p.Row.ID), PlanName: p.Row.Name, Fact: f}
			}
		}
	}
	if len(context.Sources) == 0 {
		out.Answer = "No current source-matched planting guidance is ready for these plans. Keep your own chosen windows, or add a dated preparation or buying action. Review each plan's Windows tab for its coverage or missing location. Timezone and hardiness zone alone cannot supply planting or first-harvest dates. Your ideas remain saved; nothing was changed."
		out.PlanIDs = append(out.PlanIDs, in.PlanIDs...)
		return out, nil
	}
	if s.Assistant == nil {
		return out, ErrAssistantUnavailable
	}
	draft, err := s.Assistant.AnswerPlanning(ctx, a.WorkspaceID, context)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(draft.Answer) == "" || utf8.RuneCountInString(draft.Answer) > 6000 || len(draft.PlanRefs) > 20 || len(draft.SourceRefs) == 0 || len(draft.SourceRefs) > 32 || utf8.RuneCountInString(draft.ProposedNote) > 6000 {
		return out, ErrAssistantAnswer
	}
	seen = map[string]bool{}
	for _, ref := range draft.PlanRefs {
		p, ok := plans[ref]
		if !ok {
			return out, ErrAssistantAnswer
		}
		if !seen[ref] {
			out.PlanIDs = append(out.PlanIDs, idString(p.Row.ID))
			seen[ref] = true
		}
	}
	seen = map[string]bool{}
	for _, ref := range draft.SourceRefs {
		citation, ok := sourceMap[ref]
		if !ok {
			return out, ErrAssistantAnswer
		}
		if !seen[ref] {
			out.Sources = append(out.Sources, citation)
			seen[ref] = true
		}
	}
	if strings.TrimSpace(draft.ProposedNote) != "" {
		p, ok := plans[draft.NotePlanRef]
		if !ok || !active(p.Row.State) {
			return out, ErrAssistantAnswer
		}
		own := false
		for _, c := range out.Sources {
			if c.PlanID == idString(p.Row.ID) {
				own = true
			}
		}
		if !own {
			return out, ErrAssistantAnswer
		}
		out.Note = &AssistantNote{PlanID: idString(p.Row.ID), ExpectedRevision: p.Row.Revision, Text: strings.TrimSpace(draft.ProposedNote)}
	} else if draft.NotePlanRef != "" {
		return out, ErrAssistantAnswer
	}
	// Model work holds no DB transaction. Recheck every selected aggregate and
	// source context before exposing a draft from a changed/cancelled plan.
	for ref, previous := range plans {
		current, err := s.Get(ctx, a, idString(previous.Row.ID))
		if err != nil {
			return out, err
		}
		if current == nil {
			return out, ErrAssistantChanged
		}
		schedule, err := s.schedulePlan(ctx, a, *current, ScheduleQuery{PlanID: idString(current.Row.ID), Range: MonthRange{fmt.Sprintf("%04d-01", in.Year), fmt.Sprintf("%04d-12", in.Year)}, First: 100})
		if err != nil {
			return out, err
		}
		if scope[ref] != assistantScope(*current, schedule) {
			return out, ErrAssistantChanged
		}
	}
	out.Answer = strings.TrimSpace(draft.Answer)
	out.IsAI = true
	return out, nil
}
