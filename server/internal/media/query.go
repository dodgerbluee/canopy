package media

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// ErrInvalidCursor maps to a top-level VALIDATION transport error.
var ErrInvalidCursor = errors.New("invalid pagination cursor")

// Cursor wire format: base64url("createdDesc|<rfc3339micro UTC>|<uuid>").
// created_at is NOT NULL, so the sort field is always present.
const orderCreatedDesc = "createdDesc"

func encodeCursor(created time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(
		orderCreatedDesc + "|" + created.UTC().Format("2006-01-02T15:04:05.000000Z") + "|" + id))
}

func decodeCursor(cur string) (pgtype.Timestamptz, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, ErrInvalidCursor
	}
	s := string(raw)
	i := strings.IndexByte(s, '|')
	j := strings.LastIndexByte(s, '|')
	if i < 0 || j <= i || s[:i] != orderCreatedDesc {
		return pgtype.Timestamptz{}, pgtype.UUID{}, ErrInvalidCursor
	}
	id, ok := parseUUID(s[j+1:])
	if !ok {
		return pgtype.Timestamptz{}, pgtype.UUID{}, ErrInvalidCursor
	}
	t, terr := time.Parse("2006-01-02T15:04:05.000000Z", s[i+1:j])
	if terr != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, ErrInvalidCursor
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, id, nil
}

// Page is one keyset page of in-flight assets.
type Page struct {
	Items     []AssetView
	EndCursor string
	HasNext   bool
}

// PendingUploads lists RESERVED/UPLOADED/PROCESSING/FAILED assets for
// resume and sweeper visibility, ordered (created_at DESC, id DESC).
func (s *Service) PendingUploads(ctx context.Context, ws pgtype.UUID, first int, after *string) (Page, error) {
	if first <= 0 {
		first = 25
	}
	if first > 100 {
		first = 100
	}
	params := dbgen.ListPendingAssetsParams{
		WorkspaceID: ws,
		LimitCount:  int32(first + 1),
	}
	if after != nil && *after != "" {
		created, id, err := decodeCursor(*after)
		if err != nil {
			return Page{}, err
		}
		params.AfterCreatedAt = created
		params.AfterID = id
	}
	rows, err := s.q().ListPendingAssets(ctx, params)
	if err != nil {
		return Page{}, err
	}
	hasNext := len(rows) > first
	if hasNext {
		rows = rows[:first]
	}
	ids := make([]pgtype.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	variants := map[string][]dbgen.AssetVariant{}
	if len(ids) > 0 {
		vs, err := s.q().ListAssetVariants(ctx, ids)
		if err != nil {
			return Page{}, err
		}
		for _, v := range vs {
			variants[uuidString(v.AssetID)] = append(variants[uuidString(v.AssetID)], v)
		}
	}
	page := Page{Items: make([]AssetView, 0, len(rows)), HasNext: hasNext}
	for _, r := range rows {
		page.Items = append(page.Items, viewOf(r, variants[uuidString(r.ID)]))
	}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		page.EndCursor = encodeCursor(last.CreatedAt.Time, uuidString(last.ID))
	}
	return page, nil
}
