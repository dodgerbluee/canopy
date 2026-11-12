package portability

import "time"

// ArchiveEntry is one file inside an archive with its checksum. Every
// restore verifies each entry hash before writing anything (05: manifest
// checksums, invalid archive rejected).
type ArchiveEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// FinTotal is a reconciliation anchor: live purchase totals per currency
// must be identical after a clean restore.
type FinTotal struct {
	Currency   string `json:"currency"`
	TotalMinor int64  `json:"totalMinor"`
}

// BackupManifest is written twice: inside the archive (without
// PayloadSha256) and beside it as <set>.manifest.json with
// PayloadSha256 covering the archive file exactly as stored.
type BackupManifest struct {
	SchemaVersion   string           `json:"schemaVersion"`
	BackupSet       string           `json:"backupSet"`
	CreatedAt       time.Time        `json:"createdAt"`
	PrivacyEpoch    int64            `json:"privacyEpoch"`
	Sealed          bool             `json:"sealed"`
	TableCounts     map[string]int64 `json:"tableCounts"`
	FinancialTotals []FinTotal       `json:"financialTotals"`
	AllocatedMinor  int64            `json:"allocationTotalMinor"`
	Tombstones      int64            `json:"tombstones"`
	Entries         []ArchiveEntry   `json:"entries"`
	PayloadSha256   string           `json:"payloadSha256,omitempty"`
	Exclusions      []string         `json:"exclusions"`
	BackupDuration  string           `json:"backupDuration"`
}

// ExportManifest is the user-facing provenance record inside an export
// archive: what was included, what was deliberately excluded, and the
// privacy epoch the snapshot was fenced on.
type ExportManifest struct {
	SchemaVersion string           `json:"schemaVersion"`
	WorkspaceID   string           `json:"workspaceId"`
	GeneratedAt   time.Time        `json:"generatedAt"`
	SnapshotAt    time.Time        `json:"snapshotAt"`
	PrivacyEpoch  int64            `json:"privacyEpoch"`
	Counts        map[string]int64 `json:"counts"`
	Entries       []ArchiveEntry   `json:"entries"`
	MissingMedia  []string         `json:"missingMedia"`
	Exclusions    []string         `json:"exclusions"`
}

// ExportExclusions names what a user export never contains (03: no
// secrets, sessions, idempotency receipts or raw operational logs).
var ExportExclusions = []string{
	"password hashes", "sessions", "csrf tokens", "idempotency receipts",
	"audit prior values", "raw operational logs", "other workspaces",
	"agent credential verifiers", "agent operation receipts",
}

// BackupExclusions names what a backup set never carries.
var BackupExclusions = []string{
	"sessions (invalidated on restore)", "export archives (re-request after restore)",
	"deletion ledger (retained separately and re-applied on restore)",
}
