// T24 acceptance: the care GraphQL surface over HTTP. Covers the source/
// profile lifecycle (draft -> publish), plan materialization, agenda with
// typed reasons and missing facts, complete/skip/snooze/correct occurrence
// ops, the journal CARE union projection (including undo tombstoning),
// notification intents, plan preview, timezone-edit confirmation gating,
// and the collection hooks (archive cancels, restore regenerates, context
// edits refresh missing-fact state).
package graphql

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// ---- helpers ----------------------------------------------------------------

func careMut(t *testing.T, e *testEnv, name, inputType string, input map[string]any, fields string) (map[string]any, []map[string]any) {
	t.Helper()
	r := e.gql(fmt.Sprintf(`mutation($input: %s!) {
		%s(input: $input) { %s problems { code field message currentRevision } } }`,
		inputType, name, fields), map[string]any{"input": input})
	if len(r.Errors) > 0 {
		t.Fatalf("%s transport errors: %s", name, r.Raw)
	}
	payload := dataOf(t, r, name)
	var probs []map[string]any
	if ps, ok := payload["problems"].([]any); ok {
		for _, p := range ps {
			probs = append(probs, p.(map[string]any))
		}
	}
	return payload, probs
}

func noProbs(t *testing.T, op string, probs []map[string]any) {
	t.Helper()
	if len(probs) > 0 {
		t.Fatalf("%s problems: %+v", op, probs)
	}
}

const careOccSel = `id planId plantId ruleKey kind activityKind occurrenceKey
	originalDueDate currentDueDate snoozedUntil status completedActivityId
	resolvedAt planTimezone reason { text evidence { note } missingFacts }`

type careFixture struct {
	e         *testEnv
	sourceID  string
	versionID string // source version cited by rules
	profileID string
	planID    string
	plantID   string
	plantRev  int
}

