// Package portability carries the T38.a R1 portability lane: the
// workspace export use case (JSON+CSV+files+checksum manifest, 24h TTL,
// owner-only download, epoch-fenced), the maintenance quiesce flag a
// backup sets while it runs, and the operator backup/restore use cases:
// consistent dump + referenced immutable files + checksum manifest,
// optionally sealed with an operator key held outside the archive, and a
// blank-host restore that re-applies the separately retained deletion
// ledger before access reopens (02-architecture, 03 deletion matrix).
package portability

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"canopy/server/internal/platform"
)

const (
	// SchemaVersion is the export/backup format tag; bumped only with a
	// documented migration story.
	SchemaVersion = "r1"
	// ExportTTL is the operator-policy expiry on user export archives.
	ExportTTL = 24 * time.Hour
	// DrainTimeout bounds how long a backup waits for running jobs.
	DrainTimeout = 60 * time.Second
)

// Service is the portability use-case surface. Export archives live in
// a dedicated exports directory under the ObjectStore root; they are
// never reachable through the media key layout.
type Service struct {
	Pool      *pgxpool.Pool
	Log       *slog.Logger
	StoreRoot string
	Queue     *platform.RiverClient
	DSN       string // target database URL; restore needs it for goose

	uuid  platform.UUIDv4
	clock platform.SystemClock
}

func New(pool *pgxpool.Pool, log *slog.Logger, storeRoot string, queue *platform.RiverClient) *Service {
	return &Service{Pool: pool, Log: log, StoreRoot: storeRoot, Queue: queue,
		uuid: platform.UUIDv4{}, clock: platform.SystemClock{}}
}

// ExportsDir is where built export archives live.
func (s *Service) ExportsDir() string { return filepath.Join(s.StoreRoot, "exports") }

// ExportArchivePath maps an export id to its archive file. The id must
// be canonical lowercase UUID text, so a request can never steer a path.
func ExportArchivePath(exportsDir, id string) (string, error) {
	if !uuidRe.MatchString(id) {
		return "", fmt.Errorf("invalid export id %q", id)
	}
	return filepath.Join(exportsDir, id+".tar"), nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// SafeArchivePath enforces the archive path rules on every tar entry a
// restore reads: relative, cleaned, no traversal, and inside one of the
// four known layout positions. Media keys must be canonical UUIDs, so a
// hostile archive can never write outside the storage root.
func SafeArchivePath(name string) error {
	if name == "" || strings.ContainsRune(name, '\\') {
		return errors.New("archive entry name rejected")
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || path.IsAbs(name) {
		return errors.New("archive entry must be relative")
	}
	if cleaned := path.Clean(name); cleaned != name || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("archive entry must be clean and non-escaping")
	}
	switch {
	case name == "manifest.json":
		return nil
	case strings.HasPrefix(name, "db/"):
		if !tableNameRe.MatchString(name[len("db/"):]) {
			return fmt.Errorf("unknown table entry %q", name)
		}
		return nil
	case strings.HasPrefix(name, "river/"):
		if name != "river/river_job.jsonl" {
			return fmt.Errorf("unknown river entry %q", name)
		}
		return nil
	case strings.HasPrefix(name, "media/"):
		if !uuidRe.MatchString(name[len("media/"):]) {
			return fmt.Errorf("media entry %q is not a storage key", name)
		}
		return nil
	default:
		return fmt.Errorf("unknown archive entry %q", name)
	}
}

var tableNameRe = regexp.MustCompile(`^[a-z_]+\.jsonl$`)

// SafeExportPath enforces the user-export layout on every entry the
// build worker writes: manifest.json, data/<table>.(csv|jsonl) and
// media/<canonical-uuid>. It is deliberately separate from
// SafeArchivePath: the restore whitelist must not grow entries it
// cannot process.
func SafeExportPath(name string) error {
	if name == "" || strings.ContainsRune(name, '\\') {
		return errors.New("export entry name rejected")
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || path.IsAbs(name) {
		return errors.New("export entry must be relative")
	}
	if cleaned := path.Clean(name); cleaned != name || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("export entry must be clean and non-escaping")
	}
	switch {
	case name == "manifest.json":
		return nil
	case strings.HasPrefix(name, "data/"):
		if !exportDataRe.MatchString(name[len("data/"):]) {
			return fmt.Errorf("unknown export data entry %q", name)
		}
		return nil
	case strings.HasPrefix(name, "media/"):
		if !uuidRe.MatchString(name[len("media/"):]) {
			return fmt.Errorf("media entry %q is not a storage key", name)
		}
		return nil
	default:
		return fmt.Errorf("unknown export entry %q", name)
	}
}

var exportDataRe = regexp.MustCompile(`^[a-z_]+\.(csv|jsonl)$`)

// ---- sealing (operator key lives outside the archive) ------------------

var sealMagic = []byte("CANOPYBAK1")

// ErrKeyRequired means a sealed payload was presented without a key.
var ErrKeyRequired = errors.New("archive is sealed; an operator key file is required")

// LoadKey reads a 32-byte AES key encoded as 64 lowercase hex chars.
func LoadKey(file string) ([]byte, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	hexStr := strings.TrimSpace(string(raw))
	key, err := hex.DecodeString(hexStr)
	if err != nil || len(key) != 32 {
		return nil, errors.New("key file must hold 64 hex characters (32 bytes)")
	}
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts a payload with AES-256-GCM and prefixes a magic header
// so a restore can tell sealed from plain without guessing.
func Seal(plain, key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{}, sealMagic...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, nil), nil
}

// OpenSealed verifies the magic header and decrypts.
func OpenSealed(sealed, key []byte) ([]byte, error) {
	if len(sealed) < len(sealMagic) || string(sealed[:len(sealMagic)]) != string(sealMagic) {
		return nil, errors.New("not a sealed payload")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	n := len(sealMagic) + gcm.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed payload truncated")
	}
	return gcm.Open(nil, sealed[len(sealMagic):n], sealed[n:], nil)
}

// IsSealed reports whether a payload carries the seal magic header.
func IsSealed(raw []byte) bool {
	return len(raw) >= len(sealMagic) && string(raw[:len(sealMagic)]) == string(sealMagic)
}

// ---- small shared helpers ---------------------------------------------

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func parseUUID(s string) (pgtype.UUID, bool) {
	if !uuidRe.MatchString(s) {
		return pgtype.UUID{}, false
	}
	var u pgtype.UUID
	u.Valid = true
	h, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(h) != 16 {
		return pgtype.UUID{}, false
	}
	copy(u.Bytes[:], h)
	return u, true
}
