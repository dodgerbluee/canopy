package collection

// T24 Verify: archive/context mutation transaction rollback. The care
// hook runs inside the collection mutation's transaction; a hook failure
// must roll the collection write back with it (status, audit, and
// context rows all revert together).

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
)

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("uuid %q: %v", s, err)
	}
	return u
}

func countContexts(t *testing.T, pool *pgxpool.Pool, plantID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM plant_contexts WHERE plant_id = $1", plantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type failingHook struct {
	NoopHook
	failOn string // "archive" | "restore" | "context"
	calls  *[]string
}

func (h failingHook) note(op string) {
	if h.calls != nil {
		*h.calls = append(*h.calls, op)
	}
}

func (h failingHook) PlantArchived(ctx context.Context, q *dbgen.Queries, ws, plant pgtype.UUID) error {
	h.note("archive")
	if h.failOn == "archive" {
		return errors.New("care backend down")
	}
	return nil
}

func (h failingHook) PlantRestored(ctx context.Context, q *dbgen.Queries, ws, plant pgtype.UUID) error {
	h.note("restore")
	if h.failOn == "restore" {
		return errors.New("care backend down")
	}
	return nil
}

func (h failingHook) PlantContextChanged(ctx context.Context, q *dbgen.Queries, ws, plant pgtype.UUID, effective time.Time) error {
	h.note("context")
	if h.failOn == "context" {
		return errors.New("care backend down")
	}
	return nil
}

func TestHookFailureRollsBackCollectionWrites(t *testing.T) {
	ctx, pool, a := scratch(t)
	q := dbgen.New(pool)

	base := New(pool, slog.New(slog.DiscardHandler), NoopHook{})
	res, probs, err := base.CreatePlant(ctx, a, CreatePlantInput{
		Name: "Fig", Type: "INDIVIDUAL", Category: "OTHER",
	}, nil)
	if err != nil || len(probs) > 0 {
		t.Fatalf("createPlant: err=%v probs=%v", err, probs)
	}
	plantID := res.ID
	rev := res.Revision

	// Archive with a failing hook: VALIDATION problem, plant stays ACTIVE.
	bad := New(pool, slog.New(slog.DiscardHandler), failingHook{failOn: "archive"})
	_, probs, err = bad.ArchivePlant(ctx, a, plantID, rev, nil)
	if err != nil {
		t.Fatalf("archivePlant: %v", err)
	}
	if len(probs) == 0 || probs[0].Message != "care integration failed" {
		t.Fatalf("archive hook failure must surface: %+v", probs)
	}
	plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{
		ID: mustUUID(t, plantID), WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if plant.Status != dbgen.PlantStatusACTIVE || plant.Revision != rev {
		t.Fatalf("archive must roll back: status=%v rev=%d", plant.Status, plant.Revision)
	}

	// Context edit with a failing hook: no context row, plant revision unchanged.
	badCtx := New(pool, slog.New(slog.DiscardHandler), failingHook{failOn: "context"})
	_, probs, err = badCtx.SetPlantContext(ctx, a, SetPlantContextInput{
		PlantID: plantID, ExpectedRevision: rev,
		Context: &ContextInput{Placement: "CONTAINER"},
	}, nil)
	if err != nil {
		t.Fatalf("setPlantContext: %v", err)
	}
	if len(probs) == 0 || probs[0].Message != "care integration failed" {
		t.Fatalf("context hook failure must surface: %+v", probs)
	}
	if n := countContexts(t, pool, plantID); n != 0 {
		t.Fatalf("context edit must roll back, got %d rows", n)
	}
	plant, err = q.GetPlant(ctx, dbgen.GetPlantParams{
		ID: mustUUID(t, plantID), WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if plant.Revision != rev {
		t.Fatalf("context rollback must not bump revision: %d", plant.Revision)
	}

	// A healthy hook still succeeds end-to-end (archive then restore).
	var calls []string
	good := New(pool, slog.New(slog.DiscardHandler), failingHook{calls: &calls})
	if _, probs, err = good.ArchivePlant(ctx, a, plantID, rev, nil); err != nil || len(probs) > 0 {
		t.Fatalf("archive with healthy hook: err=%v probs=%v", err, probs)
	}
	plant, _ = q.GetPlant(ctx, dbgen.GetPlantParams{ID: mustUUID(t, plantID), WorkspaceID: a.WorkspaceID})
	if plant.Status != dbgen.PlantStatusARCHIVED {
		t.Fatalf("healthy hook archive must commit: %v", plant.Status)
	}
	if _, probs, err = good.RestorePlant(ctx, a, plantID, plant.Revision, nil); err != nil || len(probs) > 0 {
		t.Fatalf("restore with healthy hook: err=%v probs=%v", err, probs)
	}
	if len(calls) != 2 || calls[0] != "archive" || calls[1] != "restore" {
		t.Fatalf("hook calls: %v", calls)
	}
}

// Restore rollback: plant stays ARCHIVED when the restore hook fails.
func TestRestoreHookFailureKeepsPlantArchived(t *testing.T) {
	ctx, pool, a := scratch(t)
	q := dbgen.New(pool)

	base := New(pool, slog.New(slog.DiscardHandler), NoopHook{})
	res, probs, err := base.CreatePlant(ctx, a, CreatePlantInput{
		Name: "Fig", Type: "INDIVIDUAL", Category: "OTHER",
	}, nil)
	if err != nil || len(probs) > 0 {
		t.Fatalf("createPlant: err=%v probs=%v", err, probs)
	}
	archived, probs, err := base.ArchivePlant(ctx, a, res.ID, res.Revision, nil)
	if err != nil || len(probs) > 0 {
		t.Fatalf("archivePlant: err=%v probs=%v", err, probs)
	}

	bad := New(pool, slog.New(slog.DiscardHandler), failingHook{failOn: "restore"})
	_, probs, err = bad.RestorePlant(ctx, a, res.ID, archived.Revision, nil)
	if err != nil {
		t.Fatalf("restorePlant: %v", err)
	}
	if len(probs) == 0 || probs[0].Message != "care integration failed" {
		t.Fatalf("restore hook failure must surface: %+v", probs)
	}
	plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{
		ID: mustUUID(t, res.ID), WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if plant.Status != dbgen.PlantStatusARCHIVED || plant.Revision != archived.Revision {
		t.Fatalf("restore must roll back: status=%v rev=%d", plant.Status, plant.Revision)
	}
}