// newCareFixture runs the happy-path setup: source -> draft profile
// (ACTION every-3-days + CHECK every-7-days requiring PLACEMENT) ->
// publish -> plant (no context yet) -> care plan. The plan materializes
// immediately, so agenda/notifications exist after setup.
func newCareFixture(t *testing.T) *careFixture {
	t.Helper()
	e := newEnv(t, DefaultLimits())
	f := &careFixture{e: e}

	payload, probs := careMut(t, e, "createSource", "CreateSourceInput", map[string]any{
		"kind": "BOOK", "title": "Fig Survival Guide", "publisher": "Orchard Press",
		"citation":    "Orchard Press, Fig Survival Guide, 2023",
		"summary":     "Regional fig care guidance.",
		"retrievedOn": "2026-01-15",
	}, "source { id revision currentVersion { id citation } }")
	noProbs(t, "createSource", probs)
	src := payload["source"].(map[string]any)
	f.sourceID = mustID(t, src, "id")
	f.versionID = mustID(t, src["currentVersion"].(map[string]any), "id")

	// Weekly-every-day rules so occurrences exist on the plan start day
	// itself (PLAN_START-anchored EVERY_N_DAYS starts at start+days).
	everyday := []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY",
		"FRIDAY", "SATURDAY", "SUNDAY"}
	rules := func(guidance string) []any {
		return []any{
			map[string]any{"ruleKey": "water", "kind": "ACTION", "activityKind": "WATER",
				"recurrence":       map[string]any{"weeklyDays": map[string]any{"weekdays": everyday}},
				"guidanceText":     guidance,
				"sourceVersionIds": []string{f.versionID}},
			map[string]any{"ruleKey": "inspect", "kind": "CHECK",
				"recurrence":       map[string]any{"weeklyDays": map[string]any{"weekdays": everyday}},
				"requiredFacts":    []string{"PLACEMENT"},
				"guidanceText":     "Check leaf undersides for pests.",
				"sourceVersionIds": []string{f.versionID}},
		}
	}

	payload, probs = careMut(t, e, "createCareProfileDraft", "CreateCareProfileDraftInput", map[string]any{
		"category": "FIG", "species": "Ficus carica", "cultivar": "Brown Turkey",
		"rules": rules("Deep water when the top inch is dry."),
	}, "profile { id revision status currentVersion { id } }")
	noProbs(t, "createCareProfileDraft", probs)
	prof := payload["profile"].(map[string]any)
	f.profileID = mustID(t, prof, "id")
	rev := int(prof["revision"].(float64))
	if prof["currentVersion"] != nil {
		t.Fatalf("DRAFT profile must not expose currentVersion, got %v", prof["currentVersion"])
	}

	// Draft versions are visible on the single-profile read.
	var draftVerID string
	r := e.gql(`query($id: ID!) { careProfile(id: $id) {
		versions { id status rules { ruleKey kind guidanceText } } } }`, map[string]any{"id": f.profileID})
	versions := dataOf(t, r, "careProfile")["versions"].([]any)
	if len(versions) != 1 {
		t.Fatalf("draft versions: %v", versions)
	}
	draftVerID = mustID(t, versions[0].(map[string]any), "id")

	// Draft edit: guidance text changes, revision bumps.
	payload, probs = careMut(t, e, "updateCareProfileDraft", "UpdateCareProfileDraftInput", map[string]any{
		"id": f.profileID, "expectedRevision": rev,
		"rules": rules("Deep water when the top 2 inches are dry."),
	}, "profile { id revision versions { id status rules { ruleKey guidanceText } } }")
	noProbs(t, "updateCareProfileDraft", probs)
	prof = payload["profile"].(map[string]any)
	rev = int(prof["revision"].(float64))
	if rev != 2 {
		t.Fatalf("draft edit revision = %d, want 2", rev)
	}

	// Publish: the draft version becomes REVIEWED and currentVersion appears.
	payload, probs = careMut(t, e, "publishCareProfile", "PublishCareProfileInput", map[string]any{
		"profileId": f.profileID, "expectedRevision": rev,
		"reviewerLabel": "owner", "reviewedOn": "2026-11-02",
	}, "profile { id revision status currentVersion { id version status reviewedOn } }")
	noProbs(t, "publishCareProfile", probs)
	prof = payload["profile"].(map[string]any)
	if prof["status"] != "REVIEWED" {
		t.Fatalf("profile status = %v, want REVIEWED", prof["status"])
	}
	cv := prof["currentVersion"].(map[string]any)
	if cv["status"] != "REVIEWED" || cv["reviewedOn"] != "2026-11-02" {
		t.Fatalf("published version: %v", cv)
	}
	publishedVerID := mustID(t, cv, "id")
	if publishedVerID != draftVerID {
		t.Fatalf("publish must review the draft version in place: %v vs %v", publishedVerID, draftVerID)
	}

	// Plant without context: the PLACEMENT fact is missing until set.
	r = e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id revision } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"name": "Brown Turkey fig", "type": "INDIVIDUAL", "category": "FIG",
		}})
	plant := dataOf(t, r, "createPlant")["plant"].(map[string]any)
	f.plantID = mustID(t, plant, "id")
	f.plantRev = int(plant["revision"].(float64))

	payload, probs = careMut(t, e, "createCarePlan", "CreateCarePlanInput", map[string]any{
		"plantId": f.plantID, "profileVersionId": publishedVerID,
		"timezone": "America/New_York",
	}, `plan { id plantId timezone enabled revision profileVersion { id version } }`)
	noProbs(t, "createCarePlan", probs)
	plan := payload["plan"].(map[string]any)
	f.planID = mustID(t, plan, "id")
	if plan["plantId"] != f.plantID || plan["timezone"] != "America/New_York" {
		t.Fatalf("plan: %v", plan)
	}
	return f
}

func (f *careFixture) agenda(t *testing.T, vars map[string]any) []map[string]any {
	t.Helper()
	r := f.e.gql(`query($from: LocalDate, $to: LocalDate, $plantId: ID, $first: Int, $after: String) {
		careAgenda(from: $from, to: $to, plantId: $plantId, first: $first, after: $after) {
			items { `+careOccSel+` } pageInfo { hasNextPage endCursor } } }`, vars)
	if len(r.Errors) > 0 {
		t.Fatalf("careAgenda errors: %s", r.Raw)
	}
	conn := dataOf(t, r, "careAgenda")
	var items []map[string]any
	for _, it := range conn["items"].([]any) {
		items = append(items, it.(map[string]any))
	}
	return items
}

func agendaWindow() map[string]any {
	now := time.Now().UTC()
	return map[string]any{
		"from":  now.AddDate(0, 0, -7).Format("2006-01-02"),
		"to":    now.AddDate(0, 0, 60).Format("2006-01-02"),
		"first": 100,
	}
}

