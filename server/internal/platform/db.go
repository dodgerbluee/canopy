package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	pcfg.MaxConns = 10
	pcfg.MaxConnIdleTime = 5 * time.Minute
	pcfg.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
