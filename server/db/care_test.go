// T02.care data-contract acceptance suite: care/source/profile/notification
// constraints verified on real PostgreSQL 17 (PGTEST_DSN), one isolated schema
// per test via the shared mustPool harness from db_test.go.
package db_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedCareWorld adds a source, a draft profile with one rule, a plan and an
// occurrence on top of seedWorld; ids are returned for targeted tests.
type careSeed struct {
	pool               *pgxpool.Pool
	wsID, plantID      pgtype.UUID
	ownerID            pgtype.UUID
	sourceID, srcVerID pgtype.UUID
	profileID, verID   pgtype.UUID
	ruleID             pgtype.UUID
	planID, occID      pgtype.UUID
}

func seedCareWorld(t *testing.T) careSeed {
	t.Helper()
	s := seedWorld(t)
	ctx := context.Background()
	cs := careSeed{pool: s.pool, wsID: s.wsID, plantID: s.plantID, ownerID: s.ownerID}
	cs.sourceID, cs.srcVerID = uid(), uid()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO sources (id, workspace_id, kind, title) VALUES ($1,$2,'EXTENSION_PUBLICATION','UC CEFAEN citrus guidance')`,
		cs.sourceID, cs.wsID); err != nil {
		t.Fatalf("source: %v", err)
	}
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO source_versions (id, source_id, workspace_id, version, citation, summary)
		 VALUES ($1,$2,$3,1,'UC CEFAEN (2024)','Water when top 2-3 cm dry.')`,
		cs.srcVerID, cs.sourceID, cs.wsID); err != nil {
		t.Fatalf("source_version: %v", err)
	}
	cs.profileID, cs.verID, cs.ruleID = uid(), uid(), uid()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_profiles (id, workspace_id, category) VALUES ($1,$2,'CITRUS')`,
		cs.profileID, cs.wsID); err != nil {
		t.Fatalf("profile: %v", err)
	}
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_profile_versions (id, profile_id, workspace_id, version, status)
		 VALUES ($1,$2,$3,1,'DRAFT')`,
		cs.verID, cs.profileID, cs.wsID); err != nil {
		t.Fatalf("profile_version: %v", err)
	}
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, activity_kind, recurrence, guidance_text)
		 VALUES ($1,$2,$3,'water-meyer','ACTION','WATER',
		         '{"form":"EVERY_N_DAYS","days":7,"anchor":"LAST_QUALIFYING_COMPLETION"}'::jsonb,
		         'Water when the top 2-3 cm of medium is dry.')`,
		cs.ruleID, cs.verID, cs.wsID); err != nil {
		t.Fatalf("rule: %v", err)
	}
	cs.planID, cs.occID = uid(), uid()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_plans (id, workspace_id, plant_id, profile_version_id, timezone)
		 VALUES ($1,$2,$3,$4,'America/Los_Angeles')`,
		cs.planID, cs.wsID, cs.plantID, cs.verID); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_occurrences (id, workspace_id, plan_id, rule_key, kind, activity_kind, occurrence_key, original_due_date, current_due_date, plan_timezone)
		 VALUES ($1,$2,$3,'water-meyer','ACTION','WATER','k-1','2026-11-04','2026-11-04','America/Los_Angeles')`,
		cs.occID, cs.wsID, cs.planID); err != nil {
		t.Fatalf("occurrence: %v", err)
	}
	return cs
}

// ---------- source versions are append-only ----------

func TestCareSourceVersionImmutable(t *testing.T) {
	cs := seedCareWorld(t)
	_, err := cs.pool.Exec(context.Background(),
		`UPDATE source_versions SET summary = 'rewritten under readers' WHERE id = $1`, cs.srcVerID)
	expectDBError(t, err, "immutable table")
}

func TestCareSourceVersionUniqueAndShaShape(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	_, err := cs.pool.Exec(ctx,
		`INSERT INTO source_versions (id, source_id, workspace_id, version, citation, summary)
		 VALUES ($1,$2,$3,1,'dup','dup')`, uid(), cs.sourceID, cs.wsID)
	expectDBError(t, err, "duplicate key")
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO source_versions (id, source_id, workspace_id, version, citation, summary, content_sha256)
		 VALUES ($1,$2,$3,2,'c','s','not-hex')`, uid(), cs.sourceID, cs.wsID)
	expectDBError(t, err, "check constraint")
}