func firstOpen(items []map[string]any, ruleKey string) map[string]any {
	for _, it := range items {
		if it["ruleKey"] == ruleKey && it["status"] == "DUE" {
			return it
		}
	}
	return nil
}

// ---- tests -------------------------------------------------------------------

func TestCareSourceProfilePlanReads(t *testing.T) {
	f := newCareFixture(t)
	e := f.e

	// sources query shows the cited source with its version.
	r := e.gql(`query { sources { items { id kind title retiredAt
		versions { id citation } } pageInfo { hasNextPage } } }`, nil)
	items := dataOf(t, r, "sources")["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != f.sourceID {
		t.Fatalf("sources: %v", items)
	}

	// careProfiles query: published only by default.
	r = e.gql(`query($f: CareProfileFilter) { careProfiles(filter: $f) {
		items { id category status currentVersion { id version } } } }`,
		map[string]any{"f": map[string]any{"category": "FIG"}})
	items = dataOf(t, r, "careProfiles")["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("careProfiles: %v", items)
	}
	p := items[0].(map[string]any)
	if p["id"] != f.profileID || p["status"] != "REVIEWED" || p["currentVersion"] == nil {
		t.Fatalf("published profile: %v", p)
	}

	// plant(id) carries the care plan (single-read attachment).
	r = e.gql(`query($id: ID!) { plant(id: $id) { id carePlan {
		id timezone revision overrides { ruleKey enabled } } } }`, map[string]any{"id": f.plantID})
	pl := dataOf(t, r, "plant")
	cp := pl["carePlan"].(map[string]any)
	if cp["id"] != f.planID || cp["timezone"] != "America/New_York" {
		t.Fatalf("plant.carePlan: %v", cp)
	}

	// retireSource stamps retiredAt (history stays citable).
	payload, probs := careMut(t, e, "retireSource", "RetireSourceInput", map[string]any{
		"id": f.sourceID, "expectedRevision": 1,
	}, "source { id retiredAt }")
	noProbs(t, "retireSource", probs)
	if payload["source"].(map[string]any)["retiredAt"] == nil {
		t.Fatalf("retiredAt must be set: %v", payload["source"])
	}

	// A throwaway draft can be deleted.
	payload, probs = careMut(t, e, "createCareProfileDraft", "CreateCareProfileDraftInput", map[string]any{
		"category": "OTHER",
		"rules": []any{map[string]any{"ruleKey": "note", "kind": "CHECK",
			"recurrence":   map[string]any{"oneTime": map[string]any{"date": "2026-12-01"}},
			"guidanceText": "One-off note."}},
	}, "profile { id revision }")
	noProbs(t, "createCareProfileDraft", probs)
	dropID := mustID(t, payload["profile"].(map[string]any), "id")
	payload, probs = careMut(t, e, "deleteCareProfileDraft", "DeleteCareProfileDraftInput", map[string]any{
		"id": dropID, "expectedRevision": 1,
	}, "deletedProfileId")
	noProbs(t, "deleteCareProfileDraft", probs)
	if payload["deletedProfileId"] != dropID {
		t.Fatalf("deletedProfileId: %v", payload["deletedProfileId"])
	}
}

