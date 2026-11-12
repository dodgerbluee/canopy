package knowledge

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentcontext"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"strings"
	"time"
)

type SweepArgs struct{}

func (SweepArgs) Kind() string { return "knowledge.enrich" }

type Worker struct {
	river.WorkerDefaults[SweepArgs]
	Service *Service
}

func (w *Worker) Timeout(*river.Job[SweepArgs]) time.Duration { return 15 * time.Minute }
func (w *Worker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	return w.Service.Sweep(ctx)
}
func PeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(30*time.Second), func() (river.JobArgs, *river.InsertOpts) {
		return SweepArgs{}, &river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: 30 * time.Second}}
	}, &river.PeriodicJobOpts{ID: "knowledge.enrich", RunOnStart: true})
}
func (s *Service) Sweep(ctx context.Context) error {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext(current_schema()||'knowledge.worker'))`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(cleanup, `SELECT pg_advisory_unlock(hashtext(current_schema()||'knowledge.worker'))`)
	}()

	if _, err = s.Q.RequeueStaleKnowledge(ctx, fmt.Sprint(ExtractionVersion)); err != nil {
		return err
	}
	rows, err := s.Q.PendingKnowledge(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		claimed, err := s.Q.ClaimKnowledge(ctx, dbgen.ClaimKnowledgeParams{PlantID: row.PlantID, WorkspaceID: row.WorkspaceID, Generation: row.Generation})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err = s.Enrich(ctx, claimed); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) finish(ctx context.Context, row dbgen.PlantKnowledge, state, message, key string) error {
	p := dbgen.FinishKnowledgeParams{PlantID: row.PlantID, WorkspaceID: row.WorkspaceID, Generation: row.Generation, State: state, Message: message}
	if key != "" {
		p.ProfileKey = pgtype.Text{String: key, Valid: true}
	}
	_, err := s.Q.FinishKnowledge(ctx, p)
	return err
}
func (s *Service) Enrich(ctx context.Context, row dbgen.PlantKnowledge) error {
	if !agentcontext.JobAllowed(ctx, s.Pool, uid(row.AgentGrantID), uid(row.AgentCredentialID), uid(row.WorkspaceID), "analysis:run", uid(row.PlantID)) {
		return s.finish(ctx, row, "FAILED", "The initiating agent connection is no longer authorized. Refresh from the app to try again.", "")
	}
	settings, err := s.Settings(ctx, row.WorkspaceID)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return s.finish(ctx, row, "DISABLED", "Enable regional knowledge in garden settings.", "")
	}
	if settings.Country == "" || settings.Region == "" {
		return s.finish(ctx, row, "NEEDS_LOCATION", "Set the garden country and region to match guidance.", "")
	}
	plant, err := s.Q.GetPlant(ctx, dbgen.GetPlantParams{ID: row.PlantID, WorkspaceID: row.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if plant.Status != dbgen.PlantStatusACTIVE {
		return s.finish(ctx, row, "UNSUPPORTED", "Archived plants are not researched.", "")
	}
	crop := cropFor(plant.Name, plant.ScientificName.String, string(plant.Category))
	sources := Applicable(s.Sources, settings.Country, settings.Region, crop)
	if len(sources) == 0 {
		return s.finish(ctx, row, "UNSUPPORTED", "No registered regional source matches this plant yet. Your plant history is still available.", "")
	}
	identity := plant.Name + " " + plant.ScientificName.String + " " + plant.Cultivar.String + " " + crop
	profileKey := hash([]any{ExtractionVersion, settings.Country, normalize(settings.Region), crop, normalize(plant.ScientificName.String), normalize(plant.Cultivar.String), subjectIdentity(identity), s.Sources})
	// One transaction-scoped lock per reusable profile prevents duplicate model
	// work. Plant writes never acquire it. Outbox generations fence late results.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	if err = q.LockMutationKey(ctx, uid(row.WorkspaceID)+"|knowledge|"+profileKey); err != nil {
		return err
	}
	profile, err := q.GetKnowledgeProfile(ctx, dbgen.GetKnowledgeProfileParams{WorkspaceID: row.WorkspaceID, ProfileKey: profileKey})
	if err == nil {
		var cached []Fact
		if json.Unmarshal(profile.Facts, &cached) != nil || !currentFacts(cached) {
			err = pgx.ErrNoRows
		}
	}
	if err == nil {
		n, err := q.FinishKnowledge(ctx, dbgen.FinishKnowledgeParams{PlantID: row.PlantID, WorkspaceID: row.WorkspaceID, Generation: row.Generation, State: "READY", Message: "Sourced guidance matched from the regional knowledge cache. AI-extracted; check applicability.", ProfileKey: pgtype.Text{String: profile.ProfileKey, Valid: true}})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	model, modelErr := s.selectedModel(ctx, row.WorkspaceID)
	if modelErr != nil {
		return s.finish(ctx, row, "FAILED", modelErr.Error(), "")
	}
	all := []Fact{}
	for _, source := range sources {
		if err = q.LockMutationKey(ctx, "knowledge-document|"+source.URL); err != nil {
			return err
		}
		doc, err := q.GetKnowledgeDocument(ctx, source.URL)
		var facts []Fact
		if err == nil {
			if json.Unmarshal(doc.Facts, &facts) != nil || !currentFacts(facts) {
				err = pgx.ErrNoRows
			}
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if s.Fetch == nil || model == nil {
				return s.finish(ctx, row, "FAILED", "Knowledge extraction needs a configured source fetcher and AI model.", "")
			}
			text, err := s.Fetch.Read(ctx, source)
			if err != nil {
				return s.finish(ctx, row, "FAILED", err.Error()+" Retry after checking source availability.", "")
			}
			if !agentcontext.JobAllowed(ctx, s.Pool, uid(row.AgentGrantID), uid(row.AgentCredentialID), uid(row.WorkspaceID), "analysis:run", uid(row.PlantID)) {
				return s.finish(ctx, row, "FAILED", "The initiating agent connection is no longer authorized.", "")
			}
			drafts, err := model.Extract(ctx, source, text)
			if err != nil {
				return s.finish(ctx, row, "FAILED", err.Error(), "")
			}
			facts, err = ValidateFacts(source, text, drafts, time.Now().UTC())
			if err != nil {
				return s.finish(ctx, row, "FAILED", err.Error(), "")
			}
			body, _ := json.Marshal(facts)
			if err = q.PutKnowledgeDocument(ctx, dbgen.PutKnowledgeDocumentParams{Url: source.URL, Provider: source.ID, ContentSha256: facts[0].ContentSHA256, Facts: body}); err != nil {
				return err
			}
		}
		all = append(all, FilterFacts(facts, identity)...)
	}
	if len(all) == 0 {
		return s.finish(ctx, row, "UNSUPPORTED", "The regional source has no applicable guidance for this plant's known type yet.", "")
	}
	body, _ := json.Marshal(all)
	if err = q.PutKnowledgeProfile(ctx, dbgen.PutKnowledgeProfileParams{WorkspaceID: row.WorkspaceID, ProfileKey: profileKey, Facts: body}); err != nil {
		return err
	}
	n, err := q.FinishKnowledge(ctx, dbgen.FinishKnowledgeParams{PlantID: row.PlantID, WorkspaceID: row.WorkspaceID, Generation: row.Generation, State: "READY", Message: "Regional source guidance. AI-extracted, not a reviewed care plan or weather-adjusted forecast.", ProfileKey: pgtype.Text{String: profileKey, Valid: true}})
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	return tx.Commit(ctx)
}
func subjectIdentity(identity string) []string {
	out := []string{}
	for _, word := range []string{"lemon", "lime", "mandarin", "satsuma", "orange", "grapefruit", "meyer", "june bearing", "day neutral", "everbearing", "primocane", "floricane", "chandler", "arapaho"} {
		if containsWord(identity, word) {
			out = append(out, word)
		}
	}
	return out
}
func containsWord(identity, word string) bool {
	return len(word) > 0 && strings.Contains(" "+normalize(identity)+" ", " "+word+" ")
}
