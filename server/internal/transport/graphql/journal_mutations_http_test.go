// T17 verification: activity/measurement mutations over HTTP — unit
// validation, backdating, revision guards, voiding, idempotency, the
// corrections audit and the transactional journal projection (union
// members ACTIVITY/MEASUREMENT on the frozen journal query).
package graphql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const journalMixedSel = `
query Journal($plantId: ID, $first: Int, $after: String, $filter: JournalFilter) {
  journal(plantId: $plantId, first: $first, after: $after, filter: $filter) {
    items {
      __typename
      ... on ActivityEntry {
        id plantId occurredAt createdAt summary tombstonedAt
        activity { id kind occurredAt timezone note quantity unit product
                   revision voidedAt }
      }
      ... on MeasurementEntry {
        id plantId occurredAt createdAt summary tombstonedAt
        measurement { id quantityKind value unit observedAt method
                      uncertainty activityId revision }
      }
      ... on PhotoEntry {
        id plantId occurredAt createdAt summary tombstonedAt
        photo { id takenAt dateSource }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

func mixedJournal(t *testing.T, m *mediaEnv, vars map[string]any) []map[string]any {
	t.Helper()
	r := m.gql(journalMixedSel, vars)
	if len(r.Errors) > 0 {
		t.Fatalf("journal query errors: %v", r.Raw)
	}
	var out struct {
		Journal struct {
			Items []map[string]any `json:"items"`
		} `json:"journal"`
	}
	if err := remarshal(r.Data, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.Journal.Items
}

func mustNoProblems(t *testing.T, probs []map[string]any, what string) {
	t.Helper()
	if len(probs) > 0 {
		t.Fatalf("%s: unexpected problems %v", what, probs)
	}
}

func actFields() string {
	return `activity { id plantId kind occurredAt timezone note quantity unit product
	                 revision voidedAt createdAt updatedAt }`
}

func measFields() string {
	return `measurement { id plantId quantityKind value unit observedAt method
	                     uncertainty activityId revision createdAt updatedAt }`
}

func TestActivityLifecycleHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	plantID := mustPlantGQL(t, m)
	t0 := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)

	// --- record: WATER carries quantity+unit; timezone defaults to workspace
	payload, probs := runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{
		"plantId": plantID, "kind": "WATER", "occurredAt": t0.Format(time.RFC3339Nano),
		"quantity": "2", "unit": "L",
	}, nil, actFields())
	mustNoProblems(t, probs, "recordActivity WATER")
	act := payload["activity"].(map[string]any)
	actID := act["id"].(string)
	if act["timezone"] != "UTC" {
		t.Fatalf("timezone default = %v, want workspace default UTC", act["timezone"])
	}
	if act["quantity"] != "2" || act["unit"] != "L" || act["revision"] != float64(1) {
		t.Fatalf("activity view wrong: %v", act)
	}
	if act["voidedAt"] != nil {
		t.Fatal("fresh activity must not be voided")
	}

	// --- validation matrix (kind/unit pairing refused before the DB CHECK)
	for _, c := range []struct {
		name  string
		input map[string]any
		code  string
	}{
		{"water-quantity-without-unit", map[string]any{"plantId": plantID, "kind": "WATER",
			"occurredAt": t0.Format(time.RFC3339Nano), "quantity": "2"}, "VALIDATION"},
		{"water-wrong-unit", map[string]any{"plantId": plantID, "kind": "WATER",
			"occurredAt": t0.Format(time.RFC3339Nano), "quantity": "2", "unit": "KG"}, "VALIDATION"},
		{"note-with-quantity", map[string]any{"plantId": plantID, "kind": "NOTE",
			"occurredAt": t0.Format(time.RFC3339Nano), "quantity": "2", "unit": "L"}, "VALIDATION"},
		{"bogus-timezone", map[string]any{"plantId": plantID, "kind": "PRUNE",
			"occurredAt": t0.Format(time.RFC3339Nano), "timezone": "Mars/Olympus_Mons"}, "VALIDATION"},
		{"unknown-plant", map[string]any{"plantId": uuid.NewString(), "kind": "PRUNE",
			"occurredAt": t0.Format(time.RFC3339Nano)}, "NOT_FOUND"},
	} {
		_, probs := runMut(t, m, "recordActivity", "RecordActivityInput", c.input, nil, actFields())
		if code := firstCode(probs); code != c.code {
			t.Fatalf("%s: code %q want %q (%v)", c.name, code, c.code, probs)
		}
	}
	// wrong-unit problem quotes expected and found values
	_, probs = runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{
		"plantId": plantID, "kind": "WATER", "occurredAt": t0.Format(time.RFC3339Nano),
		"quantity": "2", "unit": "KG",
	}, nil, actFields())
	msg, _ := probs[0]["message"].(string)
	if msg == "" || !strings.Contains(msg, "L or ML") || !strings.Contains(msg, "KG") {
		t.Fatalf("unit problem must quote values, got %q", msg)
	}

	// --- backdated FERTILIZE lands behind the newer WATER in the timeline
	tOld := t0.Add(-24 * time.Hour)
	payload, probs = runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{
		"plantId": plantID, "kind": "FERTILIZE", "occurredAt": tOld.Format(time.RFC3339Nano),
		"quantity": "1.5", "unit": "KG", "product": "10-10-10",
	}, nil, actFields())
	mustNoProblems(t, probs, "recordActivity FERTILIZE")
	if got := payload["activity"].(map[string]any)["quantity"]; got != "1.5" {
		t.Fatalf("decimal fidelity: quantity %v", got)
	}

	items := mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"ACTIVITY"}}})
	if len(items) != 2 {
		t.Fatalf("want 2 activity entries, got %d: %v", len(items), items)
	}
	if items[0]["__typename"] != "ActivityEntry" {
		t.Fatalf("item 0 typename %v", items[0]["__typename"])
	}
	if items[0]["summary"] != "Watered 2 L" || items[1]["summary"] != "Fertilized 1.5 KG (10-10-10)" {
		t.Fatalf("summaries wrong: %v / %v", items[0]["summary"], items[1]["summary"])
	}
	if items[0]["occurredAt"] != t0.Format(time.RFC3339Nano) {
		t.Fatalf("backdated ordering wrong: %v %v", items[0]["occurredAt"], items[1]["occurredAt"])
	}

	// --- correction: wrong revision CONFLICTs naming currentRevision
	_, probs = runMut(t, m, "updateActivity", "UpdateActivityInput", map[string]any{
		"id": actID, "expectedRevision": 5, "quantity": "3", "unit": "L",
	}, nil, actFields())
	if firstCode(probs) != "CONFLICT" {
		t.Fatalf("want CONFLICT, got %v", probs)
	}
	if cr, _ := probs[0]["currentRevision"].(float64); cr != 1 {
		t.Fatalf("CONFLICT must name currentRevision 1, got %v", probs[0]["currentRevision"])
	}

	// --- correction moves the entry atomically (one entry, new position)
	tNew := t0.Add(72 * time.Hour)
	payload, probs = runMut(t, m, "updateActivity", "UpdateActivityInput", map[string]any{
		"id": actID, "expectedRevision": 1,
		"occurredAt": tNew.Format(time.RFC3339Nano), "quantity": "3", "unit": "L",
	}, nil, actFields())
	mustNoProblems(t, probs, "updateActivity")
	act = payload["activity"].(map[string]any)
	if act["revision"] != float64(2) || act["quantity"] != "3" {
		t.Fatalf("update view wrong: %v", act)
	}
	items = mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"ACTIVITY"}}})
	if len(items) != 2 {
		t.Fatalf("correction must move, not duplicate: %d entries", len(items))
	}
	if items[0]["summary"] != "Watered 3 L" {
		t.Fatalf("moved entry summary not updated: %v", items[0]["summary"])
	}

	// --- void: absent from active timelines, visible as tombstone
	_, probs = runMut(t, m, "voidActivity", "VoidActivityInput",
		map[string]any{"id": actID, "expectedRevision": 2}, nil, actFields())
	mustNoProblems(t, probs, "voidActivity")

	items = mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"ACTIVITY"}}})
	if len(items) != 1 || items[0]["summary"] != "Fertilized 1.5 KG (10-10-10)" {
		t.Fatalf("voided entry must leave the active timeline: %v", items)
	}
	items = mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"ACTIVITY"}, "includeTombstoned": true}})
	if len(items) != 2 || items[0]["tombstonedAt"] == nil {
		t.Fatalf("tombstone not visible with includeTombstoned: %v", items)
	}
	ved, _ := items[0]["activity"].(map[string]any)["voidedAt"].(string)
	if ved == "" {
		t.Fatal("voided activity must expose voidedAt")
	}

	// --- voided activity refuses further edits and double-void
	_, probs = runMut(t, m, "voidActivity", "VoidActivityInput",
		map[string]any{"id": actID, "expectedRevision": 3}, nil, actFields())
	if firstCode(probs) != "CONFLICT" {
		t.Fatalf("double void must CONFLICT, got %v", probs)
	}
	_, probs = runMut(t, m, "updateActivity", "UpdateActivityInput",
		map[string]any{"id": actID, "expectedRevision": 3, "note": "oops"}, nil, actFields())
	if firstCode(probs) != "CONFLICT" {
		t.Fatalf("editing a voided activity must CONFLICT, got %v", probs)
	}
}

func TestActivityIdempotencyHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	plantID := mustPlantGQL(t, m)
	now := time.Now().UTC().Truncate(time.Second)
	input := map[string]any{"plantId": plantID, "kind": "WATER",
		"occurredAt": now.Format(time.RFC3339Nano), "quantity": "2", "unit": "L"}

	p1, probs := runMut(t, m, "recordActivity", "RecordActivityInput", input, strPtr("cm-1"), actFields())
	mustNoProblems(t, probs, "first record")
	id1 := p1["activity"].(map[string]any)["id"].(string)

	p2, probs := runMut(t, m, "recordActivity", "RecordActivityInput", input, strPtr("cm-1"), actFields())
	mustNoProblems(t, probs, "replay")
	if id2 := p2["activity"].(map[string]any)["id"].(string); id2 != id1 {
		t.Fatalf("replay returned a new activity: %s vs %s", id2, id1)
	}
	items := mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"ACTIVITY"}}})
	if len(items) != 1 {
		t.Fatalf("replay must not duplicate the projection row: %d entries", len(items))
	}

	changed := map[string]any{"plantId": plantID, "kind": "WATER",
		"occurredAt": now.Format(time.RFC3339Nano), "quantity": "5", "unit": "L"}
	_, probs = runMut(t, m, "recordActivity", "RecordActivityInput", changed, strPtr("cm-1"), actFields())
	if firstCode(probs) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("same cmid, different input must IDEMPOTENCY_CONFLICT, got %v", probs)
	}
}

func TestMeasurementLifecycleHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	plantID := mustPlantGQL(t, m)
	t0 := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)

	// --- record LENGTH/CM with uncertainty; projection summary
	payload, probs := runMut(t, m, "recordMeasurement", "RecordMeasurementInput", map[string]any{
		"plantId": plantID, "quantityKind": "LENGTH", "value": "12", "unit": "CM",
		"observedAt": t0.Format(time.RFC3339Nano), "method": "MANUAL", "uncertainty": "0.5",
	}, nil, measFields())
	mustNoProblems(t, probs, "recordMeasurement")
	mm := payload["measurement"].(map[string]any)
	mmID := mm["id"].(string)
	if mm["value"] != "12" || mm["uncertainty"] != "0.5" || mm["revision"] != float64(1) {
		t.Fatalf("measurement view wrong: %v", mm)
	}
	items := mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"MEASUREMENT"}}})
	if len(items) != 1 || items[0]["summary"] != "Length 12 CM" {
		t.Fatalf("projection wrong: %v", items)
	}

	// --- unit/quantityKind compatibility + sign rules
	for _, c := range []struct {
		name  string
		input map[string]any
	}{
		{"length-in-ml", map[string]any{"plantId": plantID, "quantityKind": "LENGTH",
			"value": "12", "unit": "ML", "observedAt": t0.Format(time.RFC3339Nano), "method": "MANUAL"}},
		{"negative-mass", map[string]any{"plantId": plantID, "quantityKind": "MASS",
			"value": "-5", "unit": "G", "observedAt": t0.Format(time.RFC3339Nano), "method": "MANUAL"}},
		{"negative-uncertainty", map[string]any{"plantId": plantID, "quantityKind": "MASS",
			"value": "5", "unit": "G", "observedAt": t0.Format(time.RFC3339Nano),
			"method": "MANUAL", "uncertainty": "-1"}},
		{"unknown-activity", map[string]any{"plantId": plantID, "quantityKind": "MASS",
			"value": "5", "unit": "G", "observedAt": t0.Format(time.RFC3339Nano),
			"method": "MANUAL", "activityId": uuid.NewString()}},
	} {
		_, probs := runMut(t, m, "recordMeasurement", "RecordMeasurementInput", c.input, nil, measFields())
		want := "NOT_FOUND"
		if c.name == "unknown-activity" {
			want = "NOT_FOUND"
		} else {
			want = "VALIDATION"
		}
		if code := firstCode(probs); code != want {
			t.Fatalf("%s: code %q want %q (%v)", c.name, code, want, probs)
		}
	}

	// --- a cold reading is real data: TEMPERATURE accepts negatives
	payload, probs = runMut(t, m, "recordMeasurement", "RecordMeasurementInput", map[string]any{
		"plantId": plantID, "quantityKind": "TEMPERATURE", "value": "-4", "unit": "C",
		"observedAt": t0.Format(time.RFC3339Nano), "method": "MANUAL",
	}, nil, measFields())
	mustNoProblems(t, probs, "cold reading")
	if v := payload["measurement"].(map[string]any)["value"]; v != "-4" {
		t.Fatalf("negative temperature mangled: %v", v)
	}

	// --- activityId link
	payload, probs = runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{
		"plantId": plantID, "kind": "WATER", "occurredAt": t0.Format(time.RFC3339Nano),
		"quantity": "1", "unit": "L",
	}, nil, actFields())
	mustNoProblems(t, probs, "link activity")
	aid := payload["activity"].(map[string]any)["id"].(string)
	payload, probs = runMut(t, m, "recordMeasurement", "RecordMeasurementInput", map[string]any{
		"plantId": plantID, "quantityKind": "VOLUME", "value": "250", "unit": "ML",
		"observedAt": t0.Format(time.RFC3339Nano), "method": "MANUAL", "activityId": aid,
	}, nil, measFields())
	mustNoProblems(t, probs, "linked measurement")
	if got := payload["measurement"].(map[string]any)["activityId"]; got != aid {
		t.Fatalf("activityId link lost: %v", got)
	}

	// --- correction: revision guard + audit keeps the original value
	_, probs = runMut(t, m, "updateMeasurement", "UpdateMeasurementInput",
		map[string]any{"id": mmID, "expectedRevision": 7, "value": "15"}, nil, measFields())
	if firstCode(probs) != "CONFLICT" {
		t.Fatalf("want CONFLICT, got %v", probs)
	}
	if cr, _ := probs[0]["currentRevision"].(float64); cr != 1 {
		t.Fatalf("CONFLICT must name currentRevision 1, got %v", probs[0]["currentRevision"])
	}
	payload, probs = runMut(t, m, "updateMeasurement", "UpdateMeasurementInput",
		map[string]any{"id": mmID, "expectedRevision": 1, "value": "15"}, nil, measFields())
	mustNoProblems(t, probs, "updateMeasurement")
	if payload["measurement"].(map[string]any)["revision"] != float64(2) {
		t.Fatalf("revision not bumped: %v", payload["measurement"])
	}
	items = mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"MEASUREMENT"}}})
	var found bool
	for _, it := range items {
		if it["summary"] == "Length 15 CM" {
			found = true
		}
	}
	if !found {
		t.Fatalf("corrected summary missing from projection: %v", items)
	}
	// corrections audit: the pre-image of the original measurement survives
	var prior []byte
	if err := m.pool.QueryRow(context.Background(),
		`SELECT prior_values FROM audit_revisions
		  WHERE aggregate_type='measurement' AND aggregate_id=$1 AND revision=2`, mmID,
	).Scan(&prior); err != nil {
		t.Fatalf("audit row missing: %v", err)
	}
	var pv map[string]any
	if err := json.Unmarshal(prior, &pv); err != nil {
		t.Fatalf("audit prior decode: %v", err)
	}
	if pv["value"] != "12" || pv["quantityKind"] != "LENGTH" {
		t.Fatalf("audit prior must retain the original measurement, got %v", pv)
	}

	// --- delete: entry leaves every timeline view (no tombstone shape in
	// the frozen Measurement contract); audit keeps the trail
	_, probs = runMut(t, m, "deleteMeasurement", "DeleteMeasurementInput",
		map[string]any{"id": mmID, "expectedRevision": 9}, nil,
		"deletedMeasurementId")
	if firstCode(probs) != "CONFLICT" {
		t.Fatalf("stale-revision delete must CONFLICT, got %v", probs)
	}
	payload, probs = runMut(t, m, "deleteMeasurement", "DeleteMeasurementInput",
		map[string]any{"id": mmID, "expectedRevision": 2}, nil, "deletedMeasurementId")
	mustNoProblems(t, probs, "deleteMeasurement")
	if payload["deletedMeasurementId"] != mmID {
		t.Fatalf("deletedMeasurementId = %v want %s", payload["deletedMeasurementId"], mmID)
	}
	items = mixedJournal(t, m, map[string]any{"plantId": plantID,
		"filter": map[string]any{"entryTypes": []string{"MEASUREMENT"}, "includeTombstoned": true}})
	for _, it := range items {
		if it["id"] == mmID {
			t.Fatalf("deleted measurement still in timeline: %v", items)
		}
	}
	_, probs = runMut(t, m, "deleteMeasurement", "DeleteMeasurementInput",
		map[string]any{"id": mmID, "expectedRevision": 2}, nil, "deletedMeasurementId")
	if firstCode(probs) != "NOT_FOUND" {
		t.Fatalf("double delete must NOT_FOUND, got %v", probs)
	}
}

func TestMixedTimelineHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	plantID := mustPlantGQL(t, m)
	base := time.Now().UTC().Add(-3 * 24 * time.Hour).Truncate(time.Second)

	// photo at base+2d, WATER at base, FERTILIZE at base+1d
	attachPhotoMut(t, m, plantID, readyPhoto(t, m), map[string]any{
		"viewpoint": "WHOLE", "dateSource": "USER",
		"takenAt": base.Add(48 * time.Hour).Format(time.RFC3339Nano),
	})
	if _, probs := runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{
		"plantId": plantID, "kind": "WATER", "occurredAt": base.Format(time.RFC3339Nano),
		"quantity": "2", "unit": "L",
	}, nil, actFields()); len(probs) > 0 {
		t.Fatalf("water: %v", probs)
	}
	if _, probs := runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{
		"plantId": plantID, "kind": "FERTILIZE",
		"occurredAt": base.Add(24 * time.Hour).Format(time.RFC3339Nano),
		"quantity":   "50", "unit": "G",
	}, nil, actFields()); len(probs) > 0 {
		t.Fatalf("fertilize: %v", probs)
	}

	// workspace-wide default order: photo (newest), fertilize, water
	items := mixedJournal(t, m, map[string]any{"first": 10})
	if len(items) != 3 {
		t.Fatalf("want 3 interleaved entries, got %d: %v", len(items), items)
	}
	wantTypes := []string{"PhotoEntry", "ActivityEntry", "ActivityEntry"}
	wantSums := []any{nil, "Fertilized 50 G", "Watered 2 L"}
	for i, it := range items {
		if it["__typename"] != wantTypes[i] {
			t.Fatalf("item %d typename %v want %v", i, it["__typename"], wantTypes[i])
		}
		if wantSums[i] != nil && it["summary"] != wantSums[i] {
			t.Fatalf("item %d summary %v want %v", i, it["summary"], wantSums[i])
		}
	}

	// entryTypes filter selects only activities
	items = mixedJournal(t, m, map[string]any{"first": 10,
		"filter": map[string]any{"entryTypes": []string{"ACTIVITY"}}})
	if len(items) != 2 {
		t.Fatalf("activity filter: %d items", len(items))
	}
	// keyset pagination across mixed types: page of 1, walk all three
	var seen []string
	var after *string
	for page := 0; page < 4; page++ {
		r := m.gql(journalMixedSel, map[string]any{"first": 1, "after": after})
		if len(r.Errors) > 0 {
			t.Fatalf("page %d: %v", page, r.Raw)
		}
		var out struct {
			Journal struct {
				Items    []map[string]any `json:"items"`
				PageInfo struct {
					HasNextPage bool    `json:"hasNextPage"`
					EndCursor   *string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"journal"`
		}
		if err := remarshal(r.Data, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Journal.Items) != 1 {
			t.Fatalf("page %d size %d", page, len(out.Journal.Items))
		}
		seen = append(seen, out.Journal.Items[0]["id"].(string))
		if !out.Journal.PageInfo.HasNextPage {
			break
		}
		after = out.Journal.PageInfo.EndCursor
	}
	if len(seen) != 3 || seen[0] == seen[1] || seen[1] == seen[2] {
		t.Fatalf("mixed-type pagination gaps/repeats: %v", seen)
	}
}

// Garden rounds and trusted agent logging support watering without pretending
// a measured volume exists. A partially supplied measurement is still invalid.
func TestUnmeasuredWateringHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	payload, probs := runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{"plantId": mustPlantGQL(t, m), "kind": "WATER", "occurredAt": time.Now().UTC().Format(time.RFC3339Nano)}, nil, actFields())
	mustNoProblems(t, probs, "unmeasured watering")
	activity := payload["activity"].(map[string]any)
	if activity["quantity"] != nil || activity["unit"] != nil {
		t.Fatal("unmeasured watering invented a volume", activity)
	}
}