func TestCareAgendaTypedReasonsAndPagination(t *testing.T) {
	f := newCareFixture(t)

	items := f.agenda(t, agendaWindow())
	var waters, inspects int
	for _, it := range items {
		if it["planId"] != f.planID || it["plantId"] != f.plantID {
			t.Fatalf("agenda item wrong plan/plant: %v", it)
		}
		if it["planTimezone"] != "America/New_York" {
			t.Fatalf("planTimezone: %v", it)
		}
		switch it["ruleKey"] {
		case "water":
			waters++
			if it["kind"] != "ACTION" || it["activityKind"] != "WATER" {
				t.Fatalf("water occurrence: %v", it)
			}
			reason := it["reason"].(map[string]any)
			if reason == nil || reason["text"] == "" {
				t.Fatalf("typed reason missing: %v", it)
			}
		case "inspect":
			inspects++
			reason := it["reason"].(map[string]any)
			if reason == nil {
				t.Fatalf("inspect reason missing: %v", it)
			}
			facts := reason["missingFacts"].([]any)
			if len(facts) != 1 || facts[0] != "PLACEMENT" {
				t.Fatalf("inspect missingFacts = %v, want [PLACEMENT]", facts)
			}
		}
	}
	if waters < 15 || inspects < 7 {
		t.Fatalf("60-day horizon: waters=%d inspects=%d", waters, inspects)
	}

	// Cursor pagination: first=1 pages through without repeats.
	p1 := f.agenda(t, map[string]any{"from": agendaWindow()["from"], "to": agendaWindow()["to"], "first": 1})
	if len(p1) != 1 {
		t.Fatalf("first=1 -> %d items", len(p1))
	}
	r := f.e.gql(`query($from: LocalDate, $to: LocalDate, $first: Int, $after: String) {
		careAgenda(from: $from, to: $to, first: $first, after: $after) {
			items { id } pageInfo { hasNextPage endCursor } } }`,
		map[string]any{"from": agendaWindow()["from"], "to": agendaWindow()["to"], "first": 1})
	pi := dataOf(t, r, "careAgenda")["pageInfo"].(map[string]any)
	if !pi["hasNextPage"].(bool) || pi["endCursor"] == nil {
		t.Fatalf("pageInfo: %v", pi)
	}
	p2 := f.agenda(t, map[string]any{"from": agendaWindow()["from"], "to": agendaWindow()["to"],
		"first": 1, "after": pi["endCursor"].(string)})
	if len(p2) != 1 || p2[0]["id"] == p1[0]["id"] {
		t.Fatalf("page 2 repeats page 1: %v vs %v", p2, p1)
	}
}

