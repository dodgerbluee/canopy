package graphql

import (
	"canopy/server/internal/knowledge"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const knowledgeQuery = `query($year:Int!){gardenKnowledge(year:$year){settings{country region place enabled revision timezone hardinessZone} plants{plantId name state message facts{id kind months timingBasis sourceUrl summary}} records{plantId month waterings photos}}}`

type guideModel struct {
	calls            int
	started, release chan struct{}
	answer           knowledge.AnswerDraft
}

func (m *guideModel) Extract(ctx context.Context, s knowledge.Source, text string) ([]knowledge.DraftFact, error) {
	m.calls++
	if m.started != nil {
		close(m.started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.release:
		}
	}
	return []knowledge.DraftFact{{Kind: "BLOOM", Label: "Spring flowers", Summary: "Flowering is a regional estimate; maturity and exposure affect it.", Evidence: "Flowering occurs in March through May.", Timing: "March through May."}}, nil
}
func (m *guideModel) Answer(context.Context, string) (knowledge.AnswerDraft, error) {
	return m.answer, nil
}

type guideTransport func(*http.Request) (*http.Response, error)

func (f guideTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func knowledgeTestService(m *mediaEnv, model *guideModel) *knowledge.Service {
	s := knowledge.New(m.pool)
	s.Model = model
	s.Fetch = &knowledge.Fetcher{HTTP: &http.Client{Transport: guideTransport(func(r *http.Request) (*http.Response, error) {
		text := "User-agent: *\nAllow: /"
		if r.URL.Path != "/robots.txt" {
			text = "<article>Flowering occurs in March through May. " + strings.Repeat("Regional plant growing conditions matter. ", 15) + "</article>"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(text))}, nil
	})}}
	return s
}
func knowledgeSettings(t *testing.T, m *mediaEnv, revision int, country, region string) map[string]any {
	t.Helper()
	key := uuid.NewString()
	payload, problems := runMut(t, m, "saveGardenKnowledgeSettings", "SaveGardenKnowledgeSettingsInput", map[string]any{"country": country, "region": region, "place": "Test garden", "timezone": "America/Chicago", "enabled": true, "expectedRevision": revision}, &key, `settings{country region enabled revision}`)
	mustNoProblems(t, problems, "save garden settings")
	return payload
}
func createKnowledgePlant(t *testing.T, m *mediaEnv, name string) string {
	t.Helper()
	payload, ps := runMut(t, m, "createPlant", "CreatePlantInput", map[string]any{"name": name, "type": "INDIVIDUAL", "category": "CITRUS", "cultivar": "Meyer"}, nil, `plant{id}`)
	mustNoProblems(t, ps, "create plant")
	return payload["plant"].(map[string]any)["id"].(string)
}
func TestKnowledgeLifecycleAndRegionalIsolationHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	one := createKnowledgePlant(t, m, "Meyer lemon")
	two := createKnowledgePlant(t, m, "Another Meyer lemon")
	model := &guideModel{}
	s := knowledgeTestService(m, model)
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	page := dataOf(t, m.gql(knowledgeQuery, map[string]any{"year": 2026}), "gardenKnowledge")
	if page["plants"].([]any)[0].(map[string]any)["state"] != "DISABLED" {
		t.Fatal("no consent must make no requests", page)
	}
	if model.calls != 0 {
		t.Fatal("provider called before opt-in")
	}
	knowledgeSettings(t, m, 0, "US", "Texas")
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 {
		t.Fatal("matching plants must reuse document/profile", model.calls)
	}
	page = dataOf(t, m.gql(knowledgeQuery, map[string]any{"year": 2026}), "gardenKnowledge")
	for _, row := range page["plants"].([]any) {
		p := row.(map[string]any)
		if p["state"] != "READY" || len(p["facts"].([]any)) != 1 {
			t.Fatal(p)
		}
	}
	var count int
	if err := m.pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE workspace_id=$1`, m.wsID).Scan(&count); err != nil || count != 0 {
		t.Fatal("knowledge must not log care", err, count)
	}
	foreign := uuid.NewString()
	var ps []map[string]any
	r := m.gql(`mutation($id:ID!){refreshGardenKnowledge(plantId:$id){queued problems{code}}}`, map[string]any{"id": foreign})
	if len(r.Errors) > 0 {
		t.Fatal(r.Raw)
	}
	problems := payloadProblems(t, r, "refreshGardenKnowledge")
	wantProblemCode(t, problems, "NOT_FOUND")
	_, ps = runMut(t, m, "saveGardenKnowledgeSettings", "SaveGardenKnowledgeSettingsInput", map[string]any{"country": "US", "region": "Texas", "place": "Test", "timezone": "America/Chicago", "enabled": true, "expectedRevision": 0}, nil, `settings{revision}`)
	wantProblemCode(t, ps, "CONFLICT")
	knowledgeSettings(t, m, 1, "GB", "England")
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	page = dataOf(t, m.gql(knowledgeQuery, map[string]any{"year": 2026}), "gardenKnowledge")
	for _, row := range page["plants"].([]any) {
		if row.(map[string]any)["state"] != "UNSUPPORTED" {
			t.Fatal("Texas must not apply to England", row)
		}
	}
	_ = one
	_ = two
}
func TestKnowledgeLateResultsFencedAndDeletion(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	id := createKnowledgePlant(t, m, "Meyer lemon")
	knowledgeSettings(t, m, 0, "US", "Texas")
	model := &guideModel{started: make(chan struct{}), release: make(chan struct{})}
	s := knowledgeTestService(m, model)
	done := make(chan error, 1)
	go func() { done <- s.Sweep(ctx) }()
	select {
	case <-model.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	knowledgeSettings(t, m, 1, "GB", "England")
	close(model.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	page := dataOf(t, m.gql(knowledgeQuery, map[string]any{"year": 2026}), "gardenKnowledge")
	plant := page["plants"].([]any)[0].(map[string]any)
	if plant["state"] == "READY" || len(plant["facts"].([]any)) != 0 {
		t.Fatal("late Texas advice leaked after location edit", plant)
	}
	if _, err := m.pool.Exec(ctx, `DELETE FROM plants WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeStaleTimingCachesReprocessed(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	createKnowledgePlant(t, m, "Meyer lemon")
	knowledgeSettings(t, m, 0, "US", "Texas")
	model := &guideModel{}
	s := knowledgeTestService(m, model)
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 {
		t.Fatal("initial extraction", model.calls)
	}
	// Simulate persisted guidance produced by the old timing process, including
	// a document and profile that still fall within their normal cache lifetime.
	for _, query := range []string{
		`UPDATE knowledge_documents SET facts=(SELECT jsonb_agg(f-'extractionVersion') FROM jsonb_array_elements(facts) f)`,
		`UPDATE knowledge_profiles SET facts=(SELECT jsonb_agg(f-'extractionVersion') FROM jsonb_array_elements(facts) f)`,
	} {
		if _, err := m.pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	page := dataOf(t, m.gql(knowledgeQuery, map[string]any{"year": 2026}), "gardenKnowledge")
	p := page["plants"].([]any)[0].(map[string]any)
	if p["state"] != "PENDING" || len(p["facts"].([]any)) != 0 {
		t.Fatal("stale bands must be hidden before background repair", p)
	}
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 {
		t.Fatal("stale document/profile reused", model.calls)
	}
	page = dataOf(t, m.gql(knowledgeQuery, map[string]any{"year": 2026}), "gardenKnowledge")
	p = page["plants"].([]any)[0].(map[string]any)
	if p["state"] != "READY" || len(p["facts"].([]any)) != 1 {
		t.Fatal("corrected guidance not published", p)
	}
	var version int
	if err := m.pool.QueryRow(ctx, `SELECT (facts->0->>'extractionVersion')::int FROM knowledge_documents LIMIT 1`).Scan(&version); err != nil || version != knowledge.ExtractionVersion {
		t.Fatal("document version", version, err)
	}
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 {
		t.Fatal("current cache re-extracted", model.calls)
	}
	var activities int
	if err := m.pool.QueryRow(ctx, `SELECT count(*) FROM activities`).Scan(&activities); err != nil || activities != 0 {
		t.Fatal("refresh must not log care", err, activities)
	}
}
func TestKnowledgeGroundedAnswerValidation(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	id := createKnowledgePlant(t, m, "Meyer lemon")
	knowledgeSettings(t, m, 0, "US", "Texas")
	model := &guideModel{}
	s := knowledgeTestService(m, model)
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	garden, err := s.Garden(ctx, m.wsID, 2026)
	if err != nil {
		t.Fatal(err)
	}
	fact := garden.Plants[0].Facts[0]
	model.answer = knowledge.AnswerDraft{Answer: "This is a regional flowering estimate, not an observation.", FactIDs: []string{fact.ID}, PlantIDs: []string{id}}
	if _, facts, err := s.Ask(ctx, m.wsID, "When might it bloom?", 2026); err != nil || len(facts) != 1 {
		t.Fatal(facts, err)
	}
	model.answer.FactIDs = []string{"invented"}
	if _, _, err = s.Ask(ctx, m.wsID, "When might it bloom?", 2026); err == nil {
		t.Fatal("invented citation accepted")
	}
	knowledgeSettings(t, m, 1, "GB", "England")
	if err = s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	model.answer = knowledge.AnswerDraft{Answer: "Invented November blooms", FactIDs: []string{}, PlantIDs: []string{}}
	fallback, refs, err := s.Ask(ctx, m.wsID, "When will my plants flower?", 2026)
	if err != nil || len(refs) != 0 || strings.Contains(fallback.Answer, "Invented") || !strings.Contains(fallback.Answer, "timing remain unknown") {
		t.Fatal("empty knowledge must stay grounded without model guesses", fallback, err)
	}
	body, err := m.q.ExportKnowledgeProfiles(ctx, m.wsID)
	if err != nil || len(body) != 1 || !json.Valid(body[0]) {
		t.Fatal("profile export", err)
	}
}

func TestKnowledgeThroughCurrentWorker(t *testing.T) {
	binary := os.Getenv("CANOPY_TEST_WORKER_BINARY")
	if binary == "" || os.Getenv("KNOWLEDGE_MODEL_LIVE") != "1" {
		t.Skip("explicit worker/model opt-in required")
	}
	m := newMediaEnv(t, 0)
	knowledgeSettings(t, m, 0, "US", "Texas")
	createKnowledgePlant(t, m, "Meyer lemon")
	createKnowledgePlant(t, m, "Second Meyer lemon")
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+m.pool.Config().ConnString(), "CANOPY_ENV=test", "CANOPY_LOG_LEVEL=warn", "CANOPY_STORAGE_ROOT="+t.TempDir(), "CANOPY_MEDIA_IMAGE=canopy-media:r2", "CANOPY_AI_BASE_URL=", "CANOPY_AI_API_KEY=")
	log, err := os.CreateTemp(t.TempDir(), "worker-log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		var ready, jobs int
		err = m.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM plant_knowledge WHERE workspace_id=$1 AND state='READY'),(SELECT count(*) FROM river_job WHERE kind='knowledge.enrich' AND state='completed')`, m.wsID).Scan(&ready, &jobs)
		if err != nil {
			t.Fatal(err)
		}
		if ready == 2 && jobs > 0 {
			var profiles int
			if err = m.pool.QueryRow(context.Background(), `SELECT count(*) FROM knowledge_profiles WHERE workspace_id=$1`, m.wsID).Scan(&profiles); err != nil || profiles != 1 {
				t.Fatal("profile reuse", err, profiles)
			}
			s := knowledge.New(m.pool)
			s.Model = knowledge.NewModel(os.Getenv("CANOPY_VISION_BASE_URL"), os.Getenv("CANOPY_VISION_API_KEY"), os.Getenv("CANOPY_VISION_MODEL"), "none")
			answer, citations, err := s.Ask(context.Background(), m.wsID, "What do the sources say about flowering for my Meyer lemons? Distinguish expected timing from actual observations.", 2026)
			if err != nil || len(answer.Answer) == 0 || len(citations) == 0 {
				t.Fatal("live grounded garden answer", err)
			}
			t.Logf("Configured model returned a validated garden answer with %d supporting fact references.", len(citations))
			t.Log("Real HTTP plant creation → durable outbox → queued current worker → public A&M guide → configured model → 2 READY plants, 1 reusable profile.")
			return
		}
		select {
		case err := <-done:
			done <- err
			t.Fatal("worker exited early", err)
		default:
		}
		time.Sleep(500 * time.Millisecond)
	}
	rows, _ := m.pool.Query(context.Background(), `SELECT state,message FROM plant_knowledge WHERE workspace_id=$1`, m.wsID)
	defer rows.Close()
	for rows.Next() {
		var state, message string
		_ = rows.Scan(&state, &message)
		t.Log(state, message)
	}
	t.Fatal("queued knowledge did not finish")
}

func TestKnowledgeRejectsMultipleModelCallsHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	for _, query := range []string{`mutation{a:askGarden(question:"First",year:2026){answer} b:askGarden(question:"Second",year:2026){answer}}`, `mutation{...Questions} fragment Questions on Mutation {a:askGarden(question:"First",year:2026){answer} b:askGarden(question:"Second",year:2026){answer}}`} {
		r := m.gql(query, nil)
		if len(r.Errors) != 1 || !strings.Contains(r.Raw, "one garden question") {
			t.Fatal("multiple model calls must be rejected before execution", r.Raw)
		}
	}
}
