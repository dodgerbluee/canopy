package graphql

// T15 acceptance over the real stack: photo/receipt attachment use cases
// through GraphQL mutations, authorized delivery over HTTP, cover-race
// arbitration by the partial unique index, shared-asset preservation and
// the grace-period purge. Isolated disposable schema + real FSStore +
// real River queue (same harness as T13).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"canopy/server/internal/media"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
)

var discardLog = slog.New(slog.DiscardHandler)

// ---- fixtures ---------------------------------------------------------------

func mustPlantGQL(t *testing.T, m *mediaEnv) string {
	t.Helper()
	id := uuid.NewString()
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO plants (id, workspace_id, name, type, category)
		 VALUES ($1,$2,'Test fig','INDIVIDUAL','FIG')`, id, m.wsID)
	if err != nil {
		t.Fatalf("insert plant: %v", err)
	}
	return id
}

func mustPurchaseGQL(t *testing.T, m *mediaEnv) string {
	t.Helper()
	id := uuid.NewString()
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO purchases (id, workspace_id, currency, total_minor)
		 VALUES ($1,$2,'USD',0)`, id, m.wsID)
	if err != nil {
		t.Fatalf("insert purchase: %v", err)
	}
	return id
}

func mustOtherWorkspace(t *testing.T, m *mediaEnv) pgtype.UUID {
	t.Helper()
	ws := uuid.NewString()
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO workspace (id, owner_id, display_name, timezone, default_currency)
		 VALUES ($1,$2,'Other ws','UTC','USD')`, ws, m.ownerID)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var out pgtype.UUID
	if err := out.Scan(ws); err != nil {
		t.Fatal(err)
	}
	return out
}

func putObject(t *testing.T, store platformmedia.Store, key string, b []byte) {
	t.Helper()
	if _, err := store.Stage(key, bytes.NewReader(b), int64(len(b))+16); err != nil {
		t.Fatalf("stage %s: %v", key, err)
	}
	if err := store.Commit(key); err != nil {
		t.Fatalf("commit %s: %v", key, err)
	}
}

// readyAsset inserts a READY asset with real stored objects for the
// original plus the named image variants. Returns the asset id and the
// storage keys actually used (original included under "original").
func readyAsset(t *testing.T, m *mediaEnv, purpose, mime, displayName string, variants map[string][]byte) (string, map[string]string) {
	t.Helper()
	ctx := context.Background()
	assetID := uuid.NewString()
	origKey := uuid.NewString()
	origBytes := variants["original"]

	_, err := m.pool.Exec(ctx,
		`INSERT INTO assets (id, workspace_id, purpose, state, byte_count, mime_type, sha256, storage_key, display_name)
		 VALUES ($1,$2,$3,'READY',$4,$5,$6,$7,$8)`,
		assetID, m.wsID, purpose, len(origBytes), mime, shaHex(origBytes), origKey, displayName)
	if err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	keys := map[string]string{"original": origKey}
	putObject(t, m.store, origKey, origBytes)
	for _, name := range []string{"thumb", "medium", "display"} {
		b, ok := variants[name]
		if !ok {
			continue
		}
		key := uuid.NewString()
		if _, err := m.pool.Exec(ctx,
			`INSERT INTO asset_variants (asset_id, name, width_px, height_px, storage_key, byte_count)
			 VALUES ($1,$2,64,48,$3,$4)`, assetID, name, key, len(b)); err != nil {
			t.Fatalf("insert variant %s: %v", name, err)
		}
		putObject(t, m.store, key, b)
		keys[name] = key
	}
	// receipt originals carry null dimensions; photo originals carry dims
	dims := "1024,768"
	if purpose == "RECEIPT" {
		dims = "NULL,NULL"
	}
	if _, err := m.pool.Exec(ctx,
		fmt.Sprintf(`INSERT INTO asset_variants (asset_id, name, width_px, height_px, storage_key, byte_count)
		 VALUES ($1,'original',%s,$2,$3)`, dims), assetID, origKey, len(origBytes)); err != nil {
		t.Fatalf("insert original variant: %v", err)
	}
	return assetID, keys
}

func assetInWorkspace(t *testing.T, m *mediaEnv, ws pgtype.UUID, purpose, state string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO assets (id, workspace_id, purpose, state, byte_count, sha256, storage_key)
		 VALUES ($1,$2,$3,$4,10,'0000000000000000000000000000000000000000000000000000000000000000',$5)`,
		id, ws, purpose, state, uuid.NewString())
	if err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	return id
}

