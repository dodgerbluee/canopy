package db_test

import (
	"context"
	"strings"
	"testing"

	candb "canopy/server/db"
	"github.com/jackc/pgx/v5/pgtype"
)

func planningIdea(t *testing.T, s seed, name string) pgtype.UUID {
	t.Helper()
	id := uid()
	if _, err := s.pool.Exec(context.Background(), "INSERT INTO planting_plans(id,workspace_id,name) VALUES($1,$2,$3)", id, s.wsID, name); err != nil {
		t.Fatal(err)
	}
	return id
}
func planningOtherWorkspace(t *testing.T, s seed) (pgtype.UUID, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	ws, location, plant := uid(), uid(), uid()
	for _, op := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Other','UTC','USD')", []any{ws, s.ownerID}},
		{"INSERT INTO locations(id,workspace_id,name,situation) VALUES($1,$2,'Other patio','OUTDOOR')", []any{location, ws}},
		{"INSERT INTO plants(id,workspace_id,name,type,category) VALUES($1,$2,'Other tree','INDIVIDUAL','OTHER')", []any{plant, ws}},
	} {
		if _, err := s.pool.Exec(ctx, op.sql, op.args...); err != nil {
			t.Fatal(err)
		}
	}
	return ws, location, plant
}
func TestPlanningIdeaDoesNotInventGardenRecords(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	plan := planningIdea(t, s, "Future blueberry")
	var state string
	var period, budget, method, quantity bool
	if err := s.pool.QueryRow(ctx, "SELECT state,target_precision IS NULL,budget_minor IS NULL,method IS NULL,desired_quantity IS NULL FROM planting_plans WHERE id=$1", plan).Scan(&state, &period, &budget, &method, &quantity); err != nil {
		t.Fatal(err)
	}
	if state != "IDEA" || !period || !budget || !method || !quantity {
		t.Fatal("unknown intent became concrete data")
	}
	var plants, purchases, care, activities int
	if err := s.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM plants),(SELECT count(*) FROM purchases),(SELECT count(*) FROM care_plans),(SELECT count(*) FROM activities)").Scan(&plants, &purchases, &care, &activities); err != nil {
		t.Fatal(err)
	}
	if plants != 1 || purchases != 0 || care != 0 || activities != 0 {
		t.Fatalf("phantom garden records %d %d %d %d", plants, purchases, care, activities)
	}
}
func TestPlanningPeriodDatabaseShapes(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	cases := []struct {
		name, columns, values string
		valid                 bool
	}{
		{"year only", "target_precision,target_year", "'YEAR',2027", true},
		{"abstract winter", "target_precision,target_year,target_season", "'SEASON',2027,'WINTER'", true},
		{"cross year months", "target_precision,target_from_month,target_to_month", "'MONTH_RANGE','2026-12','2027-02'", true},
		{"leap days", "target_precision,target_from,target_to", "'DATE_RANGE','2028-02-29','2028-03-01'", true},
		{"same day", "target_precision,target_from,target_to", "'DATE_RANGE','2027-03-14','2027-03-14'", true},
		{"missing precision", "target_year", "2027", false},
		{"year missing", "target_precision", "'YEAR'", false},
		{"year zero", "target_precision,target_year", "'YEAR',0", false},
		{"year plus exact date", "target_precision,target_year,target_from", "'YEAR',2027,'2027-01-01'", false},
		{"season missing", "target_precision,target_year", "'SEASON',2027", false},
		{"season plus months", "target_precision,target_year,target_season,target_from_month", "'SEASON',2027,'SPRING','2027-03'", false},
		{"reversed months", "target_precision,target_from_month,target_to_month", "'MONTH_RANGE','2027-02','2027-01'", false},
		{"noncanonical month", "target_precision,target_from_month,target_to_month", "'MONTH_RANGE','2027-1','2027-02'", false},
		{"invalid month", "target_precision,target_from_month,target_to_month", "'MONTH_RANGE','2027-13','2028-02'", false},
		{"month zero year", "target_precision,target_from_month,target_to_month", "'MONTH_RANGE','0000-01','2027-02'", false},
		{"missing month end", "target_precision,target_from_month", "'MONTH_RANGE','2027-01'", false},
		{"reversed days", "target_precision,target_from,target_to", "'DATE_RANGE','2027-03-02','2027-03-01'", false},
		{"missing day end", "target_precision,target_from", "'DATE_RANGE','2027-03-01'", false},
		{"infinite date", "target_precision,target_from,target_to", "'DATE_RANGE','2027-03-01','infinity'", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.pool.Exec(ctx, "INSERT INTO planting_plans(id,workspace_id,name,"+tc.columns+") VALUES($1,$2,'Future crop',"+tc.values+")", uid(), s.wsID)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid {
				expectDBError(t, err, "planting_target_shape")
			}
		})
	}
	var start, end pgtype.Date
	if err := s.pool.QueryRow(ctx, "SELECT target_from,target_to FROM planting_plans WHERE target_precision='YEAR'").Scan(&start, &end); err != nil {
		t.Fatal(err)
	}
	if start.Valid || end.Valid {
		t.Fatal("year silently acquired concrete dates")
	}
}
func TestPlanningBudgetExactAndUnknown(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	for _, test := range []struct {
		minor, currency string
		valid           bool
	}{
		{"NULL", "NULL", true}, {"0", "'USD'", true}, {"1000000000000000", "'JPY'", true},
		{"1000000000000001", "'USD'", false}, {"-1", "'USD'", false}, {"100", "NULL", false}, {"NULL", "'USD'", false}, {"100", "'usd'", false},
	} {
		_, err := s.pool.Exec(ctx, "INSERT INTO planting_plans(id,workspace_id,name,budget_minor,budget_currency) VALUES($1,$2,'Estimate',"+test.minor+","+test.currency+")", uid(), s.wsID)
		if test.valid && err != nil {
			t.Fatal(err)
		}
		if !test.valid && err == nil {
			t.Fatalf("invalid budget accepted: %+v", test)
		}
	}
	var value string
	if err := s.pool.QueryRow(ctx, "SELECT budget_minor::text FROM planting_plans WHERE budget_currency='JPY'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "1000000000000000" {
		t.Fatalf("budget rounded: %s", value)
	}
}
func TestPlanningWorkspaceForeignKeys(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	otherWS, otherLocation, otherPlant := planningOtherWorkspace(t, s)
	plan := planningIdea(t, s, "Local plan")
	otherPlan := uid()
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_plans(id,workspace_id,name) VALUES($1,$2,'Other plan')", otherPlan, otherWS); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE planting_plans SET intended_location_id=$2 WHERE id=$1", []any{plan, otherLocation}},
		{"INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on) VALUES($1,$2,$3,'BUY','Buy','2027-03-01')", []any{uid(), otherWS, plan}},
		{"INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256) VALUES($1,$2,$3,1,$4)", []any{uid(), otherWS, plan, strings.Repeat("a", 64)}},
		{"INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,mode) VALUES($1,$2,$3,$4,'EXISTING')", []any{uid(), s.wsID, plan, otherPlant}},
	} {
		_, err := s.pool.Exec(ctx, op.sql, op.args...)
		expectDBError(t, err, "foreign key")
	}
	guidance := uid()
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256) VALUES($1,$2,$3,1,$4)", guidance, otherWS, otherPlan, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	_, err := s.pool.Exec(ctx, "UPDATE planting_plans SET target_precision='YEAR',target_year=2027,accepted_guidance_id=$2 WHERE id=$1", plan, guidance)
	expectDBError(t, err, "planting_accepted_guidance_scope")
}
func TestPlanningSameWorkspaceStillRequiresSamePlan(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	first, second := planningIdea(t, s, "First"), planningIdea(t, s, "Second")
	action, snapshot := uid(), uid()
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on) VALUES($1,$2,$3,'BUY','Buy','2027-01-01')", action, s.wsID, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256) VALUES($1,$2,$3,1,$4)", snapshot, s.wsID, second, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,action_id,mode) VALUES($1,$2,$3,$4,$5,'EXISTING')", uid(), s.wsID, first, s.plantID, action)
	expectDBError(t, err, "foreign key")
	_, err = s.pool.Exec(ctx, "UPDATE planting_plans SET target_precision='YEAR',target_year=2027,accepted_guidance_id=$2 WHERE id=$1", first, snapshot)
	expectDBError(t, err, "planting_accepted_guidance_scope")
}
func TestPlanningWorkspaceDeletionHandlesProvenanceCycle(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	plan := planningIdea(t, s, "Workspace lifecycle")
	snapshot := uid()
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state,facts,retrieved_at) VALUES($1,$2,$3,1,$4,'READY','[{\"id\":\"source-fact\"}]',now())", snapshot, s.wsID, plan, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE planting_plans SET target_precision='YEAR',target_year=2027,accepted_guidance_id=$2 WHERE id=$1", plan, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,mode) VALUES($1,$2,$3,$4,'EXISTING')", uid(), s.wsID, plan, s.plantID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM workspace WHERE id=$1", s.wsID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := s.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM planting_plans)+(SELECT count(*) FROM planting_guidance_snapshots)+(SELECT count(*) FROM planting_fulfillments)").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("workspace deletion left planning rows")
	}
}
func TestPlanningActionsRequireChosenDayAndHonestCompletion(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	plan := planningIdea(t, s, "Future fig")
	for _, values := range []string{
		"NULL,NULL,'OPEN',NULL,NULL,NULL",
		"'2027-03-01','2027-03-02','OPEN',NULL,NULL,NULL",
		"'2027-03-01',NULL,'DONE',NULL,NULL,NULL",
		"'2027-03-01',NULL,'OPEN','2027-03-01',now(),NULL",
		"'2027-03-01',NULL,'DISMISSED',NULL,NULL,NULL",
	} {
		_, err := s.pool.Exec(ctx, "INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on,reminder_on,state,completed_on,completed_at,dismissed_at) VALUES($1,$2,$3,'PLANT_OUT','Plant out',"+values+")", uid(), s.wsID, plan)
		if err == nil {
			t.Fatalf("invalid action accepted: %s", values)
		}
	}
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on,reminder_on) VALUES($1,$2,$3,'PLANT_OUT','Plant out','2027-03-01','2027-02-20')", uid(), s.wsID, plan); err != nil {
		t.Fatal(err)
	}
}
func TestPlanningOwnershipDoesNotCompleteFutureWork(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	plan := planningIdea(t, s, "Bought now; plant out later")
	action, link := uid(), uid()
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on) VALUES($1,$2,$3,'PLANT_OUT','Plant out','2027-03-01')", action, s.wsID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,action_id,mode) VALUES($1,$2,$3,$4,$5,'EXISTING')", link, s.wsID, plan, s.plantID, action); err != nil {
		t.Fatal(err)
	}
	var state, actionState string
	var unplanted bool
	if err := s.pool.QueryRow(ctx, "SELECT p.state,a.state,(SELECT planted_on IS NULL FROM plants WHERE id=$2) FROM planting_plans p JOIN planting_actions a ON a.plan_id=p.id WHERE p.id=$1", plan, s.plantID).Scan(&state, &actionState, &unplanted); err != nil {
		t.Fatal(err)
	}
	if state != "IDEA" || actionState != "OPEN" || !unplanted {
		t.Fatal("ownership fabricated completed planting")
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,mode) VALUES($1,$2,$3,$4,'EXISTING')", uid(), s.wsID, plan, s.plantID)
	expectDBError(t, err, "duplicate key")
	if _, err := s.pool.Exec(ctx, "DELETE FROM plants WHERE id=$1", s.plantID); err != nil {
		t.Fatal(err)
	}
	var plant pgtype.UUID
	if err := s.pool.QueryRow(ctx, "SELECT plant_id FROM planting_fulfillments WHERE id=$1", link).Scan(&plant); err != nil {
		t.Fatal(err)
	}
	if plant.Valid {
		t.Fatal("purged identity retained")
	}
	var links int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM planting_fulfillments WHERE plan_id=$1", plan).Scan(&links); err != nil || links != 1 {
		t.Fatal("purge removed replay-protection marker")
	}
}
func TestPlanningReadyGuidanceAndAcceptedProvenance(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	plan := planningIdea(t, s, "Evidence test")
	snapshot := uid()
	_, err := s.pool.Exec(ctx, "INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state) VALUES($1,$2,$3,1,$4,'READY')", snapshot, s.wsID, plan, strings.Repeat("a", 64))
	expectDBError(t, err, "planting_guidance_ready")
	if _, err := s.pool.Exec(ctx, "INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state,facts,retrieved_at) VALUES($1,$2,$3,1,$4,'READY','[{\"id\":\"source-fact\"}]',now())", snapshot, s.wsID, plan, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, "UPDATE planting_guidance_snapshots SET message='rewrite' WHERE id=$1", snapshot)
	expectDBError(t, err, "immutable")
	if _, err = s.pool.Exec(ctx, "UPDATE planting_plans SET target_precision='YEAR',target_year=2027,accepted_guidance_id=$2 WHERE id=$1", plan, snapshot); err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, "DELETE FROM planting_guidance_snapshots WHERE id=$1", snapshot)
	expectDBError(t, err, "planting_accepted_guidance_scope")
	if _, err = s.pool.Exec(ctx, "UPDATE planting_plans SET guidance_generation=2 WHERE id=$1", plan); err != nil {
		t.Fatal(err)
	}
	var accepted pgtype.UUID
	var yr int
	if err = s.pool.QueryRow(ctx, "SELECT accepted_guidance_id,target_year FROM planting_plans WHERE id=$1", plan).Scan(&accepted, &yr); err != nil || accepted != snapshot || yr != 2027 {
		t.Fatal("refresh changed accepted intent")
	}
	if _, err = s.pool.Exec(ctx, "DELETE FROM planting_plans WHERE id=$1", plan); err != nil {
		t.Fatalf("provenance cycle blocked deletion: %v", err)
	}
}
func TestPlanningLifecycleMetadata(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	plan := planningIdea(t, s, "Lifecycle")
	for _, set := range []string{"state='COMPLETED'", "state='CANCELLED',cancelled_at=now()", "resume_state='PLANNED'"} {
		_, err := s.pool.Exec(ctx, "UPDATE planting_plans SET "+set+" WHERE id=$1", plan)
		if err == nil {
			t.Fatalf("invalid lifecycle accepted: %s", set)
		}
	}
	if _, err := s.pool.Exec(ctx, "UPDATE planting_plans SET state='CANCELLED',resume_state='IDEA',cancelled_at=now() WHERE id=$1", plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE planting_plans SET state=resume_state,resume_state=NULL,cancelled_at=NULL WHERE id=$1", plan); err != nil {
		t.Fatal(err)
	}
}
func TestPlanningMigrationDownUpPreservesExistingGarden(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	planningIdea(t, s, "Disposable intention")
	var before []byte
	if err := s.pool.QueryRow(ctx, "SELECT to_jsonb(p) FROM plants p WHERE id=$1", s.plantID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	scheduleMigration, err := candb.Migrations.ReadFile("migrations/00021_planning_schedule.sql")
	if err != nil {
		t.Fatal(err)
	}
	scheduleUp, scheduleDown, foundSchedule := strings.Cut(string(scheduleMigration), "-- +goose Down")
	if !foundSchedule {
		t.Fatal("missing schedule Down")
	}
	if _, err = s.pool.Exec(ctx, scheduleDown); err != nil {
		t.Fatal(err)
	}
	guidanceMigration, err := candb.Migrations.ReadFile("migrations/00020_planning_guidance.sql")
	if err != nil {
		t.Fatal(err)
	}
	guidanceUp, guidanceDown, foundGuidance := strings.Cut(string(guidanceMigration), "-- +goose Down")
	if !foundGuidance {
		t.Fatal("missing guidance Down")
	}
	if _, err = s.pool.Exec(ctx, guidanceDown); err != nil {
		t.Fatal(err)
	}
	migration, err := candb.Migrations.ReadFile("migrations/00019_planting_plans.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, found := strings.Cut(string(migration), "-- +goose Down")
	if !found {
		t.Fatal("missing Down")
	}
	if _, err = s.pool.Exec(ctx, down); err != nil {
		t.Fatalf("down: %v", err)
	}
	var absent bool
	if err = s.pool.QueryRow(ctx, "SELECT to_regclass('planting_plans') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatal("tables survived down")
	}
	if _, err = s.pool.Exec(ctx, up); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if _, err = s.pool.Exec(ctx, guidanceUp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, scheduleUp); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err = s.pool.QueryRow(ctx, "SELECT to_jsonb(p) FROM plants p WHERE id=$1", s.plantID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("migration changed existing plant")
	}
}

func TestPlanningScheduleSelectionNeedsAcceptedPeriodAndSurvivesMigrationRollback(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	plan, snapshot := planningIdea(t, s, "Schedule metadata"), uid()
	for _, raw := range []string{`{}`, `[]`, `{"oversize":"` + strings.Repeat("x", 8200) + `"}`} {
		_, err := s.pool.Exec(ctx, `UPDATE planting_plans SET accepted_schedule=$2 WHERE id=$1`, plan, raw)
		expectDBError(t, err, "planting_schedule_selection")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state,facts,retrieved_at) VALUES($1,$2,$3,1,repeat('a',64),'READY','[{}]',now())`, snapshot, s.wsID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE planting_plans SET target_precision='YEAR',target_year=2027,accepted_guidance_id=$2,accepted_schedule='{"version":1}' WHERE id=$1`, plan, snapshot); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := s.pool.QueryRow(ctx, `SELECT to_jsonb(p)-'accepted_schedule' FROM planting_plans p WHERE id=$1`, plan).Scan(&before); err != nil {
		t.Fatal(err)
	}
	migration, err := candb.Migrations.ReadFile("migrations/00021_planning_schedule.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, _ := strings.Cut(string(migration), "-- +goose Down")
	if _, err = s.pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT to_jsonb(p)-'accepted_schedule' FROM planting_plans p WHERE id=$1`, plan).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("selection migration changed existing intent/provenance")
	}
	var unknown bool
	if err = s.pool.QueryRow(ctx, `SELECT accepted_schedule IS NULL FROM planting_plans WHERE id=$1`, plan).Scan(&unknown); err != nil || !unknown {
		t.Fatal("migration invented selection metadata", err)
	}
}