func TestCareCitedSourceVersionCannotBeDeleted(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_rule_sources (rule_id, source_version_id) VALUES ($1,$2)`,
		cs.ruleID, cs.srcVerID); err != nil {
		t.Fatalf("rule_sources: %v", err)
	}
	_, err := cs.pool.Exec(ctx, `DELETE FROM source_versions WHERE id = $1`, cs.srcVerID)
	expectDBError(t, err, "foreign key")
}

// ---------- profile version lifecycle ----------

func TestCarePublishFlowAndGuards(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	// Publish = insert new version as DRAFT, copy rules, flip to REVIEWED.
	newVer := uid()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_profile_versions (id, profile_id, workspace_id, version, status)
		 VALUES ($1,$2,$3,2,'DRAFT')`, newVer, cs.profileID, cs.wsID); err != nil {
		t.Fatalf("draft v2: %v", err)
	}
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, activity_kind, recurrence, guidance_text)
		 SELECT $1,$2,$3,rule_key,kind,activity_kind,recurrence,guidance_text FROM care_rules WHERE profile_version_id = $4`,
		uid(), newVer, cs.wsID, cs.verID); err != nil {
		t.Fatalf("copy rules: %v", err)
	}
	// Flip without reviewer fields must fail.
	_, err := cs.pool.Exec(ctx,
		`UPDATE care_profile_versions SET status='REVIEWED' WHERE id=$1`, newVer)
	expectDBError(t, err, "publishing requires reviewer attribution fields")
	// Proper publish succeeds.
	if _, err := cs.pool.Exec(ctx,
		`UPDATE care_profile_versions
		   SET status='REVIEWED', reviewer_label='owner', reviewed_on='2026-11-02', published_at=now()
		 WHERE id=$1`, newVer); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Published rule set is frozen: insert and delete both rejected.
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, recurrence, guidance_text)
		 VALUES ($1,$2,$3,'sneak','CHECK','{"form":"ONE_TIME","date":"2027-01-01"}'::jsonb,'x')`,
		uid(), newVer, cs.wsID)
	expectDBError(t, err, "non-draft profile version are immutable")
	_, err = cs.pool.Exec(ctx, `DELETE FROM care_rules WHERE profile_version_id = $1`, newVer)
	expectDBError(t, err, "non-draft profile version are immutable")
	// Published versions are undeletable (rule sets cannot be cascade-removed).
	_, err = cs.pool.Exec(ctx, `DELETE FROM care_profile_versions WHERE id = $1`, newVer)
	expectDBError(t, err, "published care profile versions cannot be deleted")
	// Retire may only touch status + retired_at.
	if _, err := cs.pool.Exec(ctx,
		`UPDATE care_profile_versions SET status='RETIRED', retired_at=now() WHERE id=$1`, newVer); err != nil {
		t.Fatalf("retire: %v", err)
	}
	_, err = cs.pool.Exec(ctx,
		`UPDATE care_profile_versions SET retired_at=NULL WHERE id=$1`, newVer)
	expectDBError(t, err, "retired care profile versions are immutable")
}

func TestCarePinnedProfileVersionRestrict(t *testing.T) {
	cs := seedCareWorld(t)
	_, err := cs.pool.Exec(context.Background(),
		`DELETE FROM care_profile_versions WHERE id = $1`, cs.verID)
	expectDBError(t, err, "foreign key")
}

// ---------- recurrence contract ----------

func TestCareRecurrenceForms(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	good := []string{
		`{"form":"WEEKLY_DAYS","weekdays":["MONDAY","SATURDAY"]}`,
		`{"form":"ANNUAL_WINDOW","month":11,"day":1,"window_days":90,"every_years":1}`,
		`{"form":"ONE_TIME","date":"2027-02-15"}`,
		`{"form":"EVERY_N_DAYS","days":365,"anchor":"PLAN_START"}`,
	}
	for i, r := range good {
		if _, err := cs.pool.Exec(ctx,
			`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, recurrence, guidance_text)
			 VALUES ($1,$2,$3,$4,'CHECK',$5::jsonb,'ok')`,
			uid(), cs.verID, cs.wsID, "good-"+strconv.Itoa(i), r); err != nil {
			t.Fatalf("valid recurrence %s rejected: %v", r, err)
		}
	}
	bad := []string{
		`{"form":"EVERY_N_DAYS","days":0,"anchor":"PLAN_START"}`,
		`{"form":"EVERY_N_DAYS","days":366,"anchor":"PLAN_START"}`,
		`{"form":"EVERY_N_DAYS","days":7}`,
		`{"form":"ANNUAL_WINDOW","month":13,"day":1}`,
		`{"form":"ANNUAL_WINDOW","month":11,"day":32}`,
		`{"form":"ONE_TIME","date":20270101}`,
		`{"form":"RRULE","rule":"FREQ=DAILY"}`,
		`{"days":7,"anchor":"PLAN_START"}`,
		`{"form":"WEEKLY_DAYS","weekdays":[]}`,
	}
	for i, r := range bad {
		_, err := cs.pool.Exec(ctx,
			`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, recurrence, guidance_text)
			 VALUES ($1,$2,$3,$4,'CHECK',$5::jsonb,'ok')`,
			uid(), cs.verID, cs.wsID, "bad-"+strconv.Itoa(i), r)
		expectDBError(t, err, "check constraint")
	}
}