// ---- mutation helpers --------------------------------------------------------

const photoFieldsGQL = `id plantId isCover revision dateSource viewpoint takenAt timezone caption
  asset { id state purpose variants { name widthPx heightPx } }`

func runMut(t *testing.T, m *mediaEnv, name, inputType string, input map[string]any, cmid *string, fields string) (map[string]any, []map[string]any) {
	t.Helper()
	q := fmt.Sprintf(`mutation($input: %s!, $cmid: String) {
  %s(input: $input, clientMutationId: $cmid) { %s problems { code field message currentRevision } clientMutationId }
}`, inputType, name, fields)
	res := m.gql(q, map[string]any{"input": input, "cmid": cmid})
	if len(res.Errors) > 0 {
		t.Fatalf("%s transport errors: %s", name, res.Raw)
	}
	payload := res.Data[name].(map[string]any)
	var probs []map[string]any
	if ps, ok := payload["problems"].([]any); ok {
		for _, p := range ps {
			probs = append(probs, p.(map[string]any))
		}
	}
	return payload, probs
}

func firstCode(probs []map[string]any) string {
	if len(probs) == 0 {
		return ""
	}
	return probs[0]["code"].(string)
}

func mustCode(t *testing.T, probs []map[string]any, want string) {
	t.Helper()
	if got := firstCode(probs); got != want {
		t.Fatalf("problem code = %q, want %s (%+v)", got, want, probs)
	}
}

// ---- delivery helpers ----------------------------------------------------------

func getMedia(m *mediaEnv, assetID, variant string, ck *http.Cookie, extra map[string]string) *http.Response {
	req, _ := http.NewRequest("GET", m.srv.URL+"/api/media/"+assetID+"/"+variant, nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatalf("GET media: %v", err)
	}
	return res
}

func purgeJobs(t *testing.T, m *mediaEnv) (int, time.Time) {
	t.Helper()
	var n int
	var sched time.Time
	err := m.pool.QueryRow(context.Background(),
		`SELECT count(*)::int, coalesce(min(scheduled_at), 'epoch'::timestamptz)
		 FROM river_job WHERE kind = 'media:purge_asset'`).Scan(&n, &sched)
	if err != nil {
		t.Fatalf("river jobs: %v", err)
	}
	return n, sched
}

func assetState(t *testing.T, m *mediaEnv, assetID string) string {
	t.Helper()
	var s string
	if err := m.pool.QueryRow(context.Background(),
		`SELECT state::text FROM assets WHERE id = $1`, assetID).Scan(&s); err != nil {
		t.Fatalf("asset state: %v", err)
	}
	return s
}

func storeHas(t *testing.T, m *mediaEnv, key string) bool {
	ok, err := m.store.Exists(key)
	if err != nil {
		t.Fatalf("store exists: %v", err)
	}
	return ok
}

func runPurge(t *testing.T, m *mediaEnv, assetID string) {
	t.Helper()
	w := &media.PurgeWorker{Pool: m.pool, Store: m.store, Log: discardLog}
	job := &river.Job[media.PurgeAssetArgs]{Args: media.PurgeAssetArgs{AssetID: assetID}}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("purge worker: %v", err)
	}
}

// ---- tests ---------------------------------------------------------------------

