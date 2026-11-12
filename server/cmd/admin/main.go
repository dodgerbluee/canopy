// Command admin is the audited local operator CLI: owner lifecycle
// (bootstrap and recovery) plus the portability operations (backup and
// restore-test). There is no public signup or reset endpoint, so this is
// the only path an owner is created or a password is forced. Passwords
// are prompted interactively or read from a mounted secret file —
// never argv, never echoed, never logged. Backup keys are file paths,
// never key material on the command line.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"canopy/server/internal/identity"
	"canopy/server/internal/platform"
	"canopy/server/internal/portability"
)

const usage = `usage:
  admin owner create  [--workspace NAME] [--timezone TZ] [--currency CUR] [--password-file PATH]
  admin owner recover [--password-file PATH]
  admin backup   [--dest DIR] [--key-file PATH]
  admin restore  --archive PATH [--ledger PATH] [--key-file PATH]

Passwords are prompted interactively or read from a file (mounted secret);
they are never accepted as command-line arguments. backup quiesces the
stack (maintenance flag + queue drain) for the snapshot and always
reopens it afterwards. restore only ever applies a set to a blank host.`

func main() {
	args := os.Args[1:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "owner":
		runOwner(args[1:])
	case "backup":
		runBackup(args[1:])
	case "restore":
		runRestore(args[1:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func runOwner(args []string) {
	if len(args) < 1 || (args[0] != "create" && args[0] != "recover") {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	verb := args[0]

	fs := flag.NewFlagSet("owner "+verb, flag.ExitOnError)
	passwordFile := fs.String("password-file", "", "read the password from this file (mounted secret)")
	workspace := fs.String("workspace", "My garden", "workspace display name (create)")
	timezone := fs.String("timezone", "UTC", "workspace timezone (create)")
	currency := fs.String("currency", "USD", "workspace default currency (create)")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(2)
	}

	cfg, log, pool := connect()
	defer pool.Close()

	q := identity.NewQueries(pool)
	ids := platform.UUIDv4{}
	sessions := identity.NewSessionManager(q, ids, platform.SystemClock{},
		identity.DefaultSessionConfig(cfg.CookieSecure, cfg.TrustProxyProto))
	owners := identity.NewOwnerService(q, sessions, identity.NewPasswordHasher(identity.ProductionParams), ids, log)

	ctx := context.Background()
	switch verb {
	case "create":
		pw, err := readPassword(*passwordFile, "Owner password: ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "admin:", err)
			os.Exit(1)
		}
		owner, ws, err := owners.Bootstrap(ctx, identity.BootstrapInput{
			Password: pw, WorkspaceName: *workspace, Timezone: *timezone, Currency: *currency,
		})
		if errors.Is(err, identity.ErrOwnerExists) {
			fmt.Fprintln(os.Stderr, "an owner already exists; use `admin owner recover` to force a new password")
			os.Exit(1)
		}
		if errors.Is(err, identity.ErrInvalidPassword) {
			fmt.Fprintln(os.Stderr, "password rejected (empty or too long)")
			os.Exit(1)
		}
		if err != nil {
			log.Error("bootstrap failed", "err", err)
			os.Exit(1)
		}
		fmt.Printf("owner %s created; workspace %s\n", uuidStr(owner.ID), uuidStr(ws.ID))
	case "recover":
		pw, err := readPassword(*passwordFile, "New owner password: ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "admin:", err)
			os.Exit(1)
		}
		owner, err := owners.Recover(ctx, pw)
		if errors.Is(err, identity.ErrNoOwner) {
			fmt.Fprintln(os.Stderr, "no owner configured; use `admin owner create`")
			os.Exit(1)
		}
		if errors.Is(err, identity.ErrInvalidPassword) {
			fmt.Fprintln(os.Stderr, "password rejected (empty or too long)")
			os.Exit(1)
		}
		if err != nil {
			log.Error("recovery failed", "err", err)
			os.Exit(1)
		}
		fmt.Printf("owner %s recovered; all sessions revoked\n", uuidStr(owner.ID))
	}
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dest := fs.String("dest", "", "directory for the backup set (default: $CANOPY_STORAGE_ROOT/backups)")
	keyFile := fs.String("key-file", "", "optional operator key file (64 hex chars) sealing archive + ledger")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	cfg, log, pool := connect()
	defer pool.Close()

	dir := *dest
	if dir == "" {
		dir = cfg.StorageRoot + "/backups"
	}
	queue, err := platform.NewInsertOnlyRiverClient(pool)
	if err != nil {
		log.Error("river client", "err", err)
		os.Exit(1)
	}
	svc := portability.New(pool, log, cfg.StorageRoot, queue)
	svc.DSN = cfg.DatabaseURL

	sum, err := svc.Backup(context.Background(), portability.BackupOptions{Dest: dir, KeyFile: *keyFile})
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup failed:", err)
		os.Exit(1)
	}
	fmt.Printf("backup set %s (%s)\n  archive:  %s\n  ledger:   %s\n  manifest: %s\n"+
		"  tables=%d rows=%d mediaFiles=%d sealed=%v duration=%s\n",
		sum.SetID, sealLabel(sum.Sealed), sum.Archive, sum.Ledger, sum.Manifest,
		sum.Tables, sum.Rows, sum.MediaFiles, sum.Sealed, sum.Duration)
}

func runRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	archive := fs.String("archive", "", "backup archive file (.tar or .tar.enc)")
	ledger := fs.String("ledger", "", "deletion ledger file for this set (re-applies tombstones + gone-flags)")
	keyFile := fs.String("key-file", "", "operator key file for sealed sets")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *archive == "" {
		fmt.Fprintln(os.Stderr, "restore: --archive is required")
		os.Exit(2)
	}
	cfg, log, pool := connect()
	defer pool.Close()

	svc := portability.New(pool, log, cfg.StorageRoot, nil)
	svc.DSN = cfg.DatabaseURL

	sum, err := svc.Restore(context.Background(), portability.RestoreOptions{
		Archive: *archive, Ledger: *ledger, KeyFile: *keyFile,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "restore failed:", err)
		os.Exit(1)
	}
	fmt.Printf("restored set %s: tables=%d rows=%d mediaFiles=%d ledgerRows=%d duration=%s\n",
		sum.SetID, sum.Tables, sum.Rows, sum.MediaFiles, sum.LedgerRows, sum.Duration)
}

func connect() (platform.Config, *slog.Logger, *pgxpool.Pool) {
	cfg, err := platform.LoadConfig(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := platform.NewLogger(cfg)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database unavailable", "err", err)
		os.Exit(1)
	}
	if err := pool.Ping(ctx); err != nil {
		log.Error("database unavailable", "err", err)
		os.Exit(1)
	}
	return cfg, log, pool
}

func sealLabel(sealed bool) string {
	if sealed {
		return "sealed"
	}
	return "plain"
}

func readPassword(file, prompt string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("password file: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("no terminal available; pass --password-file pointing at a mounted secret")
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(b), nil
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
