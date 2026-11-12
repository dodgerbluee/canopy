package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"canopy/server/internal/platform"
)

// T09 acceptance over real HTTP: the frozen collection operations against
// the shipped resolver stack, with the negative fixtures from
// contracts/fixtures/api/errors.json reproduced end to end.

const plantFields = `
  id name scientificName cultivar rootstock type count category acquiredOn
  plantedOn estimatedAgeYears status revision
  currentContext { id effectiveFrom placement potVolumeLiters mediumNote lightNote
    location { id name situation parent { id name } } }
  acquisition { id seller acquiredOn pricingState revision }
  tags { id label }
  photoCount`

const locationFields = `id name situation label revision parent { id name }`

func payloadProblems(t *testing.T, r gqlResp, field string) []map[string]any {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("want 200 payload channel, got %d: %s", r.Status, r.Raw)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("payload failures must not appear as top-level errors: %s", r.Raw)
	}
	top, ok := r.Data[field].(map[string]any)
	if !ok {
		t.Fatalf("missing payload %s: %s", field, r.Raw)
	}
	raw, _ := json.Marshal(top["problems"])
	var ps []map[string]any
	if err := json.Unmarshal(raw, &ps); err != nil {
		t.Fatalf("problems decode: %v (%s)", err, r.Raw)
	}
	return ps
}

func wantProblemCode(t *testing.T, ps []map[string]any, code string) map[string]any {
	t.Helper()
	for _, p := range ps {
		if p["code"] == code {
			return p
		}
	}
	t.Fatalf("no %s problem in %v", code, ps)
	return nil
}

func dataOf(t *testing.T, r gqlResp, field string) map[string]any {
	t.Helper()
	if r.Status != http.StatusOK || len(r.Errors) != 0 {
		t.Fatalf("clean query failed: %s", r.Raw)
	}
	m, ok := r.Data[field].(map[string]any)
	if !ok {
		t.Fatalf("missing %s: %s", field, r.Raw)
	}
	return m
}

func mustID(t *testing.T, m map[string]any, field string) string {
	t.Helper()
	v, ok := m[field].(string)
	if !ok || v == "" {
		t.Fatalf("missing string field %q in %v", field, m)
	}
	return v
}

func TestCollectionRoundTripOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())

	// Location.
	r := e.gql(`mutation($input: CreateLocationInput!) {
		createLocation(input: $input) { location { `+locationFields+` } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"name": "Kitchen window", "situation": "INDOOR", "label": "east sill"}})
	loc := dataOf(t, r, "createLocation")["location"].(map[string]any)
	locID := mustID(t, loc, "id")

	// Tag.
	r = e.gql(`mutation($input: CreateTagInput!) {
		createTag(input: $input) { tag { id label revision } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"label": "fruit"}})
	tag := dataOf(t, r, "createTag")["tag"].(map[string]any)
	tagID := mustID(t, tag, "id")

	// Plant with unknown provenance, tag link, and initial context.
	r = e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { `+plantFields+` } problems { code field message currentRevision } } }`,
		map[string]any{"input": map[string]any{
			"name": "Meyer lemon", "type": "INDIVIDUAL", "category": "CITRUS",
			"tagIds":         []string{tagID},
			"initialContext": map[string]any{"locationId": locID, "placement": "CONTAINER", "potVolumeLiters": "12.5", "mediumNote": "citrus mix"},
			"acquisition":    map[string]any{"seller": "estate sale", "acquiredOn": "2024-05-01", "pricingState": "UNKNOWN"},
		}})
	plant := dataOf(t, r, "createPlant")["plant"].(map[string]any)
	plantID := mustID(t, plant, "id")

	// Unknowns stay unknown; knowns round-trip.
	if plant["scientificName"] != nil || plant["count"] != nil || plant["plantedOn"] != nil {
		t.Fatalf("unknown fields must be null: %v", plant)
	}
	if plant["acquiredOn"] != "2024-05-01" {
		t.Fatalf("acquiredOn projection: %v", plant["acquiredOn"])
	}
	acq := plant["acquisition"].(map[string]any)
	if acq["pricingState"] != "UNKNOWN" || acq["seller"] != "estate sale" {
		t.Fatalf("acquisition: %v", acq)
	}
	if tags, _ := plant["tags"].([]any); len(tags) != 1 {
		t.Fatalf("tags: %v", plant["tags"])
	}
	ctxRow := plant["currentContext"].(map[string]any)
	if ctxRow["placement"] != "CONTAINER" || ctxRow["potVolumeLiters"] != "12.5" {
		t.Fatalf("context: %v", ctxRow)
	}
	if ctxRow["location"].(map[string]any)["id"] != locID {
		t.Fatalf("context location: %v", ctxRow["location"])
	}
	if plant["photoCount"] != float64(0) {
		t.Fatalf("photoCount: %v", plant["photoCount"])
	}

	// List query finds it.
	r = e.gql(`query($first: Int) { plants(first: $first) { items { `+plantFields+` } pageInfo { hasNextPage endCursor } } }`,
		map[string]any{"first": 25})
	items := dataOf(t, r, "plants")["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != plantID {
		t.Fatalf("plants list: %s", r.Raw)
	}

	// Detail query by id.
	r = e.gql(`query($id: ID!) { plant(id: $id) { `+plantFields+` } }`, map[string]any{"id": plantID})
	if got := dataOf(t, r, "plant"); got["name"] != "Meyer lemon" {
		t.Fatalf("plant detail: %s", r.Raw)
	}
}

// errors.json REVISION_CONFLICT: data-only payload problem with
// currentRevision, never a top-level error.
func TestStaleUpdateRaceOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id revision } problems { code } } }`,
		map[string]any{"input": map[string]any{"name": "Race", "type": "INDIVIDUAL", "category": "OTHER"}})
	plant := dataOf(t, r, "createPlant")["plant"].(map[string]any)
	id := mustID(t, plant, "id")

	upd := func(rev int, name string) gqlResp {
		return e.gql(`mutation($input: UpdatePlantInput!) {
			updatePlant(input: $input) { plant { id name revision } problems { code field message currentRevision } } }`,
			map[string]any{"input": map[string]any{"id": id, "expectedRevision": rev, "name": name}})
	}
	if r := upd(1, "v2"); len(payloadProblems(t, r, "updatePlant")) != 0 {
		t.Fatalf("first update must succeed: %s", r.Raw)
	}
	// Second writer still believes revision 1.
	r = upd(1, "v3")
	ps := payloadProblems(t, r, "updatePlant")
	p := wantProblemCode(t, ps, "CONFLICT")
	if p["field"] != "input.expectedRevision" {
		t.Fatalf("problem field: %v", p["field"])
	}
	if p["currentRevision"] != float64(2) {
		t.Fatalf("currentRevision must be 2: %v", p)
	}
	if top := r.Data["updatePlant"].(map[string]any); top["plant"] != nil {
		t.Fatalf("stale update must not return a plant: %s", r.Raw)
	}
}