func TestAttachPhotoDeliveryAndCover(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	displayBytes := jpegBytes(900)
	assetID, keys := readyAsset(t, m, "PHOTO", "image/jpeg", "field notes.jpg", map[string][]byte{
		"thumb":    jpegBytes(120),
		"medium":   jpegBytes(500),
		"display":  displayBytes,
		"original": jpegBytes(3000),
	})

	takenAt := "2026-10-31T15:30:00Z"
	payload, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": assetID, "dateSource": "USER",
		"takenAt": takenAt, "viewpoint": "WHOLE", "isCover": true,
	}, strpGQL("cm-attach-1"), "photo { "+photoFieldsGQL+" }")
	if len(probs) > 0 {
		t.Fatalf("attachPhoto problems: %+v", probs)
	}
	photo := payload["photo"].(map[string]any)
	photoID := photo["id"].(string)
	if photo["isCover"] != true || photo["revision"].(float64) != 1 {
		t.Fatalf("photo = %v", photo)
	}
	if photo["takenAt"] != takenAt {
		t.Fatalf("takenAt = %v, want %s", photo["takenAt"], takenAt)
	}
	if vs := photo["asset"].(map[string]any)["variants"].([]any); len(vs) != 4 {
		t.Fatalf("asset variants = %d, want 4", len(vs))
	}

	// plant graph: cover + count
	res := m.gql(`query($id: ID!) { plant(id: $id) { id photoCount coverPhoto { id isCover } } }`,
		map[string]any{"id": plantID})
	plant := res.Data["plant"].(map[string]any)
	if plant["photoCount"].(float64) != 1 {
		t.Fatalf("photoCount = %v", plant["photoCount"])
	}
	if plant["coverPhoto"].(map[string]any)["id"].(string) != photoID {
		t.Fatalf("coverPhoto = %v, want %s", plant["coverPhoto"], photoID)
	}

	// delivery: authenticated owner gets the derivative with safe headers
	res2 := getMedia(m, assetID, "display", m.cookie, nil)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("GET display = %d", res2.StatusCode)
	}
	body, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if !bytes.Equal(body, displayBytes) {
		t.Fatal("delivered bytes differ from stored derivative")
	}
	if ct := res2.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := res2.Header.Get("Content-Disposition"); cd != `inline; filename="field notes.jpg"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if res2.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if cc := res2.Header.Get("Cache-Control"); cc != "private, no-cache" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	etag := fmt.Sprintf(`"%s-display-%d"`, assetID, len(displayBytes))
	if got := res2.Header.Get("ETag"); got != etag {
		t.Fatalf("ETag = %q, want %s", got, etag)
	}
	if cl := res2.Header.Get("Content-Length"); cl != strconv.Itoa(len(displayBytes)) {
		t.Fatalf("Content-Length = %q", cl)
	}

	// conditional revalidation
	res3 := getMedia(m, assetID, "display", m.cookie, map[string]string{"If-None-Match": etag})
	if res3.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match = %d, want 304", res3.StatusCode)
	}
	res3.Body.Close()

	// original is an attachment download
	res4 := getMedia(m, assetID, "original", m.cookie, nil)
	if res4.StatusCode != http.StatusOK {
		t.Fatalf("GET original = %d", res4.StatusCode)
	}
	res4.Body.Close()
	if cd := res4.Header.Get("Content-Disposition"); cd != `attachment; filename="field notes.jpg"` {
		t.Fatalf("original Content-Disposition = %q", cd)
	}

	// unauthenticated: 401, never bytes
	res5 := getMedia(m, assetID, "display", nil, nil)
	if res5.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET = %d, want 401", res5.StatusCode)
	}
	res5.Body.Close()

	// storage keys are never accepted as input
	res6 := getMedia(m, keys["display"], "display", m.cookie, nil)
	if res6.StatusCode != http.StatusNotFound {
		t.Fatalf("storage-key request = %d, want 404", res6.StatusCode)
	}
	res6.Body.Close()
}

func TestAttachPhotoNegatives(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)

	// wrong purpose: RECEIPT asset cannot become a plant photo
	receiptID, _ := readyAsset(t, m, "RECEIPT", "application/pdf", "r.pdf",
		map[string][]byte{"original": []byte("%PDF-1.4 fake")})
	_, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": receiptID, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, nil, "photo { id }")
	mustCode(t, probs, "VALIDATION")

	// not READY: PROCESSING photo serves nothing
	processing := assetInWorkspace(t, m, m.wsID, "PHOTO", "PROCESSING")
	_, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": processing, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, nil, "photo { id }")
	mustCode(t, probs, "VALIDATION")

	// cross-workspace asset: indistinguishable from unknown
	other := mustOtherWorkspace(t, m)
	foreign := assetInWorkspace(t, m, other, "PHOTO", "READY")
	_, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": foreign, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, nil, "photo { id }")
	mustCode(t, probs, "NOT_FOUND")

	// cross-workspace delivery is a plain 404 too
	res := getMedia(m, foreign, "original", m.cookie, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-ws delivery = %d, want 404", res.StatusCode)
	}
	res.Body.Close()

	// unknown variant name: 404
	photoAsset, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "p.jpg",
		map[string][]byte{"original": jpegBytes(50), "display": jpegBytes(30)})
	res = getMedia(m, photoAsset, "full", m.cookie, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown variant = %d, want 404", res.StatusCode)
	}
	res.Body.Close()

	// no implicit sharing: one live link per photo asset
	plant2 := mustPlantGQL(t, m)
	_, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": photoAsset, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, nil, "photo { id }")
	if len(probs) > 0 {
		t.Fatalf("first attach: %+v", probs)
	}
	_, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plant2, "assetId": photoAsset, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, nil, "photo { id }")
	mustCode(t, probs, "CONFLICT") // other plant
	_, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": photoAsset, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, nil, "photo { id }")
	mustCode(t, probs, "CONFLICT") // same plant re-attach

	// idempotent replay: same cmid returns the same photo, no second row
	replayAsset, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "replay.jpg",
		map[string][]byte{"original": jpegBytes(50)})
	payload, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": replayAsset, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, strpGQL("cm-replay"), "photo { id }")
	if len(probs) > 0 {
		t.Fatalf("replay first: %+v", probs)
	}
	firstID := payload["photo"].(map[string]any)["id"].(string)
	payload, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": replayAsset, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
	}, strpGQL("cm-replay"), "photo { id }")
	if len(probs) > 0 {
		t.Fatalf("replay: %+v", probs)
	}
	if got := payload["photo"].(map[string]any)["id"].(string); got != firstID {
		t.Fatalf("replay photo id = %s, want %s", got, firstID)
	}
	var n int
	if err := m.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM plant_photos WHERE asset_id = $1 AND deleted_at IS NULL`,
		replayAsset).Scan(&n); err != nil || n != 1 {
		t.Fatalf("live links = %d (err %v), want 1", n, err)
	}

	// invalid enum value is a transport-level validation error, never a 200
	resBad := m.gql(`mutation($input: AttachPhotoInput!) {
  attachPhoto(input: $input) { photo { id } problems { code } }
}`, map[string]any{"input": map[string]any{
		"plantId": plantID, "assetId": photoAsset, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "BOGUS",
	}})
	if len(resBad.Errors) == 0 && firstCode(mustProblems(t, resBad.Raw)) == "" {
		t.Fatal("bad viewpoint accepted")
	}
}