func TestCareOccurrenceOpsAndJournalUnion(t *testing.T) {
	f := newCareFixture(t)
	e := f.e

	items := f.agenda(t, agendaWindow())

	// Complete an ACTION occurrence: activity + next occurrence in one payload.
	water := firstOpen(items, "water")
	if water == nil {
		t.Fatal("no open water occurrence")
	}
	payload, probs := careMut(t, e, "completeOccurrence", "CompleteOccurrenceInput", map[string]any{
		"occurrenceId": mustID(t, water, "id"), "expectedRevision": 1,
		"note": "deep soak", "quantity": "2.5", "unit": "L",
	}, `occurrence { `+careOccSel+` } activity { id kind note occurredAt voidedAt } nextOccurrence { id currentDueDate status }`)
	noProbs(t, "completeOccurrence", probs)
	occ := payload["occurrence"].(map[string]any)
	if occ["status"] != "COMPLETED" || occ["resolvedAt"] == nil || occ["completedActivityId"] == nil {
		t.Fatalf("completed occurrence: %v", occ)
	}
	act := payload["activity"].(map[string]any)
	if act["kind"] != "WATER" || act["note"] != "deep soak" || act["voidedAt"] != nil {
		t.Fatalf("linked activity: %v", act)
	}
	next := payload["nextOccurrence"].(map[string]any)
	if next["currentDueDate"].(string) <= water["currentDueDate"].(string) {
		t.Fatalf("next occurrence not later: %v vs %v", next["currentDueDate"], water["currentDueDate"])
	}

	// Completing an ACTION projects an ActivityEntry (via the activity),
	// not a CareEntry.
	r := e.gql(`query($f: JournalFilter) { journal(first: 10, filter: $f) { items {
		__typename ... on ActivityEntry { id summary } ... on CareEntry { id occurrenceId } } } }`,
		map[string]any{"f": map[string]any{"entryTypes": []string{"ACTIVITY", "CARE"}}})
	jitems := dataOf(t, r, "journal")["items"].([]any)
	var actEntries, careEntries int
	for _, it := range jitems {
		switch it.(map[string]any)["__typename"] {
		case "ActivityEntry":
			actEntries++
		case "CareEntry":
			careEntries++
		}
	}
	if actEntries != 1 || careEntries != 0 {
		t.Fatalf("after ACTION completion: activities=%d cares=%d (%v)", actEntries, careEntries, jitems)
	}

	// Completing a CHECK projects a CareEntry directly and creates no activity.
	insp := firstOpen(items, "inspect")
	if insp == nil {
		t.Fatal("no open inspect occurrence")
	}
	inspID := mustID(t, insp, "id")
	payload, probs = careMut(t, e, "completeOccurrence", "CompleteOccurrenceInput", map[string]any{
		"occurrenceId": inspID, "expectedRevision": 1,
	}, `occurrence { id status } activity { id kind voidedAt } nextOccurrence { id }`)
	noProbs(t, "completeOccurrence", probs)
	act = payload["activity"].(map[string]any)
	if act == nil || act["kind"] != "OBSERVE" || act["voidedAt"] != nil {
		t.Fatalf("CHECK completion must link an OBSERVE activity: %v", payload["activity"])
	}
	r = e.gql(`query($f: JournalFilter) { journal(first: 10, filter: $f) { items {
		__typename ... on CareEntry { id plantId occurrenceId kind activityKind summary tombstonedAt } } } }`,
		map[string]any{"f": map[string]any{"entryTypes": []string{"CARE"}}})
	jitems = dataOf(t, r, "journal")["items"].([]any)
	if len(jitems) != 1 {
		t.Fatalf("CARE journal entries: %v", jitems)
	}
	ce := jitems[0].(map[string]any)
	if ce["__typename"] != "CareEntry" || ce["occurrenceId"] != inspID || ce["kind"] != "CHECK" ||
		ce["activityKind"] != nil || ce["plantId"] != f.plantID || ce["tombstonedAt"] != nil {
		t.Fatalf("CareEntry: %v", ce)
	}
	// The OBSERVE link stays behind the completion: no ActivityEntry for it.
	r = e.gql(`query($f: JournalFilter) { journal(first: 10, filter: $f) { items { __typename } } }`,
		map[string]any{"f": map[string]any{"entryTypes": []string{"ACTIVITY"}}})
	if n := len(dataOf(t, r, "journal")["items"].([]any)); n != 1 {
		t.Fatalf("check completion leaked an ActivityEntry: %v", n)
	}

	// Undo the CHECK completion: occurrence reopens, CareEntry tombstones.
	payload, probs = careMut(t, e, "correctOccurrenceCompletion", "CorrectOccurrenceCompletionInput", map[string]any{
		"occurrenceId": inspID, "expectedRevision": 2, "undo": true,
	}, `occurrence { id status } activity { id } retracted nextOccurrence { id status }`)
	noProbs(t, "correctOccurrenceCompletion", probs)
	if payload["occurrence"].(map[string]any)["status"] != "DUE" {
		t.Fatalf("undo must reopen: %v", payload["occurrence"])
	}
	if payload["retracted"] != false {
		t.Fatalf("retracted = %v, want false (no later resolutions)", payload["retracted"])
	}
	r = e.gql(`query($f: JournalFilter) { journal(first: 10, filter: $f) { items {
		__typename ... on CareEntry { id tombstonedAt } } } }`,
		map[string]any{"f": map[string]any{"entryTypes": []string{"CARE"}, "includeTombstoned": true}})
	jitems = dataOf(t, r, "journal")["items"].([]any)
	if len(jitems) != 1 || jitems[0].(map[string]any)["tombstonedAt"] == nil {
		t.Fatalf("undone CareEntry must tombstone: %v", jitems)
	}
	// Default journal reads hide the tombstone again.
	r = e.gql(`query($f: JournalFilter) { journal(first: 10, filter: $f) { items { __typename } } }`,
		map[string]any{"f": map[string]any{"entryTypes": []string{"CARE"}}})
	if n := len(dataOf(t, r, "journal")["items"].([]any)); n != 0 {
		t.Fatalf("tombstoned CareEntry visible without includeTombstoned: %v", n)
	}

	// Skip advances the schedule; the skipped row leaves the open agenda.
	water2 := firstOpen(f.agenda(t, agendaWindow()), "water")
	if water2 == nil {
		t.Fatal("no open water occurrence after completion")
	}
	payload, probs = careMut(t, e, "skipOccurrence", "SkipOccurrenceInput", map[string]any{
		"occurrenceId": mustID(t, water2, "id"), "expectedRevision": 1,
	}, `occurrence { id status } nextOccurrence { id currentDueDate status }`)
	noProbs(t, "skipOccurrence", probs)
	if payload["occurrence"].(map[string]any)["status"] != "SKIPPED" {
		t.Fatalf("skip: %v", payload["occurrence"])
	}

	// Snooze moves currentDueDate without changing the logical key.
	water3 := firstOpen(f.agenda(t, agendaWindow()), "water")
	if water3 == nil {
		t.Fatal("no open water occurrence after skip")
	}
	until := time.Now().UTC().AddDate(0, 0, 9).Format("2006-01-02")
	payload, probs = careMut(t, e, "snoozeOccurrence", "SnoozeOccurrenceInput", map[string]any{
		"occurrenceId": mustID(t, water3, "id"), "expectedRevision": 1, "until": until,
	}, `occurrence { id occurrenceKey currentDueDate snoozedUntil } nextOccurrence { id }`)
	noProbs(t, "snoozeOccurrence", probs)
	sno := payload["occurrence"].(map[string]any)
	if sno["occurrenceKey"] != water3["occurrenceKey"] {
		t.Fatalf("snooze changed the logical key: %v -> %v", water3["occurrenceKey"], sno["occurrenceKey"])
	}
	if sno["currentDueDate"] != until || sno["snoozedUntil"] != until {
		t.Fatalf("snooze fields: %v", sno)
	}

	// Resolving the same occurrence twice conflicts with a revision hint.
	_, probs = careMut(t, e, "skipOccurrence", "SkipOccurrenceInput", map[string]any{
		"occurrenceId": mustID(t, water2, "id"), "expectedRevision": 1,
	}, "occurrence { id }")
	wantProblemCode(t, probs, "CONFLICT")

	// Undo the ACTION completion: later occurrences of the rule were
	// already generated, so the row is retracted (newer decisions stay
	// authoritative) and the activity's timeline entry tombstones.
	waterID := mustID(t, water, "id")
	payload, probs = careMut(t, e, "correctOccurrenceCompletion", "CorrectOccurrenceCompletionInput", map[string]any{
		"occurrenceId": waterID, "expectedRevision": 2, "undo": true,
	}, `occurrence { id status } activity { id voidedAt } retracted nextOccurrence { id }`)
	noProbs(t, "correctOccurrenceCompletion (ACTION undo)", probs)
	if payload["retracted"] != true {
		t.Fatalf("retracted = %v, want true (later decisions exist)", payload["retracted"])
	}
	if s := payload["occurrence"].(map[string]any)["status"]; s != "COMPLETION_RETRACTED" {
		t.Fatalf("ACTION undo status = %v, want COMPLETION_RETRACTED", s)
	}
	// Undo clears the completion link (03: "voids the linked activity,
	// appends audit and clears completion link"); the voided event stays
	// attributable through its tombstoned timeline entry below.
	if payload["activity"] != nil {
		t.Fatalf("undo must clear the completion link: %v", payload["activity"])
	}
	r = e.gql(`query($f: JournalFilter) { journal(first: 10, filter: $f) { items {
		__typename ... on ActivityEntry { id summary tombstonedAt } } } }`,
		map[string]any{"f": map[string]any{"entryTypes": []string{"ACTIVITY"}, "includeTombstoned": true}})
	jitems = dataOf(t, r, "journal")["items"].([]any)
	if len(jitems) != 1 || jitems[0].(map[string]any)["tombstonedAt"] == nil {
		t.Fatalf("undone ActivityEntry must tombstone: %v", jitems)
	}
}

