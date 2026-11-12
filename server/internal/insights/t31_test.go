// T31 acceptance: OpenAI-compatible vision adapter wire discipline,
// evidence provenance (ids + hashes + capture order), abstention shape,
// and longitudinal SYNTHESIS runs that never write care state.
package insights

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/processor"
)

// ---- helpers ---------------------------------------------------------------

func seedPhotoAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w world, asset pgtype.UUID, takenAt time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO plant_photos (id, workspace_id, plant_id, asset_id, taken_at, date_source, viewpoint)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, 'USER', 'WHOLE') RETURNING id`,
		w.WorkspaceID, w.PlantID, asset, takenAt).Scan(&id); err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	return id
}

// fakeOpenAI serves the OpenAI chat-completions wire; the handler is
// supplied per test and every decoded request is recorded.
type fakeOpenAI struct {
	hits int
	last chatRequest
	auth string
	path string
	h    func(hits int) (int, string) // status, body
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits++
	f.auth = r.Header.Get("Authorization")
	f.path = r.URL.Path
	_ = json.NewDecoder(r.Body).Decode(&f.last)
	code, body := f.h(f.hits)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func chatBody(content string) string {
	return fmt.Sprintf(`{"model":"srv-vision-1","choices":[{"message":{"role":"assistant","content":%s},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
		jsonString(content))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func visionInput(ws string, snapJSON string, assetIDs ...string) AnalyzeInput {
	return AnalyzeInput{
		WorkspaceID: ws, Capability: CapVisionAnalyzer,
		Snapshot: json.RawMessage(snapJSON), PromptVersion: "p1",
		PhotoAssetIDs: assetIDs,
	}
}

func twoPhotoSnapshot(pA, aA, tA, pB, aB, tB string) string {
	return fmt.Sprintf(`{"schema":"canopy-snap/2","kind":"VISION",
		"plant":{"id":"plant-1","name":"Monstera"},
		"workspace":{"timezone":"UTC","hasCoordinates":false},
		"activities":[],"measurements":[],
		"photos":[
			{"id":%s,"assetId":%s,"sha256":"aa","takenAt":%s},
			{"id":%s,"assetId":%s,"sha256":"bb","takenAt":%s}],
		"guidance":[],"weather":[],"missingFacts":[]}`,
		jsonString(pA), jsonString(aA), jsonString(tA),
		jsonString(pB), jsonString(aB), jsonString(tB))
}

// ---- wire format ------------------------------------------------------------

func TestVisionAnalyzerWireFormat(t *testing.T) {
	assetA, assetB := "aaaa0000-0000-4000-8000-00000000000a", "bbbb0000-0000-4000-8000-00000000000b"
	fake := &fakeOpenAI{h: func(int) (int, string) {
		return 200, chatBody("```json\n" + string(validObsJSON()) + "\n```")
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	st := &fakeStore{objs: map[string][]byte{
		"ws/a.jpg": []byte("JPEG-A-BYTES"),
		"ws/b.jpg": []byte("JPEG-B-BYTES"),
	}}
	aA := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/a.jpg")
	aB := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/b.jpg")

	v, err := NewVisionAnalyzer(srv.URL, "k", "cfg-vision-model", 30*time.Second, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), st, &fakeSB{}

	// Declared pair order is A then B, but B was captured EARLIER: the
	// adapter must send chronological order (B first).
	snap := twoPhotoSnapshot("pA", assetA, "2026-10-02T00:00:00Z", "pB", assetB, "2026-09-01T00:00:00Z")
	// align snapshot asset ids with the seeded rows
	snap = strings.ReplaceAll(snap, assetA, uuidString(aA))
	snap = strings.ReplaceAll(snap, assetB, uuidString(aB))

	out, err := v.Analyze(ctx, visionInput(uuidString(w.WorkspaceID), snap, uuidString(aA), uuidString(aB)))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if fake.path != "/v1/chat/completions" {
		t.Fatalf("path: %s", fake.path)
	}
	if fake.auth != "Bearer k" {
		t.Fatalf("auth header: %q", fake.auth)
	}
	if fake.last.Model != "cfg-vision-model" {
		t.Fatalf("model must be the configured one, got %q", fake.last.Model)
	}
	sys, _ := fake.last.Messages[0].Content.(string)
	if !strings.Contains(sys, "prompt version p1") {
		t.Fatal("system prompt must carry the run's prompt version")
	}
	user := fake.last.Messages[1].Content.([]interface{})
	text, _ := user[0].(map[string]interface{})
	if !strings.Contains(fmt.Sprint(text["text"]), `"canopy-snap/2"`) {
		t.Fatal("snapshot JSON must be sent as text")
	}
	imgURL := func(part interface{}) string {
		m := part.(map[string]interface{})
		iu := m["image_url"].(map[string]interface{})
		return iu["url"].(string)
	}
	wantA := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("JPEG-A-BYTES"))
	wantB := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("JPEG-B-BYTES"))
	if got := imgURL(user[1]); got != wantB {
		t.Fatalf("first image must be the EARLIER capture:\n got %.40s\nwant %.40s", got, wantB)
	}
	if got := imgURL(user[2]); got != wantA {
		t.Fatalf("second image must be the later capture:\n got %.40s\nwant %.40s", got, wantA)
	}
	var res ObsResult
	if err := json.Unmarshal(out.Raw, &res); err != nil {
		t.Fatalf("raw must be the fenced-extracted object: %v %s", err, out.Raw)
	}
	if out.Provider != "openai-compatible" || out.Model != "srv-vision-1" {
		t.Fatalf("provenance: %+v", out)
	}
	if out.UsageCostMicroUSD != nil {
		t.Fatalf("chat usage is unpriced: cost must settle unknown, got %d", *out.UsageCostMicroUSD)
	}
}

func TestVisionErrorClasses(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	st := &fakeStore{objs: map[string][]byte{"ws/a.jpg": []byte("A"), "ws/b.jpg": []byte("B")}}
	aA := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/a.jpg")
	aB := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/b.jpg")
	snap := twoPhotoSnapshot("pA", uuidString(aA), "2026-10-02T00:00:00Z", "pB", uuidString(aB), "2026-09-01T00:00:00Z")

	cases := []struct {
		name string
		h    func(int) (int, string)
		want ErrorClass
	}{
		{"400 permanent", func(int) (int, string) { return 400, `{"error":{"message":"bad"}` }, ClassPermanent},
		{"401 permanent", func(int) (int, string) { return 401, `{}` }, ClassPermanent},
		{"429 retryable", func(int) (int, string) { return 429, `{}` }, ClassRetryable},
		{"500 retryable", func(int) (int, string) { return 500, `{}` }, ClassRetryable},
		{"malformed envelope invalid", func(int) (int, string) { return 200, `not json` }, ClassInvalidOutput},
		{"empty choices invalid", func(int) (int, string) { return 200, `{"choices":[]}` }, ClassInvalidOutput},
		{"empty content invalid", func(int) (int, string) { return 200, chatBody("   ") }, ClassInvalidOutput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeOpenAI{h: tc.h}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			v, err := NewVisionAnalyzer(srv.URL, "k", "m", 5*time.Second, 1<<20, 1<<20)
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			v.Q, v.Store, v.SB = dbgen.New(pool), st, &fakeSB{}
			_, err = v.Analyze(ctx, visionInput(uuidString(w.WorkspaceID), snap, uuidString(aA), uuidString(aB)))
			var pe *ProviderError
			if !asProviderErr(err, &pe) || pe.Class != tc.want {
				t.Fatalf("err=%v class=%v want=%v", err, classOf(err), tc.want)
			}
		})
	}

	t.Run("oversized response invalid", func(t *testing.T) {
		fake := &fakeOpenAI{h: func(int) (int, string) { return 200, `{"model":"` + strings.Repeat("x", 4096) + `"}` }}
		srv := httptest.NewServer(fake)
		defer srv.Close()
		v, err := NewVisionAnalyzer(srv.URL, "k", "m", 5*time.Second, 1024, 1<<20)
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		v.Q, v.Store, v.SB = dbgen.New(pool), st, &fakeSB{}
		_, err = v.Analyze(ctx, visionInput(uuidString(w.WorkspaceID), snap, uuidString(aA), uuidString(aB)))
		var pe *ProviderError
		if !asProviderErr(err, &pe) || pe.Class != ClassInvalidOutput {
			t.Fatalf("oversize must be invalid: %v", err)
		}
	})
}

func asProviderErr(err error, target **ProviderError) bool {
	pe, ok := err.(*ProviderError)
	if ok {
		*target = pe
	}
	return ok
}

func classOf(err error) ErrorClass {
	if pe, ok := err.(*ProviderError); ok {
		return pe.Class
	}
	return -1
}

func TestVisionConfigGuards(t *testing.T) {
	if _, err := NewVisionAnalyzer("http://x.example", "k", "", time.Second, 1<<20, 1<<20); err == nil {
		t.Fatal("model must be explicitly configured")
	}
	if _, err := NewVisionAnalyzer("http://x.example/v1", "k", "m", time.Second, 1<<20, 1<<20); err == nil {
		t.Fatal("base URL must be an origin (adapter appends /v1/chat/completions itself)")
	}
	if _, err := NewVisionAnalyzer("ftp://x.example", "k", "m", time.Second, 1<<20, 1<<20); err == nil {
		t.Fatal("scheme must be http(s)")
	}
}

func TestVisionRequiresExactlyAPair(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(string(validObsJSON())) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	v, err := NewVisionAnalyzer(srv.URL, "k", "m", 5*time.Second, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), &fakeStore{}, &fakeSB{}
	if _, err := v.Analyze(ctx, visionInput(uuidString(w.WorkspaceID), `{}`, "only-one")); err == nil {
		t.Fatal("single-photo runs must be refused (VISION is a comparison)")
	}
	if fake.hits != 0 {
		t.Fatal("nothing may be fetched for an invalid pair")
	}
}

func TestVisionTranscodesIncompatiblePhoto(t *testing.T) {
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(string(validObsJSON())) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	st := &fakeStore{objs: map[string][]byte{"ws/a.webp": []byte("WEBPBYTES"), "ws/b.jpg": []byte("JPEG")}}
	aA := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/webp", "ws/a.webp")
	aB := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/b.jpg")
	sb := &fakeSB{script: func(workDir string, argv []string) (processor.Output, error) {
		if argv[0] == "vips" && argv[1] == "jpegsave" {
			return processor.Output{}, os.WriteFile(filepath.Join(workDir, argv[3]), []byte("TRANSCODED-JPEG"), 0o600)
		}
		return processor.Output{}, fmt.Errorf("unexpected sandbox call %v", argv)
	}}
	v, err := NewVisionAnalyzer(srv.URL, "k", "m", 5*time.Second, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), st, sb
	snap := twoPhotoSnapshot("pA", uuidString(aA), "2026-09-01T00:00:00Z", "pB", uuidString(aB), "2026-10-01T00:00:00Z")
	if _, err := v.Analyze(ctx, visionInput(uuidString(w.WorkspaceID), snap, uuidString(aA), uuidString(aB))); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	user := fake.last.Messages[1].Content.([]interface{})
	iu := user[1].(map[string]interface{})["image_url"].(map[string]interface{})["url"].(string)
	want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("TRANSCODED-JPEG"))
	if iu != want {
		t.Fatalf("webp must be sandbox-transcoded before send: %.40s", iu)
	}
}