func mustProblems(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var env struct {
		Data map[string]struct {
			Problems []map[string]any `json:"problems"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	for _, p := range env.Data {
		return p.Problems
	}
	return nil
}

func TestPhotoDateSemantics(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)

	// USER without an explicit instant is refused: never invent a date
	a1, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "a1.jpg", map[string][]byte{"original": jpegBytes(40)})
	_, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": a1, "dateSource": "USER", "viewpoint": "WHOLE",
	}, nil, "photo { id }")
	mustCode(t, probs, "VALIDATION")

	// UPLOAD_FALLBACK labels the upload instant honestly
	a2, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "a2.jpg", map[string][]byte{"original": jpegBytes(40)})
	payload, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": a2, "dateSource": "UPLOAD_FALLBACK", "viewpoint": "WHOLE",
	}, nil, "photo { takenAt dateSource asset { createdAt } }")
	if len(probs) > 0 {
		t.Fatalf("upload_fallback: %+v", probs)
	}
	ph := payload["photo"].(map[string]any)
	if ph["dateSource"] != "UPLOAD_FALLBACK" {
		t.Fatalf("dateSource = %v", ph["dateSource"])
	}
	if ph["takenAt"] != ph["asset"].(map[string]any)["createdAt"] {
		t.Fatalf("takenAt %v != asset createdAt %v", ph["takenAt"], ph["asset"].(map[string]any)["createdAt"])
	}

	// EXIF may stay null transiently (naive timestamp awaiting timezone)
	a3, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "a3.jpg", map[string][]byte{"original": jpegBytes(40)})
	payload, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": a3, "dateSource": "EXIF", "viewpoint": "LEAF",
	}, nil, "photo { takenAt dateSource }")
	if len(probs) > 0 {
		t.Fatalf("exif null takenAt: %+v", probs)
	}
	if payload["photo"].(map[string]any)["takenAt"] != nil {
		t.Fatalf("exif takenAt = %v, want null", payload["photo"].(map[string]any)["takenAt"])
	}

	// bad IANA zone is a validation problem, not a 500
	a4, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "a4.jpg", map[string][]byte{"original": jpegBytes(40)})
	_, probs = runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": a4, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE", "timezone": "Mars/Phobos",
	}, nil, "photo { id }")
	mustCode(t, probs, "VALIDATION")
}

func TestCoverUniquenessAndRace(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	ids := []string{}
	for i := range 4 {
		a, _ := readyAsset(t, m, "PHOTO", "image/jpeg", fmt.Sprintf("p%d.jpg", i),
			map[string][]byte{"original": jpegBytes(40 + i)})
		payload, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
			"plantId": plantID, "assetId": a, "dateSource": "USER",
			"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE",
		}, nil, "photo { id revision }")
		if len(probs) > 0 {
			t.Fatalf("attach %d: %+v", i, probs)
		}
		ids = append(ids, payload["photo"].(map[string]any)["id"].(string))
	}

	// sequential cover moves: exactly one cover survives
	_, probs := runMut(t, m, "updatePhoto", "UpdatePhotoInput", map[string]any{
		"id": ids[0], "expectedRevision": 1, "isCover": true,
	}, nil, "photo { id isCover revision }")
	if len(probs) > 0 {
		t.Fatalf("cover p0: %+v", probs)
	}
	payload, probs := runMut(t, m, "updatePhoto", "UpdatePhotoInput", map[string]any{
		"id": ids[1], "expectedRevision": 1, "isCover": true,
	}, nil, "photo { id isCover revision }")
	if len(probs) > 0 {
		t.Fatalf("cover p1: %+v", probs)
	}
	if payload["photo"].(map[string]any)["isCover"] != true {
		t.Fatal("p1 should be cover")
	}
	res := m.gql(`query($id: ID!) { plant(id: $id) { coverPhoto { id } } }`, map[string]any{"id": plantID})
	if got := res.Data["plant"].(map[string]any)["coverPhoto"].(map[string]any)["id"]; got != ids[1] {
		t.Fatalf("cover = %v, want %s (previous cover must clear)", got, ids[1])
	}

	// race: two concurrent cover claims — the partial unique index is the
	// arbiter; the plant must end with exactly one cover
	var wg sync.WaitGroup
	codes := make([]string, 2)
	for i, pid := range []string{ids[2], ids[3]} {
		wg.Add(1)
		go func(i int, pid string) {
			defer wg.Done()
			_, probs := runMut(t, m, "updatePhoto", "UpdatePhotoInput", map[string]any{
				"id": pid, "expectedRevision": 1, "isCover": true,
			}, nil, "photo { id isCover }")
			codes[i] = firstCode(probs)
		}(i, pid)
	}
	wg.Wait()
	for i, c := range codes {
		if c != "" && c != "CONFLICT" {
			t.Fatalf("race loser got %q (photo %s)", c, []string{ids[2], ids[3]}[i])
		}
	}
	var covers int
	if err := m.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM plant_photos WHERE plant_id = $1 AND is_cover AND deleted_at IS NULL`,
		plantID).Scan(&covers); err != nil || covers != 1 {
		t.Fatalf("covers = %d (err %v), want exactly 1; codes=%v", covers, err, codes)
	}
}

func TestUpdateDeletePhotoRevisionAndPurge(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	assetID, keys := readyAsset(t, m, "PHOTO", "image/jpeg", "gone.jpg",
		map[string][]byte{"original": jpegBytes(80), "display": jpegBytes(40)})
	payload, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": assetID, "dateSource": "USER",
		"takenAt": "2026-10-31T00:00:00Z", "viewpoint": "WHOLE", "isCover": true,
	}, nil, "photo { id revision }")
	if len(probs) > 0 {
		t.Fatalf("attach: %+v", probs)
	}
	photoID := payload["photo"].(map[string]any)["id"].(string)

	// stale revision: CONFLICT with the current revision reported
	_, probs = runMut(t, m, "updatePhoto", "UpdatePhotoInput", map[string]any{
		"id": photoID, "expectedRevision": 99, "caption": "nope",
	}, nil, "photo { id }")
	mustCode(t, probs, "CONFLICT")
	if cr, ok := probs[0]["currentRevision"].(float64); !ok || cr != 1 {
		t.Fatalf("currentRevision = %v, want 1", probs[0]["currentRevision"])
	}

	// delete with the right revision
	_, probs = runMut(t, m, "deletePhoto", "DeletePhotoInput", map[string]any{
		"id": photoID, "expectedRevision": 1,
	}, nil, "deletedPhotoId")
	if len(probs) > 0 {
		t.Fatalf("deletePhoto: %+v", probs)
	}

	// plant graph drops the photo and the cover
	res := m.gql(`query($id: ID!) { plant(id: $id) { photoCount coverPhoto { id } } }`, map[string]any{"id": plantID})
	plant := res.Data["plant"].(map[string]any)
	if plant["photoCount"].(float64) != 0 || plant["coverPhoto"] != nil {
		t.Fatalf("plant after delete = %v", plant)
	}

	// last link gone: asset hidden immediately (DELETING) + purge job in grace
	if st := assetState(t, m, assetID); st != "DELETING" {
		t.Fatalf("asset state = %s, want DELETING", st)
	}
	del := getMedia(m, assetID, "display", m.cookie, nil)
	if del.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETING asset delivery = %d, want 404", del.StatusCode)
	}
	del.Body.Close()
	n, sched := purgeJobs(t, m)
	if n != 1 {
		t.Fatalf("purge jobs = %d, want 1", n)
	}
	if until := time.Until(sched); until < 6*24*time.Hour || until > 8*24*time.Hour {
		t.Fatalf("purge scheduled_at %v (in %v), want ~7 days out", sched, until)
	}

	// running the purge removes every stored object, row stays DELETING
	runPurge(t, m, assetID)
	for _, k := range keys {
		if storeHas(t, m, k) {
			t.Fatalf("object %s survived purge", k)
		}
	}
	if st := assetState(t, m, assetID); st != "DELETING" {
		t.Fatalf("asset row state after purge = %s, want DELETING tombstone", st)
	}
}

