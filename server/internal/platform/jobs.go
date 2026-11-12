package platform

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// RiverClient is the composition-root seam for background jobs. Feature
// packages register workers and insert through the same pgx.Tx as their
// writes, which makes job enqueue atomic with the data it reflects.
type RiverClient = river.Client[pgx.Tx]

func NewRiverClient(pool *pgxpool.Pool, workers *river.Workers, periodic ...*river.PeriodicJob) (*RiverClient, error) {
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:       map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 8}},
		Workers:      workers,
		PeriodicJobs: periodic,
	})
	if err != nil {
		return nil, fmt.Errorf("river: %w", err)
	}
	return c, nil
}

// NewInsertOnlyRiverClient wires a client that may enqueue jobs but never
// executes them: the API process inserts on the same tx as its writes,
// the worker process runs them. Nil Queues/Workers is river's insert-only
// shape — it also skips the registered-kind check, so the API can enqueue
// kinds whose workers live in the other binary.
func NewInsertOnlyRiverClient(pool *pgxpool.Pool) (*RiverClient, error) {
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("river: %w", err)
	}
	return c, nil
}

// RunTx runs fn inside one transaction; fn enqueues with InsertTx so a
// rollback leaves neither state nor job.
func RunTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// MigrateRiver installs/updates River's own tables in the active schema.
func MigrateRiver(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	_, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}
