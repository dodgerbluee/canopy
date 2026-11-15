package planning

import (
	dbgen "canopy/server/db/generated"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"
)

type assistantFunc func(context.Context, pgtype.UUID, AssistantContext) (AssistantDraft, error)

func (f assistantFunc) AnswerPlanning(ctx context.Context, ws pgtype.UUID, in AssistantContext) (AssistantDraft, error) {
	return f(ctx, ws, in)
}
func validAnswer() AssistantDraft {
	return AssistantDraft{Answer: "Review the cited planting window, then choose your next action.", PlanRefs: []string{"p1"}, SourceRefs: []string{"p1f1"}, NotePlanRef: "p1", ProposedNote: "Review the supported planting window before choosing an action day."}
}
func TestAssistantCitationsDraftAndNoWrites(t *testing.T) {
	s, a, _, p, _ := scheduleWorld(t)
	before := count(t, s, "audit_revisions")
	s.Assistant = assistantFunc(func(ctx context.Context, ws pgtype.UUID, in AssistantContext) (AssistantDraft, error) {
		if ws != a.WorkspaceID || len(in.ProposedPlans) != 1 || len(in.Sources) != 1 || in.ProposedPlans[0].EstimatedBudget == nil || *in.ProposedPlans[0].EstimatedBudget != "120.00 USD (estimate, not actual spending)" || in.ProposedPlans[0].AlreadyInGarden {
			t.Fatal(in)
		}
		return validAnswer(), nil
	})
	r, err := s.AskPlanner(context.Background(), a, AssistantQuery{Question: "What should I prepare?", PlanIDs: []string{idString(p.Row.ID)}, Year: 2027})
	if err != nil || !r.IsAI || len(r.Sources) != 1 || r.Sources[0].Fact.ID != "source-planting" || r.Note == nil || r.Note.ExpectedRevision != p.Row.Revision {
		t.Fatal(r, err)
	}
	if count(t, s, "plants")+count(t, s, "planting_actions")+count(t, s, "purchases") > 0 || count(t, s, "audit_revisions") != before {
		t.Fatal("assistant wrote data")
	}
}
func TestAssistantInvalidReferencesAndStaleContext(t *testing.T) {
	for _, name := range []string{"foreign-plan", "foreign-source", "wrong-note", "oversized", "changed-plan", "changed-source"} {
		t.Run(name, func(t *testing.T) {
			s, a, g, p, _ := scheduleWorld(t)
			s.Assistant = assistantFunc(func(ctx context.Context, _ pgtype.UUID, _ AssistantContext) (AssistantDraft, error) {
				d := validAnswer()
				switch name {
				case "foreign-plan":
					d.PlanRefs = []string{"not-supplied"}
				case "foreign-source":
					d.SourceRefs = []string{"invented"}
				case "wrong-note":
					d.NotePlanRef = "foreign"
				case "oversized":
					for range 6001 {
						d.Answer += "x"
					}
				case "changed-plan":
					_, ps, err := s.Update(ctx, a, UpdateInput{ID: idString(p.Row.ID), ExpectedRevision: p.Row.Revision, Intent: Intent{Name: "Changed"}}, nil)
					if err != nil || len(ps) > 0 {
						t.Fatal(ps, err)
					}
				case "changed-source":
					g.mu.Lock()
					g.policy = "new growing region"
					g.mu.Unlock()
				}
				return d, nil
			})
			_, err := s.AskPlanner(context.Background(), a, AssistantQuery{Question: "What should I do?", PlanIDs: []string{idString(p.Row.ID)}, Year: 2027})
			want := ErrAssistantAnswer
			if name == "changed-plan" || name == "changed-source" {
				want = ErrAssistantChanged
			}
			if !errors.Is(err, want) {
				t.Fatal(name, err)
			}
		})
	}
}
func TestAssistantManualPrivateAndUnavailablePaths(t *testing.T) {
	s, a := scratch(t)
	r := mustCreate(t, s, a, Intent{Name: "Manual idea"})
	called := false
	s.Assistant = assistantFunc(func(context.Context, pgtype.UUID, AssistantContext) (AssistantDraft, error) {
		called = true
		return validAnswer(), nil
	})
	reply, err := s.AskPlanner(context.Background(), a, AssistantQuery{Question: "When can I start?", PlanIDs: []string{r.ID}, Year: 2027})
	if err != nil || reply.IsAI || called || reply.Note != nil || len(reply.Sources) != 0 {
		t.Fatal(reply, err)
	}
	other := workspace(t, s)
	if _, err = s.AskPlanner(context.Background(), other, AssistantQuery{Question: "When?", PlanIDs: []string{r.ID}, Year: 2027}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatal("private plan", err)
	}
	for _, ids := range [][]string{{}, {r.ID, r.ID}} {
		if _, err = s.AskPlanner(context.Background(), a, AssistantQuery{Question: "When?", PlanIDs: ids, Year: 2027}); !errors.Is(err, ErrInvalidQuery) {
			t.Fatal(err)
		}
	}
	ready, actor, _, p, _ := scheduleWorld(t)
	_, err = ready.AskPlanner(context.Background(), actor, AssistantQuery{Question: "When?", PlanIDs: []string{idString(p.Row.ID)}, Year: 2027})
	if !errors.Is(err, ErrAssistantUnavailable) {
		t.Fatal(err)
	}
}
func TestAssistantBudgetPresentationIsExactAndEstimated(t *testing.T) {
	for currency, want := range map[string]string{"USD": "120.00", "JPY": "12000", "KWD": "12.000"} {
		row := dbgen.PlantingPlan{BudgetMinor: pgtype.Int8{Int64: 12000, Valid: true}, BudgetCurrency: pgtype.Text{String: currency, Valid: true}}
		got := estimatedBudget(row)
		if got == nil || *got != want+" "+currency+" (estimate, not actual spending)" {
			t.Fatal(got)
		}
	}
	if estimatedBudget(dbgen.PlantingPlan{}) != nil {
		t.Fatal("unknown became zero")
	}
}

func TestAssistantRejectsOwnershipPurgedDuringAnswer(t *testing.T) {
	s, a, _, p, _ := scheduleWorld(t)
	ctx := context.Background()
	plant := s.IDs.New()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO plants(id,workspace_id,name,type,category) VALUES($1,$2,'Actual linked tree','INDIVIDUAL','OTHER')`, plant, a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,mode) VALUES($1,$2,$3,$4,'EXISTING')`, s.IDs.New(), a.WorkspaceID, p.Row.ID, plant); err != nil {
		t.Fatal(err)
	}
	s.Assistant = assistantFunc(func(ctx context.Context, _ pgtype.UUID, in AssistantContext) (AssistantDraft, error) {
		if !in.ProposedPlans[0].AlreadyInGarden {
			t.Fatal("missing known ownership")
		}
		if _, err := s.Pool.Exec(ctx, "DELETE FROM plants WHERE id=$1", plant); err != nil {
			t.Fatal(err)
		}
		return validAnswer(), nil
	})
	_, err := s.AskPlanner(ctx, a, AssistantQuery{Question: "What next?", PlanIDs: []string{idString(p.Row.ID)}, Year: 2027})
	if !errors.Is(err, ErrAssistantChanged) {
		t.Fatal("purged ownership answer escaped", err)
	}
}
