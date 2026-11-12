package platform

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	candb "canopy/server/db"
)

type noopTestArgs struct{ Note string }

func (noopTestArgs) Kind() string { return "noop_test" }

type signalWorker struct {
	river.WorkerDefaults[noopTestArgs]
	done chan string
}

func (w *signalWorker) Work(ctx context.Context, job *river.Job[noopTestArgs]) error {
	w.done <- job.Args.Note
	return nil
}

func setupRiverDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "t04_" + time.Now().UTC().Format("20060102150405000000")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	up, err := candb.AllUp()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)

	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateRiver(ctx, pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		a, err := pgx.Connect(ctx, dsn)
		if err == nil {
			_, _ = a.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			a.Close(ctx)
		}
	})
	return pool
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// workerBundle registers the test kind so Insert accepts it, without ever
// starting a consumer.
func workerBundle(_ string) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &signalWorker{done: make(chan string, 16)})
	return workers
}

// Acceptance: tx rollback leaves neither state nor job.
func TestRollbackLeavesNeitherStateNorJob(t *testing.T) {
	pool := setupRiverDB(t)
	ctx := context.Background()
	client, err := NewRiverClient(pool, workerBundle("rollback"))
	if err != nil {
		t.Fatal(err)
	}
	err = RunTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO plants (id, workspace_id, name, type, category)
			 VALUES (gen_random_uuid(), gen_random_uuid(), 'ghost', 'INDIVIDUAL', 'OTHER')`); err != nil {
			return err
		}
		if _, err := client.InsertTx(ctx, tx, noopTestArgs{Note: "ghost"}, nil); err != nil {
			return err
		}
		return context.Canceled // force rollback through the seam
	})
	if err == nil {
		t.Fatal("expected the tx to fail")
	}
	if n := countRows(t, pool, "plants"); n != 0 {
		t.Fatalf("rollback left %d plants", n)
	}
	if n := countRows(t, pool, "river_job"); n != 0 {
		t.Fatalf("rollback left %d jobs", n)
	}
}

func TestCommitEnqueuesAndWorkerProcesses(t *testing.T) {
	pool := setupRiverDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan string, 4)
	workers := river.NewWorkers()
	river.AddWorker(workers, &signalWorker{done: done})
	client, err := NewRiverClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := RunTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := client.InsertTx(ctx, tx, noopTestArgs{Note: "committed"}, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case note := <-done:
		if note != "committed" {
			t.Fatalf("worker saw %q", note)
		}
	case <-ctx.Done():
		t.Fatal("job never processed")
	}
	// job row ends completed
	for {
		var state string
		if err := pool.QueryRow(ctx, "SELECT state FROM river_job LIMIT 1").Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "completed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("job state: %s", state)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// Restart recovery: a job committed by a previous process is picked up by a
// fresh client (simulates crash/restart between enqueue and execution).
func TestRestartRecovery(t *testing.T) {
	pool := setupRiverDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	insertOnly, err := NewRiverClient(pool, workerBundle("after-restart-discard"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertOnly.Insert(ctx, noopTestArgs{Note: "after-restart"}, nil); err != nil {
		t.Fatal(err)
	}

	done := make(chan string, 1)
	workers := river.NewWorkers()
	river.AddWorker(workers, &signalWorker{done: done})
	client, err := NewRiverClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case note := <-done:
		if note != "after-restart" {
			t.Fatalf("worker saw %q", note)
		}
	case <-ctx.Done():
		t.Fatal("job not recovered after restart")
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