func TestReceiptLinkingSharedAsset(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	p1 := mustPurchaseGQL(t, m)
	p2 := mustPurchaseGQL(t, m)
	assetID, keys := readyAsset(t, m, "RECEIPT", "application/pdf", "receipt.pdf",
		map[string][]byte{"original": []byte("%PDF-1.4 shared receipt")})

	// wrong purpose: PHOTO asset cannot document a purchase
	photoAsset, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "not-a-receipt.jpg",
		map[string][]byte{"original": jpegBytes(30)})
	_, probs := runMut(t, m, "attachReceipt", "AttachReceiptInput", map[string]any{
		"purchaseId": p1, "assetId": photoAsset,
	}, nil, "receipt { id }")
	mustCode(t, probs, "VALIDATION")

	// attach to purchase 1
	payload, probs := runMut(t, m, "attachReceipt", "AttachReceiptInput", map[string]any{
		"purchaseId": p1, "assetId": assetID,
	}, strpGQL("cm-r-1"), "receipt { id purchaseId asset { id state variants { name widthPx } } }")
	if len(probs) > 0 {
		t.Fatalf("attachReceipt: %+v", probs)
	}
	rec := payload["receipt"].(map[string]any)
	receiptID := rec["id"].(string)
	if rec["purchaseId"] != p1 {
		t.Fatalf("receipt purchaseId = %v", rec["purchaseId"])
	}
	vs := rec["asset"].(map[string]any)["variants"].([]any)
	if len(vs) != 1 || vs[0].(map[string]any)["name"] != "original" || vs[0].(map[string]any)["widthPx"] != nil {
		t.Fatalf("receipt variants = %v, want single null-dims original", vs)
	}

	// duplicate on the same purchase: CONFLICT
	_, probs = runMut(t, m, "attachReceipt", "AttachReceiptInput", map[string]any{
		"purchaseId": p1, "assetId": assetID,
	}, nil, "receipt { id }")
	mustCode(t, probs, "CONFLICT")

	// sharing across purchases is allowed
	payload, probs = runMut(t, m, "attachReceipt", "AttachReceiptInput", map[string]any{
		"purchaseId": p2, "assetId": assetID,
	}, nil, "receipt { id }")
	if len(probs) > 0 {
		t.Fatalf("second attachReceipt: %+v", probs)
	}
	receiptID2 := payload["receipt"].(map[string]any)["id"].(string)

	// purchase graph carries receipts with assets
	res := m.gql(`query($id: ID!) { purchase(id: $id) { id receipts { id asset { id state } } } }`,
		map[string]any{"id": p1})
	po := res.Data["purchase"].(map[string]any)
	if rs := po["receipts"].([]any); len(rs) != 1 {
		t.Fatalf("purchase receipts = %v", rs)
	}

	// receipt delivery: attachment-only, image variants never exist
	r0 := getMedia(m, assetID, "original", m.cookie, nil)
	if r0.StatusCode != http.StatusOK {
		t.Fatalf("receipt original = %d", r0.StatusCode)
	}
	r0.Body.Close()
	if cd := r0.Header.Get("Content-Disposition"); cd != `attachment; filename="receipt.pdf"` {
		t.Fatalf("receipt disposition = %q", cd)
	}
	r1 := getMedia(m, assetID, "thumb", m.cookie, nil)
	if r1.StatusCode != http.StatusNotFound {
		t.Fatalf("receipt thumb = %d, want 404", r1.StatusCode)
	}
	r1.Body.Close()

	// detach one link: shared asset preserved, still deliverable
	rev := purchaseRevision(t, m, p1)
	_, probs = runMut(t, m, "detachReceipt", "DetachReceiptInput", map[string]any{
		"purchaseId": p1, "receiptId": receiptID, "expectedRevision": rev,
	}, nil, "detachedReceiptId")
	if len(probs) > 0 {
		t.Fatalf("detach p1: %+v", probs)
	}
	if st := assetState(t, m, assetID); st != "READY" {
		t.Fatalf("shared asset state = %s, want READY until last link", st)
	}
	r2 := getMedia(m, assetID, "original", m.cookie, nil)
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("shared asset after partial detach = %d, want 200", r2.StatusCode)
	}
	r2.Body.Close()

	// stale revision on the second detach: CONFLICT with purchase revision
	_, probs = runMut(t, m, "detachReceipt", "DetachReceiptInput", map[string]any{
		"purchaseId": p2, "receiptId": receiptID2, "expectedRevision": rev + 50,
	}, nil, "detachedReceiptId")
	mustCode(t, probs, "CONFLICT")

	// last link: DELETING + delivery stops + purge job; purge preserves nothing
	rev2 := purchaseRevision(t, m, p2)
	_, probs = runMut(t, m, "detachReceipt", "DetachReceiptInput", map[string]any{
		"purchaseId": p2, "receiptId": receiptID2, "expectedRevision": rev2,
	}, nil, "detachedReceiptId")
	if len(probs) > 0 {
		t.Fatalf("detach p2: %+v", probs)
	}
	if st := assetState(t, m, assetID); st != "DELETING" {
		t.Fatalf("asset after last detach = %s, want DELETING", st)
	}
	r3 := getMedia(m, assetID, "original", m.cookie, nil)
	if r3.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETING receipt delivery = %d, want 404", r3.StatusCode)
	}
	r3.Body.Close()
	if n, _ := purgeJobs(t, m); n != 1 {
		t.Fatalf("purge jobs = %d, want 1", n)
	}
	runPurge(t, m, assetID)
	if storeHas(t, m, keys["original"]) {
		t.Fatal("receipt object survived purge")
	}
}