func TestCareOverrideRecurrenceCheck(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_plan_overrides (id, plan_id, workspace_id, rule_key, recurrence_override)
		 VALUES ($1,$2,$3,'water-meyer','{"form":"EVERY_N_DAYS","days":5,"anchor":"LAST_QUALIFYING_COMPLETION"}'::jsonb)`,
		uid(), cs.planID, cs.wsID); err != nil {
		t.Fatalf("valid override: %v", err)
	}
	_, err := cs.pool.Exec(ctx,
		`INSERT INTO care_plan_overrides (id, plan_id, workspace_id, rule_key, recurrence_override)
		 VALUES ($1,$2,$3,'water-meyer','{"form":"EVERY_N_DAYS","days":0,"anchor":"PLAN_START"}'::jsonb)`,
		uid(), cs.planID, cs.wsID)
	expectDBError(t, err, "check constraint")
}

func TestCareRuleKindActivityExclusivity(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	_, err := cs.pool.Exec(ctx,
		`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, activity_kind, recurrence, guidance_text)
		 VALUES ($1,$2,$3,'act-no-kind','ACTION',NULL,'{"form":"ONE_TIME","date":"2027-01-01"}'::jsonb,'x')`,
		uid(), cs.verID, cs.wsID)
	expectDBError(t, err, "chk_rule_activity_exclusive")
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, activity_kind, recurrence, guidance_text)
		 VALUES ($1,$2,$3,'chk-with-kind','CHECK','WATER','{"form":"ONE_TIME","date":"2027-01-01"}'::jsonb,'x')`,
		uid(), cs.verID, cs.wsID)
	expectDBError(t, err, "chk_rule_activity_exclusive")
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO care_rules (id, profile_version_id, workspace_id, rule_key, kind, recurrence, guidance_text, required_facts)
		 VALUES ($1,$2,$3,'bad-facts','CHECK','{"form":"ONE_TIME","date":"2027-01-01"}'::jsonb,'x', ARRAY['MOON_PHASE'])`,
		uid(), cs.verID, cs.wsID)
	expectDBError(t, err, "chk_rule_required_facts")
}

// ---------- plans ----------

func TestCareOnePlanPerPlant(t *testing.T) {
	cs := seedCareWorld(t)
	_, err := cs.pool.Exec(context.Background(),
		`INSERT INTO care_plans (id, workspace_id, plant_id, timezone) VALUES ($1,$2,$3,'America/Denver')`,
		uid(), cs.wsID, cs.plantID)
	expectDBError(t, err, "duplicate key")
}

func TestCarePlanFieldChecks(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	_, err := cs.pool.Exec(ctx,
		`INSERT INTO care_plans (id, workspace_id, plant_id, timezone, notification_time) VALUES ($1,$2,$3,'America/Denver','25:00')`,
		uid(), cs.wsID, uid())
	expectDBError(t, err, "check constraint")
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO care_plans (id, workspace_id, plant_id, timezone) VALUES ($1,$2,$3,'not a zone!')`,
		uid(), cs.wsID, uid())
	expectDBError(t, err, "check constraint")
}

// ---------- occurrences ----------

func TestCareOccurrenceLogicalKeyUnique(t *testing.T) {
	cs := seedCareWorld(t)
	_, err := cs.pool.Exec(context.Background(),
		`INSERT INTO care_occurrences (id, workspace_id, plan_id, rule_key, kind, activity_kind, occurrence_key, original_due_date, current_due_date, plan_timezone)
		 VALUES ($1,$2,$3,'water-meyer','ACTION','WATER','k-1','2026-11-11','2026-11-11','America/Los_Angeles')`,
		uid(), cs.wsID, cs.planID)
	expectDBError(t, err, "duplicate key")
}