// ---- end-to-end runs ---------------------------------------------------------

func obsCiting(pA, pB string) string {
	return fmt.Sprintf(`{"visibleObservations":[
		{"text":"New leaf unfurled between captures.","evidenceIds":[%s,%s]},
		{"text":"Leaf gloss looks reduced in the later photo.","evidenceIds":[%s]}],
		"hypotheses":[{"text":"Recent watering interval may be too long.","confidence":"LOW","evidenceIds":[%s,%s]}],
		"missingInformation":["No scale reference in either photo."],
		"followUpChecks":["Check substrate moisture before next watering."],"citations":[]}`,
		jsonString(pA), jsonString(pB), jsonString(pB), jsonString(pA), jsonString(pB))
}

func TestWorkerVisionEndToEndHonestProvenanceNoCareWrites(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	older := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/old.jpg")
	newer := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/new.jpg")
	pOld := seedPhotoAt(t, ctx, pool, w, older, time.Now().UTC().Add(-30*24*time.Hour))
	pNew := seedPhotoAt(t, ctx, pool, w, newer, time.Now().UTC())

	fake := &fakeOpenAI{h: func(int) (int, string) {
		return 200, chatBody(obsCiting(uuidString(pOld), uuidString(pNew)))
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	st := &fakeStore{objs: map[string][]byte{"ws/old.jpg": []byte("OLD"), "ws/new.jpg": []byte("NEW")}}
	v, err := NewVisionAnalyzer(srv.URL, "k", "cfg-vision-model", 10*time.Second, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), st, &fakeSB{}
	provider := &Router{Ports: []Port{NoProvider{}, v}}
	s := newSvc(pool, defaultLimits(), provider)
	s.PromptVersion = "p1"

	// Snapshot photos must carry evidence hashes (provenance).
	run, probs, err := s.Request(ctx, w.Actor, RequestInput{
		Kind: "VISION", PlantID: uuidString(w.PlantID),
		PhotoIDs: []string{uuidString(pNew), uuidString(pOld)}, // reversed order on purpose
	}, nil)
	noProbs(t, probs, err)
	var snap Snapshot
	if err := json.Unmarshal(run.InputSnapshot, &snap); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, p := range snap.Photos {
		if len(p.Sha256) != 64 || p.TakenAt == "" {
			t.Fatalf("snapshot photo must carry sha256 + takenAt: %+v", p)
		}
	}

	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatalf("process: %v", err)
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State != dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatalf("state %s failure=%+v", done.State, done.FailureCode)
	}
	if done.Provider.String != "openai-compatible" || done.Model.String != "srv-vision-1" ||
		done.PromptVersion.String != "p1" {
		t.Fatalf("run row must store provider/model/prompt version: %+v", done)
	}
	if done.CostActualMicroUsd.Valid {
		t.Fatalf("unpriced chat usage must settle cost unknown (NULL), got %d", done.CostActualMicroUsd.Int64)
	}
	// Chronological send order despite reversed request order.
	if fake.hits != 1 {
		t.Fatalf("hits = %d", fake.hits)
	}
	user := fake.last.Messages[1].Content.([]interface{})
	first := user[1].(map[string]interface{})["image_url"].(map[string]interface{})["url"].(string)
	if !strings.Contains(first, base64.StdEncoding.EncodeToString([]byte("OLD"))) {
		t.Fatal("earlier capture must be sent first")
	}
	// No direct care writes: observations never touch care or journal state.
	var care, journal int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM care_occurrences`).Scan(&care); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if care != 0 || journal != 0 {
		t.Fatalf("vision runs must not write care/journal state: care=%d journal=%d", care, journal)
	}
}

func TestWorkerVisionAbstentionIsValidOutcome(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	a1 := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/1.jpg")
	a2 := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/2.jpg")
	p1 := seedPhotoAt(t, ctx, pool, w, a1, time.Now().UTC().Add(-10*24*time.Hour))
	p2 := seedPhotoAt(t, ctx, pool, w, a2, time.Now().UTC())

	// Incompatible pair: the provider abstains — no comparison claims,
	// empty hypotheses, refusal stated in missingInformation.
	abstain := `{"visibleObservations":[{"text":"First photo shows a broad-leafed plant; second shows a ceramic pot on a shelf.","evidenceIds":[]}],` +
		`"hypotheses":[],"missingInformation":["The two photos do not show the same plant; comparison refused."],` +
		`"followUpChecks":["Re-capture both photos of the same plant."],"citations":[]}`
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(abstain) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	st := &fakeStore{objs: map[string][]byte{"ws/1.jpg": []byte("1"), "ws/2.jpg": []byte("2")}}
	v, err := NewVisionAnalyzer(srv.URL, "k", "m", 10*time.Second, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), st, &fakeSB{}
	s := newSvc(pool, defaultLimits(), &Router{Ports: []Port{NoProvider{}, v}})

	run, probs, err := s.Request(ctx, w.Actor, RequestInput{
		Kind: "VISION", PlantID: uuidString(w.PlantID),
		PhotoIDs: []string{uuidString(p1), uuidString(p2)},
	}, nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatalf("process: %v", err)
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State != dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatalf("abstention is a valid outcome, got state %s failure=%+v", done.State, done.FailureCode)
	}
	var res ObsResult
	if err := json.Unmarshal(done.Result, &res); err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(res.Hypotheses) != 0 || len(res.MissingInformation) == 0 {
		t.Fatalf("abstention shape: %+v", res)
	}
}

func TestWorkerVisionRejectsUnsupportedEvidenceRefs(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	a1 := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/1.jpg")
	a2 := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/2.jpg")
	p1 := seedPhotoAt(t, ctx, pool, w, a1, time.Now().UTC().Add(-5*24*time.Hour))
	p2 := seedPhotoAt(t, ctx, pool, w, a2, time.Now().UTC())

	// Provider cites an evidence id that was never in the snapshot.
	forged := `{"visibleObservations":[{"text":"Growth looks steady.","evidenceIds":["11111111-1111-4111-8111-111111111111"]}],` +
		`"hypotheses":[],"missingInformation":[],"followUpChecks":[],"citations":[]}`
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(forged) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	st := &fakeStore{objs: map[string][]byte{"ws/1.jpg": []byte("1"), "ws/2.jpg": []byte("2")}}
	v, err := NewVisionAnalyzer(srv.URL, "k", "m", 10*time.Second, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), st, &fakeSB{}
	s := newSvc(pool, defaultLimits(), &Router{Ports: []Port{NoProvider{}, v}})

	run, probs, err := s.Request(ctx, w.Actor, RequestInput{
		Kind: "VISION", PlantID: uuidString(w.PlantID),
		PhotoIDs: []string{uuidString(p1), uuidString(p2)},
	}, nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatalf("process: %v", err)
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State == dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatal("unsupported source refs must never be stored as success")
	}
	if done.FailureCode.AnalysisFailureCode != dbgen.AnalysisFailureCodeREPAIRFAILED {
		t.Fatalf("failure code: %+v", done.FailureCode)
	}
}

func TestWorkerVisionRepairCarriesPreviousOutput(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	a1 := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/1.jpg")
	a2 := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/jpeg", "ws/2.jpg")
	p1 := seedPhotoAt(t, ctx, pool, w, a1, time.Now().UTC().Add(-5*24*time.Hour))
	p2 := seedPhotoAt(t, ctx, pool, w, a2, time.Now().UTC())

	bad := `{"visibleObservations":"not-an-array"}`
	fake := &fakeOpenAI{h: func(hits int) (int, string) {
		if hits == 1 {
			return 200, chatBody(bad)
		}
		return 200, chatBody(obsCiting(uuidString(p1), uuidString(p2)))
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	st := &fakeStore{objs: map[string][]byte{"ws/1.jpg": []byte("1"), "ws/2.jpg": []byte("2")}}
	v, err := NewVisionAnalyzer(srv.URL, "k", "m", 10*time.Second, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), st, &fakeSB{}
	s := newSvc(pool, defaultLimits(), &Router{Ports: []Port{NoProvider{}, v}})

	run, probs, err := s.Request(ctx, w.Actor, RequestInput{
		Kind: "VISION", PlantID: uuidString(w.PlantID),
		PhotoIDs: []string{uuidString(p1), uuidString(p2)},
	}, nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatalf("process: %v", err)
	}
	if fake.hits != 2 {
		t.Fatalf("expected one bounded repair attempt, hits=%d", fake.hits)
	}
	repairSeen := false
	for _, part := range fake.last.Messages[1].Content.([]interface{}) {
		m := part.(map[string]interface{})
		if s, _ := m["text"].(string); strings.Contains(s, "not-an-array") {
			repairSeen = true
		}
	}
	if !repairSeen {
		t.Fatal("repair attempt must carry the previous invalid output")
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State != dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatalf("repair must recover: state %s failure=%+v", done.State, done.FailureCode)
	}
}

func TestWorkerSynthesisEndToEndNoCareWrites(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	fp := newFakePort()
	s := newSvc(pool, defaultLimits(), fp)
	s.PromptVersion = "p1"

	run, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "SYNTHESIS", PlantID: uuidString(w.PlantID)}, nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatalf("process: %v", err)
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State != dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatalf("state %s failure=%+v", done.State, done.FailureCode)
	}
	if done.Capability != string(CapTextAnalyzer) {
		t.Fatalf("synthesis rides the text port: %s", done.Capability)
	}
	if done.Provider.String != "fakeprov" || done.Model.String != "fake-model" ||
		done.PromptVersion.String != "p1" {
		t.Fatalf("provenance row: %+v", done)
	}
	var care, journal int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM care_occurrences`).Scan(&care); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if care != 0 || journal != 0 {
		t.Fatalf("synthesis must not write care/journal state: care=%d journal=%d", care, journal)
	}
}

func TestVisionCapabilityHonestWithoutPort(t *testing.T) {
	r := &Router{Ports: []Port{NoProvider{}}}
	st := r.Capabilities(nil)[CapVisionAnalyzer]
	if st.Available {
		t.Fatal("vision must be honestly unavailable with no port registered")
	}
	if st.Reason == "" {
		t.Fatal("unavailable vision must carry a reason")
	}
}