// Duplicate create replay: same clientMutationId + same body returns the
// original result; same key with a different body is an honest conflict.
func TestDuplicateCreateReplayOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	mut := `mutation($input: CreatePlantInput!, $clientMutationId: String) {
		createPlant(input: $input, clientMutationId: $clientMutationId) {
			plant { id name revision } problems { code field message } clientMutationId } }`
	in := map[string]any{"name": "Replayed", "type": "INDIVIDUAL", "category": "OTHER"}

	r1 := e.gql(mut, map[string]any{"input": in, "clientMutationId": "cm-1"})
	id1 := mustID(t, dataOf(t, r1, "createPlant")["plant"].(map[string]any), "id")
	r2 := e.gql(mut, map[string]any{"input": in, "clientMutationId": "cm-1"})
	top2 := dataOf(t, r2, "createPlant")
	if top2["clientMutationId"] != "cm-1" {
		t.Fatalf("clientMutationId echo: %s", r2.Raw)
	}
	id2 := mustID(t, top2["plant"].(map[string]any), "id")
	if id1 != id2 {
		t.Fatalf("replay created a second plant: %s vs %s", id1, id2)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM plants WHERE workspace_id=$1 AND name='Replayed'`, e.wsID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("plants=%d err=%v", n, err)
	}

	other := map[string]any{"name": "Different", "type": "INDIVIDUAL", "category": "OTHER"}
	r3 := e.gql(mut, map[string]any{"input": other, "clientMutationId": "cm-1"})
	wantProblemCode(t, payloadProblems(t, r3, "createPlant"), "IDEMPOTENCY_CONFLICT")
}

// A location from another workspace is invisible: NOT_FOUND, never a link.
func TestCrossWorkspaceDeniedOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	ctx := context.Background()
	ids := platform.UUIDv4{}

	// Foreign workspace + location, inserted directly.
	owner := ids.New()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO owner (id, password_hash) VALUES ($1,'x')`, owner); err != nil {
		t.Fatal(err)
	}
	ws := ids.New()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO workspace (id, owner_id, display_name, timezone, default_currency)
		 VALUES ($1,$2,'Foreign','UTC','USD')`, ws, owner); err != nil {
		t.Fatal(err)
	}
	foreignLoc := ids.New()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO locations (id, workspace_id, name, situation) VALUES ($1,$2,'Their shelf','INDOOR')`,
		foreignLoc, ws); err != nil {
		t.Fatal(err)
	}

	r := e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id revision } problems { code } } }`,
		map[string]any{"input": map[string]any{"name": "Mine", "type": "INDIVIDUAL", "category": "OTHER"}})
	plant := dataOf(t, r, "createPlant")["plant"].(map[string]any)

	r = e.gql(`mutation($input: SetPlantContextInput!) {
		setPlantContext(input: $input) { plant { id } context { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"plantId": mustID(t, plant, "id"), "expectedRevision": 1,
			"context": map[string]any{"locationId": uuidStr(foreignLoc), "placement": "CONTAINER"},
		}})
	wantProblemCode(t, payloadProblems(t, r, "setPlantContext"), "NOT_FOUND")

	// And the foreign workspace's rows never appear in my queries.
	r = e.gql(`query { locations { `+locationFields+` } }`, nil)
	if arr, ok := r.Data["locations"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("foreign location leaked: %s", r.Raw)
	}
}

func uuidStr(u pgtype.UUID) string {
	var s string
	for i, b := range u.Bytes {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			s += "-"
		}
		const hex = "0123456789abcdef"
		s += string(hex[b>>4]) + string(hex[b&0xf])
	}
	return s
}

func TestLocationCycleDepthOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	mut := `mutation($input: CreateLocationInput!) {
		createLocation(input: $input) { location { id revision } problems { code field message } } }`

	var chain []string
	var parent any
	for i := 0; i < 5; i++ {
		in := map[string]any{"name": "L", "situation": "INDOOR"}
		if parent != nil {
			in["parentLocationId"] = parent
		}
		r := e.gql(mut, map[string]any{"input": in})
		loc := dataOf(t, r, "createLocation")["location"].(map[string]any)
		chain = append(chain, mustID(t, loc, "id"))
		parent = loc["id"]
	}
	// Sixth level exceeds depth 5.
	r := e.gql(mut, map[string]any{"input": map[string]any{
		"name": "L6", "situation": "INDOOR", "parentLocationId": parent,
	}})
	wantProblemCode(t, payloadProblems(t, r, "createLocation"), "VALIDATION")

	// Cycle: re-parent the root under its own descendant.
	r = e.gql(`mutation($input: UpdateLocationInput!) {
		updateLocation(input: $input) { location { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"id": chain[0], "expectedRevision": 1, "parentLocationId": chain[2],
		}})
	wantProblemCode(t, payloadProblems(t, r, "updateLocation"), "VALIDATION")
}

// errors.json INVALID_CURSOR: top-level GraphQL error, not a payload problem.
func TestInvalidCursorOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(`query($first: Int, $after: String) {
		plants(first: $first, after: $after) { items { id } pageInfo { hasNextPage endCursor } } }`,
		map[string]any{"first": 25, "after": "not-a-valid-cursor"})
	if len(r.Errors) != 1 || r.Errors[0].Message != "Invalid pagination cursor." ||
		r.Errors[0].Extensions["code"] != "VALIDATION" {
		t.Fatalf("want INVALID_CURSOR fixture shape, got %s", r.Raw)
	}
	if r.Data["plants"] != nil {
		t.Fatalf("data must be null for invalid cursor: %s", r.Raw)
	}
}

func TestArchivedVisibilityOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id revision } problems { code } } }`,
		map[string]any{"input": map[string]any{"name": "Ghost", "type": "INDIVIDUAL", "category": "OTHER"}})
	plant := dataOf(t, r, "createPlant")["plant"].(map[string]any)
	id := mustID(t, plant, "id")

	r = e.gql(`mutation($input: ArchivePlantInput!) {
		archivePlant(input: $input) { plant { id status revision } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"id": id, "expectedRevision": 1}})
	got := dataOf(t, r, "archivePlant")["plant"].(map[string]any)
	if got["status"] != "ARCHIVED" || got["revision"] != float64(2) {
		t.Fatalf("archive: %v", got)
	}

	// Default list hides it; explicit ARCHIVED shows it.
	r = e.gql(`query($first: Int) { plants(first: $first) { items { id status } } }`, map[string]any{"first": 25})
	if items := dataOf(t, r, "plants")["items"]; len(items.([]any)) != 0 {
		t.Fatalf("archived plant in default list: %s", r.Raw)
	}
	r = e.gql(`query($first: Int, $filter: PlantFilter) { plants(first: $first, filter: $filter) { items { id status } } }`,
		map[string]any{"first": 25, "filter": map[string]any{"status": []string{"ARCHIVED"}}})
	items := dataOf(t, r, "plants")["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != id {
		t.Fatalf("explicit ARCHIVED filter: %s", r.Raw)
	}

	// Direct read still works and keeps revision.
	r = e.gql(`query($id: ID!) { plant(id: $id) { id status revision } }`, map[string]any{"id": id})
	if got := dataOf(t, r, "plant"); got["status"] != "ARCHIVED" {
		t.Fatalf("archived direct read: %s", r.Raw)
	}
}

func TestPurgePlantOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id revision } problems { code } } }`,
		map[string]any{"input": map[string]any{"name": "Doomed", "type": "INDIVIDUAL", "category": "OTHER"}})
	id := mustID(t, dataOf(t, r, "createPlant")["plant"].(map[string]any), "id")

	mut := `mutation($input: PurgePlantInput!) {
		purgePlant(input: $input) { purgedPlantId problems { code field message } } }`
	// The enum only accepts the exact confirm phrase; anything else is a
	// client-side validation failure at the transport boundary.
	r = e.gql(mut, map[string]any{"input": map[string]any{
		"id": id, "expectedRevision": 1, "confirm": "DELETE",
	}})
	if len(r.Errors) == 0 {
		t.Fatalf("bad confirm phrase must be rejected: %s", r.Raw)
	}
	r = e.gql(mut, map[string]any{"input": map[string]any{
		"id": id, "expectedRevision": 1, "confirm": "PERMANENTLY_DELETE_PLANT",
	}})
	if got := mustID(t, dataOf(t, r, "purgePlant"), "purgedPlantId"); got != id {
		t.Fatalf("purgedPlantId: %s", r.Raw)
	}
	r = e.gql(`query($id: ID!) { plant(id: $id) { id } }`, map[string]any{"id": id})
	if r.Data["plant"] != nil {
		t.Fatalf("purged plant still readable: %s", r.Raw)
	}
}