func TestCareNotifications(t *testing.T) {
	f := newCareFixture(t)
	e := f.e

	// Both rules anchor at PLAN_START, so two occurrences are due today
	// and each raised one INAPP intent.
	r := e.gql(`query { notifications { items { id channel dedupKey refKind refId
		title body readAt } pageInfo { hasNextPage endCursor } } }`, nil)
	items := dataOf(t, r, "notifications")["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("notifications: %v", items)
	}
	seen := map[string]bool{}
	for _, it := range items {
		n := it.(map[string]any)
		if n["channel"] != "INAPP" || n["refKind"] != "OCCURRENCE" || n["readAt"] != nil {
			t.Fatalf("notification: %v", n)
		}
		if n["title"] == "" || n["body"] == "" {
			t.Fatalf("notification content: %v", n)
		}
		seen[n["refId"].(string)] = true
	}
	if len(seen) != 2 {
		t.Fatalf("one intent per occurrence: %v", items)
	}

	// Mark read: idempotent, stamps readAt.
	n0 := items[0].(map[string]any)
	payload, probs := careMut(t, e, "markNotificationRead", "MarkNotificationReadInput", map[string]any{
		"notificationId": mustID(t, n0, "id"),
	}, "notification { id readAt }")
	noProbs(t, "markNotificationRead", probs)
	if payload["notification"].(map[string]any)["readAt"] == nil {
		t.Fatalf("readAt not stamped: %v", payload["notification"])
	}
	payload, probs = careMut(t, e, "markNotificationRead", "MarkNotificationReadInput", map[string]any{
		"notificationId": mustID(t, n0, "id"),
	}, "notification { id readAt }")
	noProbs(t, "markNotificationRead (repeat)", probs)

	// unreadOnly filter drops the read one.
	r = e.gql(`query($u: Boolean) { notifications(unreadOnly: $u) { items { id } } }`,
		map[string]any{"u": true})
	if n := len(dataOf(t, r, "notifications")["items"].([]any)); n != 1 {
		t.Fatalf("unreadOnly count = %d", n)
	}
}