func TestCareOccurrenceStateChecks(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	// COMPLETED requires the linked activity.
	_, err := cs.pool.Exec(ctx,
		`INSERT INTO care_occurrences (id, workspace_id, plan_id, rule_key, kind, activity_kind, occurrence_key, original_due_date, current_due_date, plan_timezone, status, resolved_at)
		 VALUES ($1,$2,$3,'r','ACTION','WATER','k-2','2026-11-04','2026-11-04','America/Los_Angeles','COMPLETED',now())`,
		uid(), cs.wsID, cs.planID)
	expectDBError(t, err, "chk_occurrence_completed_activity")
	// DUE must not carry resolved_at.
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO care_occurrences (id, workspace_id, plan_id, rule_key, kind, activity_kind, occurrence_key, original_due_date, current_due_date, plan_timezone, status, resolved_at)
		 VALUES ($1,$2,$3,'r','ACTION','WATER','k-3','2026-11-04','2026-11-04','America/Los_Angeles','DUE',now())`,
		uid(), cs.wsID, cs.planID)
	expectDBError(t, err, "chk_occurrence_resolution")
	// Snooze moves the effective date forward only.
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO care_occurrences (id, workspace_id, plan_id, rule_key, kind, activity_kind, occurrence_key, original_due_date, current_due_date, snoozed_until, plan_timezone)
		 VALUES ($1,$2,$3,'r','ACTION','WATER','k-4','2026-11-04','2026-11-04','2026-11-01','America/Los_Angeles')`,
		uid(), cs.wsID, cs.planID)
	expectDBError(t, err, "chk_occurrence_snooze_forward")
	// CHECK kind carries no activity kind.
	_, err = cs.pool.Exec(ctx,
		`INSERT INTO care_occurrences (id, workspace_id, plan_id, rule_key, kind, activity_kind, occurrence_key, original_due_date, current_due_date, plan_timezone)
		 VALUES ($1,$2,$3,'r','CHECK','WATER','k-5','2026-11-04','2026-11-04','America/Los_Angeles')`,
		uid(), cs.wsID, cs.planID)
	expectDBError(t, err, "chk_occurrence_activity_exclusive")
}

func TestCareJournalCareEntryProjection(t *testing.T) {
	cs := seedCareWorld(t)
	_, err := cs.pool.Exec(context.Background(),
		`INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
		 VALUES ($1,$2,$3,'CARE',$4,now(),'Watered (care plan)')`,
		uid(), cs.wsID, cs.plantID, cs.occID)
	if err != nil {
		t.Fatalf("CARE journal entry rejected: %v", err)
	}
}

// ---------- weather + notifications ----------

func TestCareWeatherWindowCheck(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	loc := uid()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO locations (id, workspace_id, name, situation) VALUES ($1,$2,'Patio','OUTDOOR')`,
		loc, cs.wsID); err != nil {
		t.Fatalf("location: %v", err)
	}
	_, err := cs.pool.Exec(ctx,
		`INSERT INTO weather_snapshots (id, workspace_id, location_id, provider, fetched_at, valid_from, valid_until, expires_at)
		 VALUES ($1,$2,$3,'p',now(),'2026-11-02T12:00:00Z','2026-11-02T06:00:00Z',now()+interval '1 hour')`,
		uid(), cs.wsID, loc)
	expectDBError(t, err, "chk_weather_window")
}

func TestCareNotificationDedup(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	dedup := "occ:5f0c1b6e:2026-11-04:INAPP"
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO notifications (id, workspace_id, owner_id, channel, dedup_key, ref_kind, ref_id, title, body)
		 VALUES ($1,$2,$3,'INAPP',$4,'OCCURRENCE',$5,'Water Meyer lemon','due today')`,
		uid(), cs.wsID, cs.ownerID, dedup, cs.occID); err != nil {
		t.Fatalf("notification: %v", err)
	}
	_, err := cs.pool.Exec(ctx,
		`INSERT INTO notifications (id, workspace_id, owner_id, channel, dedup_key, ref_kind, ref_id, title, body)
		 VALUES ($1,$2,$3,'INAPP',$4,'OCCURRENCE',$5,'Water Meyer lemon','duplicate tick')`,
		uid(), cs.wsID, cs.ownerID, dedup, cs.occID)
	expectDBError(t, err, "duplicate key")
}

// ---------- cascade behavior (plant purge path) ----------

func TestCarePlantDeleteCascadesOwnedCare(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	if _, err := cs.pool.Exec(ctx,
		`INSERT INTO care_rule_cursors (plan_id, workspace_id, rule_key, last_resolved_ordinal, decision, next_due_date)
		 VALUES ($1,$2,'water-meyer',3,'COMPLETED','2026-11-11')`, cs.planID, cs.wsID); err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if _, err := cs.pool.Exec(ctx, `DELETE FROM plants WHERE id=$1`, cs.plantID); err != nil {
		t.Fatalf("plant delete: %v", err)
	}
	for _, tbl := range []string{"care_plans", "care_occurrences", "care_rule_cursors"} {
		var n int64
		if err := cs.pool.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if n != 0 {
			t.Fatalf("%s not cascaded: %d rows remain", tbl, n)
		}
	}
}
