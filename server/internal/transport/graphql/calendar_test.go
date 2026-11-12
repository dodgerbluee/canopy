package graphql

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

const calendarQuery = `query CalendarEvents($from:LocalDate!, $to:LocalDate!, $first:Int!, $after:String){calendarEvents(from:$from,to:$to,first:$first,after:$after){items{id entityId plantId day kind activityKind title amountMm note revision} pageInfo{hasNextPage endCursor}}}`

func TestCalendarRainLifecycleHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	if _, err := m.pool.Exec(ctx, `UPDATE workspace SET timezone='America/Chicago' WHERE id=$1`, m.wsID); err != nil {
		t.Fatal(err)
	}
	create := func(input map[string]any, key string) (map[string]any, []map[string]any) {
		return runMut(t, m, "recordRain", "RecordRainInput", input, &key, `rain{id day amountMm note revision}`)
	}
	payload, ps := create(map[string]any{"day": "2026-11-01"}, "rain1")
	mustNoProblems(t, ps, "create rain")
	rain := payload["rain"].(map[string]any)
	id := rain["id"].(string)
	if rain["amountMm"] != nil || rain["note"] != nil {
		t.Fatal("unknown rain facts must stay unknown", rain)
	}
	replay, ps := create(map[string]any{"day": "2026-11-01"}, "rain1")
	mustNoProblems(t, ps, "replay")
	if replay["rain"].(map[string]any)["id"] != id {
		t.Fatal("duplicate rain")
	}
	_, ps = create(map[string]any{"day": "2026-11-02"}, "rain1")
	wantProblemCode(t, ps, "IDEMPOTENCY_CONFLICT")
	_, ps = create(map[string]any{"day": "2026-11-02", "amountMm": "-1"}, "negative")
	wantProblemCode(t, ps, "VALIDATION")
	_, ps = create(map[string]any{"day": "2026-11-02", "amountMm": "10001"}, "huge")
	wantProblemCode(t, ps, "VALIDATION")
	_, ps = create(map[string]any{"day": "2026-11-02", "amountMm": "25.4", "note": "Steady shower"}, "rain2")
	mustNoProblems(t, ps, "known amount")
	var activities int
	if err := m.pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE workspace_id=$1`, m.wsID).Scan(&activities); err != nil || activities != 0 {
		t.Fatal("rain must not create water activities", err, activities)
	}
	vars := map[string]any{"from": "2026-11-01", "to": "2026-11-30", "first": 1}
	page := dataOf(t, m.gql(calendarQuery, vars), "calendarEvents")
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["day"] != "2026-11-01" {
		t.Fatal(page)
	}
	info := page["pageInfo"].(map[string]any)
	if info["hasNextPage"] != true {
		t.Fatal(info)
	}
	vars["after"] = info["endCursor"]
	second := dataOf(t, m.gql(calendarQuery, vars), "calendarEvents")
	if len(second["items"].([]any)) != 1 || second["items"].([]any)[0].(map[string]any)["amountMm"] != "25.4" {
		t.Fatal(second)
	}
	vars["to"] = "2026-11-29"
	if len(m.gql(calendarQuery, vars).Errors) == 0 {
		t.Fatal("cursor must bind range")
	}
	vars["to"] = "2026-11-30"
	vars["after"] = nil
	// Foreign workspace rain stays invisible and cannot be removed.
	foreign := uuid.NewString()
	if _, err := m.pool.Exec(ctx, `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Other','UTC','USD')`, foreign, m.ownerID); err != nil {
		t.Fatal(err)
	}
	foreignRain := uuid.NewString()
	if _, err := m.pool.Exec(ctx, `INSERT INTO rain_events(id,workspace_id,event_date) VALUES($1,$2,'2026-11-01')`, foreignRain, foreign); err != nil {
		t.Fatal(err)
	}
	key := "foreign"
	_, ps = runMut(t, m, "voidRain", "VoidRainInput", map[string]any{"id": foreignRain, "expectedRevision": 1}, &key, `removedId`)
	wantProblemCode(t, ps, "NOT_FOUND")
	vars["first"] = 100
	page = dataOf(t, m.gql(calendarQuery, vars), "calendarEvents")
	if len(page["items"].([]any)) != 2 {
		t.Fatal("foreign record leaked", page)
	}
	key = "wrong-revision"
	_, ps = runMut(t, m, "voidRain", "VoidRainInput", map[string]any{"id": id, "expectedRevision": 2}, &key, `removedId`)
	wantProblemCode(t, ps, "CONFLICT")
	key = "remove"
	for range 2 {
		out, problems := runMut(t, m, "voidRain", "VoidRainInput", map[string]any{"id": id, "expectedRevision": 1}, &key, `removedId`)
		mustNoProblems(t, problems, "remove/replay")
		if out["removedId"] != id {
			t.Fatal(out)
		}
	}
	page = dataOf(t, m.gql(calendarQuery, vars), "calendarEvents")
	if len(page["items"].([]any)) != 1 {
		t.Fatal("void must hide rain", page)
	}
	rows, err := m.q.ExportRain(ctx, m.wsID)
	if err != nil || len(rows) != 2 {
		t.Fatal("rain export must include history", err, len(rows))
	}
	var audits int
	if err := m.pool.QueryRow(ctx, `SELECT count(*) FROM audit_revisions WHERE aggregate_type='rain' AND workspace_id=$1`, m.wsID).Scan(&audits); err != nil || audits != 3 {
		t.Fatal("one audit per logical mutation", err, audits)
	}
}
func TestCalendarPhotoCapturedAndWorkspaceDaysHTTP(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	ctx := context.Background()
	plant := mustPlantGQL(t, m)
	if _, err := m.pool.Exec(ctx, `UPDATE workspace SET timezone='America/Chicago' WHERE id=$1`, m.wsID); err != nil {
		t.Fatal(err)
	}
	photo := attachPhotoMut(t, m, plant, readyPhoto(t, m), map[string]any{"takenAt": "2026-11-02T05:30:00Z", "dateSource": "USER", "viewpoint": "WHOLE"})
	if _, err := m.pool.Exec(ctx, `UPDATE plant_photos SET created_at='2026-11-10T12:00:00Z' WHERE id=$1`, photo["id"]); err != nil {
		t.Fatal(err)
	} // Captured Nov 1 local (DST fall-back), added Nov 10.
	// Upload-only/unknown captures must never be placed on the upload day as though snapped then.
	fallback := attachPhotoMut(t, m, plant, readyPhoto(t, m), map[string]any{"dateSource": "UPLOAD_FALLBACK", "viewpoint": "WHOLE"})
	unknown := attachPhotoMut(t, m, plant, readyPhoto(t, m), map[string]any{"dateSource": "EXIF", "viewpoint": "WHOLE"})
	if _, err := m.pool.Exec(ctx, `UPDATE plant_photos SET created_at='2026-11-02T05:30:00Z' WHERE id=$1 OR id=$2`, fallback["id"], unknown["id"]); err != nil {
		t.Fatal(err)
	}
	old := attachPhotoMut(t, m, plant, readyPhoto(t, m), map[string]any{"takenAt": "2025-01-01T12:00:00Z", "dateSource": "USER", "viewpoint": "WHOLE"})
	if _, err := m.pool.Exec(ctx, `UPDATE plant_photos SET created_at='2026-11-02T05:30:00Z' WHERE id=$1`, old["id"]); err != nil {
		t.Fatal(err)
	}
	oldPage := dataOf(t, m.gql(calendarQuery, map[string]any{"from": "2025-01-01", "to": "2025-01-31", "first": 100}), "calendarEvents")
	oldItems := oldPage["items"].([]any)
	if len(oldItems) != 1 || oldItems[0].(map[string]any)["entityId"] != old["id"] || oldItems[0].(map[string]any)["day"] != "2025-01-01" {
		t.Fatal("old imported photo must appear on capture day", oldPage)
	}
	_, ps := runMut(t, m, "recordActivity", "RecordActivityInput", map[string]any{"plantId": plant, "kind": "WATER", "occurredAt": "2026-11-02T06:30:00Z", "quantity": "1", "unit": "L"}, nil, actFields())
	mustNoProblems(t, ps, "water")
	page := dataOf(t, m.gql(calendarQuery, map[string]any{"from": "2026-11-01", "to": "2026-11-02", "first": 100}), "calendarEvents")
	items := page["items"].([]any)
	if len(items) != 2 {
		t.Fatal(page)
	}
	if items[0].(map[string]any)["kind"] != "PHOTO" || items[0].(map[string]any)["day"] != "2026-11-01" || items[1].(map[string]any)["activityKind"] != "WATER" || items[1].(map[string]any)["day"] != "2026-11-02" {
		t.Fatal("photo uses capture date and workspace DST day", page)
	}
	if len(m.gql(calendarQuery, map[string]any{"from": "2026-01-01", "to": "2026-11-30", "first": 100}).Errors) == 0 {
		t.Fatal("unbounded query accepted")
	}
	if _, err := m.pool.Exec(ctx, `UPDATE plant_photos SET deleted_at=now() WHERE id=$1`, photo["id"]); err != nil {
		t.Fatal(err)
	}
	page = dataOf(t, m.gql(calendarQuery, map[string]any{"from": "2026-11-01", "to": "2026-11-02", "first": 100}), "calendarEvents")
	if len(page["items"].([]any)) != 1 {
		t.Fatal("deleted photo remains", page)
	}
}