func TestPurgeWorkerGuards(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)

	// referenced DELETING asset: the run-time re-check protects the bytes
	assetID, keys := readyAsset(t, m, "PHOTO", "image/jpeg", "kept.jpg",
		map[string][]byte{"original": jpegBytes(60)})
	if _, err := m.pool.Exec(context.Background(),
		`UPDATE assets SET state = 'DELETING' WHERE id = $1`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pool.Exec(context.Background(),
		`INSERT INTO plant_photos (id, workspace_id, plant_id, asset_id, date_source, viewpoint)
		 VALUES ($1,$2,$3,$4,'USER','WHOLE')`, uuid.NewString(), m.wsID, plantID, assetID); err != nil {
		t.Fatal(err)
	}
	runPurge(t, m, assetID)
	if !storeHas(t, m, keys["original"]) {
		t.Fatal("purge deleted a still-referenced asset")
	}

	// non-DELETING asset: the job never revives or deletes
	assetID2, keys2 := readyAsset(t, m, "PHOTO", "image/jpeg", "ready.jpg",
		map[string][]byte{"original": jpegBytes(60)})
	runPurge(t, m, assetID2)
	if !storeHas(t, m, keys2["original"]) {
		t.Fatal("purge deleted a READY asset")
	}
}

func purchaseRevision(t *testing.T, m *mediaEnv, purchaseID string) int32 {
	t.Helper()
	var rev int32
	if err := m.pool.QueryRow(context.Background(),
		`SELECT revision FROM purchases WHERE id = $1`, purchaseID).Scan(&rev); err != nil {
		t.Fatalf("purchase revision: %v", err)
	}
	return rev
}

