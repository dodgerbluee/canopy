package main

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	candb "canopy/server/db"
	"canopy/server/internal/platform"
)

func main() {
	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "up", "down", "status", "version":
	default:
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|status|version>")
		os.Exit(2)
	}
	cfg, err := platform.LoadConfig(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := platform.NewLogger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	sub, err := fs.Sub(candb.Migrations, "migrations")
	if err != nil {
		log.Error("embed", "err", err)
		os.Exit(1)
	}
	// Lane-reserved migration numbers (docs/adr/0002-data.md) are backfilled
	// in numeric order: e.g. 00004 (AI lane) lands after 00005-00007 were
	// already applied on long-lived databases. Goose halts on gaps by default,
	// so the provider is built with out-of-order application enabled; out-of-
	// order migrations still run in numeric order relative to each other.
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub,
		goose.WithAllowOutofOrder(true),
	)
	if err != nil {
		log.Error("provider", "err", err)
		os.Exit(1)
	}
	defer provider.Close()

	switch cmd {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			log.Error("migration failed", "cmd", cmd, "err", err)
			os.Exit(1)
		}
		for _, r := range results {
			log.Info("applied", "version", r.Source.Version, "name", r.Source.Path, "duration", r.Duration)
		}
	case "down":
		r, err := provider.Down(ctx)
		if err != nil {
			log.Error("migration failed", "cmd", cmd, "err", err)
			os.Exit(1)
		}
		if r != nil {
			log.Info("reverted", "version", r.Source.Version, "name", r.Source.Path)
		}
	case "status":
		states, err := provider.Status(ctx)
		if err != nil {
			log.Error("status", "err", err)
			os.Exit(1)
		}
		for _, s := range states {
			if s.State == goose.StateApplied {
				fmt.Printf("applied  %d %s (%s)\n", s.Source.Version, s.Source.Path, s.AppliedAt)
			} else {
				fmt.Printf("%-8s %d %s\n", s.State, s.Source.Version, s.Source.Path)
			}
		}
	case "version":
		current, target, err := provider.GetVersions(ctx)
		if err != nil {
			log.Error("version", "err", err)
			os.Exit(1)
		}
		fmt.Printf("current: %d  target: %d\n", current, target)
	}
}
