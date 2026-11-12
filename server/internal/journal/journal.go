// Package journal — the unified timeline projection (03: journal_entries).
// Projection rows reference canonical entities and are written in the same
// transaction as the entity change; they are never the source of truth.
//
// Ownership increments (07-tasks): T16 ships the photo projection and the
// photo-series read; T17 adds activity/measurement commands and their
// projection; T18 extends the read model (remaining union members,
// search). Union members not yet mapped by the resolver never have rows
// here, so pages stay honest in the meantime.
package journal

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

const (
	defaultPageSize int32 = 25
	maxPageSize     int32 = 100
	// summaryMax mirrors the DB CHECK char_length(summary) <= 280.
	summaryMax = 280
)

// ErrInvalidCursor is returned when an `after` value is malformed or was
// issued under a different filter signature.
var ErrInvalidCursor = errors.New("invalid journal pagination cursor")

// ---- projection writes (called inside the entity mutation's tx) ----------

// PhotoSummary builds the safe timeline summary for a photo entry: the
// owner's own caption (truncated) or a viewpoint label. Never derived from
// file contents, EXIF or storage keys.
func PhotoSummary(caption pgtype.Text, viewpoint dbgen.PhotoViewpoint) string {
	if caption.Valid {
		if s := strings.TrimSpace(caption.String); s != "" {
			return truncateRunes(s, summaryMax)
		}
	}
	switch viewpoint {
	case dbgen.PhotoViewpointWHOLE:
		return "Photo of whole plant"
	case dbgen.PhotoViewpointLEAF:
		return "Photo of leaf"
	case dbgen.PhotoViewpointFRUIT:
		return "Photo of fruit"
	default:
		return "Photo"
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ProjectActivity upserts the ACTIVITY projection row for r (reviving a
// tombstone on re-projection, per the shared conflict target). The care
// service logs activities through completion mutations and needs the same
// projection journal.Service applies to its own writes.
func ProjectActivity(ctx context.Context, q *dbgen.Queries, entryID pgtype.UUID, r dbgen.Activity) error {
	var unit, product, note *string
	if r.Unit.Valid {
		u := string(r.Unit.MeasurementUnit)
		unit = &u
	}
	if r.Product.Valid {
		product = &r.Product.String
	}
	if r.Note.Valid {
		note = &r.Note.String
	}
	_, err := q.UpsertJournalEntry(ctx, dbgen.UpsertJournalEntryParams{
		ID:          entryID,
		WorkspaceID: r.WorkspaceID,
		PlantID:     r.PlantID,
		EntryType:   dbgen.JournalEntryTypeACTIVITY,
		SourceID:    r.ID,
		OccurredAt:  r.OccurredAt,
		Summary:     ActivitySummary(string(r.Kind), r.Quantity, unit, product, note),
	})
	return err
}

// TombstoneActivity marks an activity's projection row tombstoned.
func TombstoneActivity(ctx context.Context, q *dbgen.Queries, ws, activityID pgtype.UUID) error {
	_, err := q.TombstoneJournalEntry(ctx, dbgen.TombstoneJournalEntryParams{
		WorkspaceID: ws, EntryType: dbgen.JournalEntryTypeACTIVITY, SourceID: activityID,
	})
	return err
}

// ProjectPhoto upserts the PHOTO projection row. The capture-date
// correction path relies on this being the same statement's conflict
// target: one entry per (workspace, PHOTO, photo, plant), always current.
func ProjectPhoto(ctx context.Context, q *dbgen.Queries, entryID, ws, plantID, photoID pgtype.UUID,
	occurredAt pgtype.Timestamptz, summary string) error {
	_, err := q.UpsertJournalEntry(ctx, dbgen.UpsertJournalEntryParams{
		ID:          entryID,
		WorkspaceID: ws,
		PlantID:     plantID,
		EntryType:   dbgen.JournalEntryTypePHOTO,
		SourceID:    photoID,
		OccurredAt:  occurredAt,
		Summary:     summary,
	})
	return err
}

// TombstonePhoto marks the photo's projection row tombstoned. The entry
// stays queryable with includeTombstoned; the photo row itself is only
// soft-deleted, so the tombstone remains fully attributable.
func TombstonePhoto(ctx context.Context, q *dbgen.Queries, ws, photoID pgtype.UUID) error {
	_, err := q.TombstoneJournalEntry(ctx, dbgen.TombstoneJournalEntryParams{
		WorkspaceID: ws,
		EntryType:   dbgen.JournalEntryTypePHOTO,
		SourceID:    photoID,
	})
	return err
}

// ---- read model -------------------------------------------------------------

// Query is the transport-neutral journal page request. Default order is
// (occurredAt DESC, id DESC), matching the frozen contract. EntryTypes
// carries the frozen enum names ("PHOTO", ...); empty = all types.
type Query struct {
	PlantID           *pgtype.UUID // nil = workspace-wide
	EntryTypes        []string
	From              *time.Time
	To                *time.Time
	IncludeTombstoned bool
	First             int32
	After             *string
}

type EntryView struct {
	ID           string     `json:"id"`
	PlantID      string     `json:"plantId"`
	EntryType    string     `json:"entryType"`
	SourceID     string     `json:"sourceId"`
	OccurredAt   time.Time  `json:"occurredAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	Summary      string     `json:"summary"`
	TombstonedAt *time.Time `json:"tombstonedAt,omitempty"`
}

type Page struct {
	Items     []EntryView
	HasNext   bool
	EndCursor string
}

type Service struct {
	pool  *pgxpool.Pool
	q     *dbgen.Queries
	uuid  platform.UUIDv4
	clock platform.SystemClock
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:  pool,
		q:     dbgen.New(pool),
		uuid:  platform.UUIDv4{},
		clock: platform.SystemClock{},
	}
}

func (s *Service) List(ctx context.Context, ws pgtype.UUID, qz Query) (Page, error) {
	first := clampFirst(qz.First)
	sig := filterSignature(qz)

	params := dbgen.ListJournalParams{
		WorkspaceID:       ws,
		EntryTypes:        qz.EntryTypes,
		IncludeTombstoned: qz.IncludeTombstoned,
		LimitCount:        first + 1, // one extra row answers hasNextPage honestly
	}
	if qz.PlantID != nil {
		params.PlantID = *qz.PlantID
	}
	if qz.From != nil {
		params.FromAt = pgtype.Timestamptz{Time: *qz.From, Valid: true}
	}
	if qz.To != nil {
		params.ToAt = pgtype.Timestamptz{Time: *qz.To, Valid: true}
	}
	if qz.After != nil {
		occ, id, err := decodeCursor(*qz.After, sig)
		if err != nil {
			return Page{}, err
		}
		params.AfterOccurredAt = pgtype.Timestamptz{Time: occ, Valid: true}
		params.AfterID = id
	}

	rows, err := s.q.ListJournal(ctx, params)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]EntryView, 0, first)}
	if int32(len(rows)) > first {
		page.HasNext = true
		rows = rows[:first]
	}
	for _, r := range rows {
		page.Items = append(page.Items, entryView(r))
	}
	if len(rows) > 0 {
		last := page.Items[len(page.Items)-1]
		page.EndCursor = encodeCursor(sig, last.OccurredAt, last.ID)
	}
	return page, nil
}

func entryView(r dbgen.JournalEntry) EntryView {
	v := EntryView{
		ID:        uuidString(r.ID),
		PlantID:   uuidString(r.PlantID),
		EntryType: string(r.EntryType),
		SourceID:  uuidString(r.SourceID),
		Summary:   r.Summary,
	}
	if r.OccurredAt.Valid {
		v.OccurredAt = r.OccurredAt.Time
	}
	if r.CreatedAt.Valid {
		v.CreatedAt = r.CreatedAt.Time
	}
	if r.TombstonedAt.Valid {
		t := r.TombstonedAt.Time
		v.TombstonedAt = &t
	}
	return v
}

func clampFirst(first int32) int32 {
	if first <= 0 {
		return defaultPageSize
	}
	if first > maxPageSize {
		return maxPageSize
	}
	return first
}

// ---- cursor binding -----------------------------------------------------------

// filterSignature binds a cursor to the filter that issued it: a series
// page cannot be resumed under a different plant, type set, window or
// tombstone setting. Entry types are sorted so list order never changes
// the signature.
func filterSignature(qz Query) string {
	var b strings.Builder
	if qz.PlantID != nil {
		b.WriteString(uuidString(*qz.PlantID))
	}
	b.WriteByte('|')
	types := append([]string(nil), qz.EntryTypes...)
	sort.Strings(types)
	b.WriteString(strings.Join(types, ","))
	b.WriteByte('|')
	b.WriteString(tsTag(qz.From))
	b.WriteByte('|')
	b.WriteString(tsTag(qz.To))
	b.WriteByte('|')
	if qz.IncludeTombstoned {
		b.WriteByte('1')
	} else {
		b.WriteByte('0')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func tsTag(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func encodeCursor(sig string, occurredAt time.Time, id string) string {
	payload := sig + "|" + occurredAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeCursor(cur, sig string) (time.Time, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, ErrInvalidCursor
	}
	s := string(raw)
	i := strings.IndexByte(s, '|')
	j := strings.LastIndexByte(s, '|')
	if i < 0 || j <= i || s[:i] != sig {
		return time.Time{}, pgtype.UUID{}, ErrInvalidCursor
	}
	occ, err := time.Parse(time.RFC3339Nano, s[i+1:j])
	if err != nil {
		return time.Time{}, pgtype.UUID{}, ErrInvalidCursor
	}
	id, ok := parseUUID(s[j+1:])
	if !ok {
		return time.Time{}, pgtype.UUID{}, ErrInvalidCursor
	}
	return occ.UTC(), id, nil
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 36)
	k := 0
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[k] = '-'
			k++
		}
		out[k] = hexDigits[v>>4]
		out[k+1] = hexDigits[v&0x0f]
		k += 2
	}
	return string(out)
}

func parseUUID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}