func strpGQL(s string) *string { return &s }

// ---- end-to-end: real HEIC through the pipeline, delivered GPS-free ---------

func TestPhotoPipelineDeliveryGPSFree(t *testing.T) {
	if out, err := exec.Command("docker", "image", "inspect", "canopy-media:r2").CombinedOutput(); err != nil {
		t.Skipf("media image not present (make media-image): %s", strings.TrimSpace(string(out)))
	}
	body, err := os.ReadFile("../../../testdata/media-corpus/iphone-12mp.heic")
	if err != nil {
		t.Fatal(err)
	}
	m := newMediaEnv(t, 50<<20)
	plantID := mustPlantGQL(t, m)

	// real upload flow: reserve, PUT bytes, finalize (enqueues the job)
	asset := m.reserve(t, "PHOTO", "IMG_0001.heic", len(body), nil)
	assetID := asset["id"].(string)
	if code, resp := m.put(t, assetID, body, m.cookie, m.csrf); code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", code, resp)
	}
	res := m.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": assetID, "sha256": shaHex(body)},
	})
	if res.Data["finalizeUpload"].(map[string]any)["asset"].(map[string]any)["state"] != "PROCESSING" {
		t.Fatalf("finalize: %s", res.Raw)
	}

	// run the processor exactly as the worker binary would
	w := &processor.Worker{
		Pool: m.pool, Store: m.store,
		SB:         &processor.DockerSandbox{Image: "canopy-media:r2", Memory: "512m", PIDs: 64},
		MaxPixels:  processor.DefaultMaxPixels,
		JobTimeout: 180 * time.Second,
		Log:        discardLog,
	}
	job := &river.Job[media.ProcessAssetArgs]{Args: media.ProcessAssetArgs{AssetID: assetID}}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("processor: %v", err)
	}
	if st := assetState(t, m, assetID); st != "READY" {
		t.Fatalf("state after processing = %s, want READY", st)
	}

	// attach with EXIF semantics (naive timestamp timezone unresolved -> null)
	payload, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{
		"plantId": plantID, "assetId": assetID, "dateSource": "EXIF", "viewpoint": "WHOLE", "isCover": true,
	}, nil, "photo { id asset { variants { name widthPx heightPx } } }")
	if len(probs) > 0 {
		t.Fatalf("attachPhoto: %+v", probs)
	}
	vs := payload["photo"].(map[string]any)["asset"].(map[string]any)["variants"].([]any)
	if len(vs) != 4 {
		t.Fatalf("variants = %v, want 4", vs)
	}
	var disp map[string]any
	for _, v := range vs {
		mv := v.(map[string]any)
		if mv["name"] == "display" {
			disp = mv
		}
	}
	// orientation 6 source: derivatives must be upright (portrait: h > w)
	if disp["heightPx"].(float64) <= disp["widthPx"].(float64) {
		t.Fatalf("display not portrait: %v", disp)
	}

	// delivered display: JPEG magic, zero EXIF/GPS payload
	r := getMedia(m, assetID, "display", m.cookie, nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET display = %d", r.StatusCode)
	}
	got, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.HasPrefix(got, []byte{0xFF, 0xD8, 0xFF}) {
		t.Fatal("display is not JPEG")
	}
	if bytes.Contains(got, []byte("Exif")) || bytes.Contains(got, []byte("GPS")) {
		t.Fatal("display derivative carries EXIF/GPS metadata")
	}

	// original is delivered as a private attachment and is also metadata-free
	r = getMedia(m, assetID, "original", m.cookie, nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET original = %d", r.StatusCode)
	}
	orig, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if cd := r.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("original disposition = %q", cd)
	}
	if bytes.Contains(orig, []byte("Exif")) || bytes.Contains(orig, []byte("GPS")) {
		t.Fatal("stored original carries EXIF/GPS metadata")
	}
}