func TestCarePreviewAndPlanEdits(t *testing.T) {
	f := newCareFixture(t)
	e := f.e

	// Preview is a query and changes nothing.
	r := e.gql(`query($input: PreviewCarePlanInput!) { previewCarePlan(input: $input) {
		preview { occurrences { ruleKey kind dueDate reason { text missingFacts } }
			missingFacts wouldCancel { id } }
		problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"plantId": f.plantID, "timezone": "America/Chicago",
		}})
	if len(r.Errors) > 0 {
		t.Fatalf("previewCarePlan errors: %s", r.Raw)
	}
	pl := dataOf(t, r, "previewCarePlan")
	if len(pl["problems"].([]any)) > 0 {
		t.Fatalf("preview problems: %v", pl["problems"])
	}
	pv := pl["preview"].(map[string]any)
	if len(pv["occurrences"].([]any)) == 0 {
		t.Fatalf("preview occurrences empty")
	}
	mf := pv["missingFacts"].([]any)
	if len(mf) != 1 || mf[0] != "PLACEMENT" {
		t.Fatalf("preview missingFacts = %v", mf)
	}
	if len(pv["wouldCancel"].([]any)) == 0 {
		t.Fatalf("timezone-change preview must list would-cancel rows: %v", pv["wouldCancel"])
	}

	// Timezone change without confirmation is rejected.
	_, probs := careMut(t, e, "updateCarePlan", "UpdateCarePlanInput", map[string]any{
		"id": f.planID, "expectedRevision": 1, "timezone": "America/Chicago",
	}, "plan { id }")
	wantProblemCode(t, probs, "VALIDATION")

	// With confirmation the plan re-anchors and regenerates.
	payload, probs := careMut(t, e, "updateCarePlan", "UpdateCarePlanInput", map[string]any{
		"id": f.planID, "expectedRevision": 1, "timezone": "America/Chicago",
		"confirmRecalculation": true,
	}, "plan { id timezone revision }")
	noProbs(t, "updateCarePlan", probs)
	plan := payload["plan"].(map[string]any)
	if plan["timezone"] != "America/Chicago" || int(plan["revision"].(float64)) != 2 {
		t.Fatalf("updated plan: %v", plan)
	}
	items := f.agenda(t, agendaWindow())
	if len(items) == 0 {
		t.Fatal("agenda empty after timezone recalculation")
	}
	for _, it := range items {
		if it["planTimezone"] != "America/Chicago" {
			t.Fatalf("regenerated occurrence kept old tz: %v", it)
		}
	}

	// Invalid override recurrence is rejected with a field-scoped problem.
	_, probs = careMut(t, e, "updateCarePlan", "UpdateCarePlanInput", map[string]any{
		"id": f.planID, "expectedRevision": 2,
		"overrides": []any{map[string]any{"ruleKey": "water", "enabled": true,
			"recurrenceOverride": map[string]any{"everyNDays": map[string]any{"days": 0, "anchor": "PLAN_START"}}}},
	}, "plan { id }")
	wantProblemCode(t, probs, "VALIDATION")

	// Valid override: water every 5 days replaces the profile cadence.
	payload, probs = careMut(t, e, "updateCarePlan", "UpdateCarePlanInput", map[string]any{
		"id": f.planID, "expectedRevision": 2,
		"overrides": []any{map[string]any{"ruleKey": "water", "enabled": true,
			"recurrenceOverride": map[string]any{"everyNDays": map[string]any{"days": 5, "anchor": "PLAN_START"}}}},
	}, "plan { id revision overrides { ruleKey enabled recurrenceOverride { ... on EveryNDaysRecurrence { days anchor } } } }")
	noProbs(t, "updateCarePlan override", probs)
	plan = payload["plan"].(map[string]any)
	ovs := plan["overrides"].([]any)
	if len(ovs) != 1 {
		t.Fatalf("overrides: %v", ovs)
	}
	ov := ovs[0].(map[string]any)
	if ov["ruleKey"] != "water" || ov["recurrenceOverride"].(map[string]any)["days"] != float64(5) {
		t.Fatalf("override round-trip: %v", ov)
	}
}

func TestCareCollectionHooks(t *testing.T) {
	f := newCareFixture(t)
	e := f.e
	ctx := context.Background()

	// Archive cancels open future work; the agenda empties.
	payload, probs := careMut(t, e, "archivePlant", "ArchivePlantInput", map[string]any{
		"id": f.plantID, "expectedRevision": f.plantRev,
	}, "plant { id status revision }")
	noProbs(t, "archivePlant", probs)
	plant := payload["plant"].(map[string]any)
	if plant["status"] != "ARCHIVED" {
		t.Fatalf("archive: %v", plant)
	}
	f.plantRev = int(plant["revision"].(float64))
	if items := f.agenda(t, agendaWindow()); len(items) != 0 {
		t.Fatalf("agenda not emptied by archive: %d items", len(items))
	}
	var cancelled int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM care_occurrences WHERE plan_id=$1 AND status='CANCELLED'`, f.planID,
	).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled == 0 {
		t.Fatal("archive must cancel open occurrences")
	}

	// Restore regenerates open work.
	payload, probs = careMut(t, e, "restorePlant", "RestorePlantInput", map[string]any{
		"id": f.plantID, "expectedRevision": f.plantRev,
	}, "plant { id status revision }")
	noProbs(t, "restorePlant", probs)
	f.plantRev = int(payload["plant"].(map[string]any)["revision"].(float64))
	if items := f.agenda(t, agendaWindow()); len(items) == 0 {
		t.Fatal("restore must regenerate open occurrences")
	}

	// Context edit clears the PLACEMENT missing fact on regeneration.
	items := f.agenda(t, agendaWindow())
	insp := firstOpen(items, "inspect")
	if insp == nil {
		t.Fatal("no inspect occurrence after restore")
	}
	if facts := insp["reason"].(map[string]any)["missingFacts"].([]any); len(facts) != 1 {
		t.Fatalf("pre-context missingFacts: %v", facts)
	}
	payload, probs = careMut(t, e, "setPlantContext", "SetPlantContextInput", map[string]any{
		"plantId": f.plantID, "expectedRevision": f.plantRev,
		"context": map[string]any{"placement": "CONTAINER", "potVolumeLiters": "25"},
	}, "plant { id revision }")
	noProbs(t, "setPlantContext", probs)

	for _, it := range f.agenda(t, agendaWindow()) {
		if it["ruleKey"] != "inspect" {
			continue
		}
		if facts := it["reason"].(map[string]any)["missingFacts"]; len(facts.([]any)) != 0 {
			t.Fatalf("PLACEMENT still missing after context edit: %v", it)
		}
	}
}

func TestCareCrossWorkspaceIsolation(t *testing.T) {
	f := newCareFixture(t)
	e := f.e
	ctx := context.Background()

	// A plant in someone else's workspace is NOT_FOUND (no enumeration),
	// even for the same owner identity's second workspace.
	other, err := e.q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: platform.UUIDv4{}.New(), OwnerID: e.ownerID,
		DisplayName: "other", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := e.q.InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: other.ID,
		Name: "not mine", Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryFIG,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, probs := careMut(t, e, "createCarePlan", "CreateCarePlanInput", map[string]any{
		"plantId": uuidStrPG(theirs.ID), "timezone": "UTC",
	}, "plan { id }")
	wantProblemCode(t, probs, "NOT_FOUND")

	// Their agenda is invisible: our agenda only carries our plan's rows.
	for _, it := range f.agenda(t, agendaWindow()) {
		if it["planId"] != f.planID {
			t.Fatalf("foreign occurrence leaked: %v", it)
		}
	}
}
