// Sources (T21): owner-entered knowledge with append-only versions.
// "Owner-entered without any scraping service" — every write here is
// explicit owner content; nothing fetches URLs.
package care

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

var sourceKindSet = map[string]bool{"OWNER_NOTE": true, "BOOK": true, "ARTICLE": true, "EXTENSION_PUBLICATION": true, "OTHER": true}
var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SourceView is the domain read shape; T24 maps it onto the GraphQL types.
type SourceView struct {
	ID        string
	Kind      string
	Title     string
	Publisher *string
	URL       *string
	Region    *string
	License   *string
	RetiredAt *string
	Revision  int32
	CreatedAt string
	UpdatedAt string
	Versions  []SourceVersionView
}

type SourceVersionView struct {
	ID            string
	SourceID      string
	Version       int32
	Citation      string
	Summary       string
	RetrievedOn   *string
	ContentSha256 *string
	CreatedAt     string
}

// CreateSourceInput: the first version is part of creation (the SDL input
// carries citation/summary directly).
type CreateSourceInput struct {
	Kind          string
	Title         string
	Publisher     *string
	URL           *string
	Region        *string
	License       *string
	Citation      string
	Summary       string
	RetrievedOn   *string
	ContentSha256 *string
}

type UpdateSourceInput struct {
	ID               string
	ExpectedRevision int32
	Title            *string
	Publisher        *string
	URL              *string
	Region           *string
	License          *string
	Citation         string
	Summary          string
	RetrievedOn      *string
	ContentSha256    *string
}

// validateSourceContent checks the text limits shared by create/update
// (mirrors the DB CHECKs so problems quote field paths, not constraint names).
// The aggregate kind is immutable, so update skips that check.
func validateSourceContent(in CreateSourceInput, field string, requireKind bool) []Problem {
	var probs []Problem
	bad := func(f, msg string) { probs = append(probs, problem(CodeValidation, f, msg)) }
	if requireKind && !sourceKindSet[in.Kind] {
		bad(field+".kind", "expected OWNER_NOTE, BOOK, ARTICLE, EXTENSION_PUBLICATION or OTHER, found "+quote(in.Kind))
	}
	if charLen(in.Title) < 1 || charLen(in.Title) > 500 {
		bad(field+".title", "expected 1..500 characters")
	}
	for _, p := range []struct {
		f, v string
		max  int
	}{
		{field + ".publisher", textPtr(in.Publisher), 300},
		{field + ".url", textPtr(in.URL), 2000},
		{field + ".region", textPtr(in.Region), 200},
		{field + ".license", textPtr(in.License), 500},
	} {
		if p.v != "" && charLen(p.v) > p.max {
			bad(p.f, fmt.Sprintf("expected at most %d characters", p.max))
		}
	}
	if charLen(in.Citation) < 1 || charLen(in.Citation) > 1000 {
		bad(field+".citation", "expected 1..1000 characters")
	}
	if charLen(in.Summary) < 1 || charLen(in.Summary) > 8000 {
		bad(field+".summary", "expected 1..8000 characters")
	}
	if r := in.RetrievedOn; r != nil && !validDate(*r) {
		bad(field+".retrievedOn", "expected YYYY-MM-DD, found "+quote(*r))
	}
	if s := in.ContentSha256; s != nil && !sha256Re.MatchString(*s) {
		bad(field+".contentSha256", "expected 64 lowercase hex characters")
	}
	return probs
}

func validDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (s *Service) CreateSource(ctx context.Context, a Actor, in CreateSourceInput, cmid *string) (SourceView, []Problem, error) {
	var zero SourceView
	if probs := validateSourceContent(in, "input", true); len(probs) > 0 {
		return zero, probs, nil
	}
	return mutate(s, ctx, a, "createSource", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (SourceView, []Problem) {
			id := s.uuid.New()
			src, err := q.InsertSource(ctx, dbgen.InsertSourceParams{
				ID: id, WorkspaceID: a.WorkspaceID,
				Kind:      dbgen.SourceKind(in.Kind),
				Title:     in.Title,
				Publisher: ptrText(in.Publisher), Url: ptrText(in.URL),
				Region: ptrText(in.Region), License: ptrText(in.License),
			})
			if err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if _, err := q.InsertSourceVersion(ctx, dbgen.InsertSourceVersionParams{
				ID: s.uuid.New(), SourceID: id, WorkspaceID: a.WorkspaceID, Version: 1,
				Citation: in.Citation, Summary: in.Summary,
				RetrievedOn:   ptrDate(in.RetrievedOn),
				ContentSha256: ptrText(in.ContentSha256),
			}); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := s.audit(ctx, q, a, "create", "source", id, src.Revision, nil); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			view, err := s.sourceView(ctx, q, src)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			return view, nil
		})
}

// UpdateSource applies aggregate edits (null keeps existing) and appends a
// new immutable version; old versions remain inspectable forever.
func (s *Service) UpdateSource(ctx context.Context, a Actor, in UpdateSourceInput, cmid *string) (SourceView, []Problem, error) {
	var zero SourceView
	uid, ok := parseID(in.ID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.id", "expected a UUID")}, nil
	}
	createShape := CreateSourceInput{
		Title: textPtr(in.Title), Publisher: in.Publisher, URL: in.URL,
		Region: in.Region, License: in.License,
		Citation: in.Citation, Summary: in.Summary,
		RetrievedOn: in.RetrievedOn, ContentSha256: in.ContentSha256,
	}
	if charLen(createShape.Title) == 0 {
		return zero, []Problem{problem(CodeValidation, "input.title", "expected 1..500 characters")}, nil
	}
	if probs := validateSourceContent(createShape, "input", false); len(probs) > 0 {
		return zero, probs, nil
	}
	return mutate(s, ctx, a, "updateSource", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (SourceView, []Problem) {
			cur, err := q.GetSource(ctx, dbgen.GetSourceParams{ID: uid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeNotFound, "input.id", "no such source in this workspace.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			if cur.RetiredAt.Valid {
				return zero, []Problem{problem(CodeResourceGone, "input.id",
					"source is retired; retire is terminal — create a new source to replace it.")}
			}
			updated, err := q.UpdateSourceAggregate(ctx, dbgen.UpdateSourceAggregateParams{
				ID: uid, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				Title: ptrText(in.Title), Publisher: ptrText(in.Publisher), Url: ptrText(in.URL),
				Region: ptrText(in.Region), License: ptrText(in.License),
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "expectedRevision", "source was modified concurrently."),
						cur.Revision)}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			maxV, err := q.MaxSourceVersionNumber(ctx, uid)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			if _, err := q.InsertSourceVersion(ctx, dbgen.InsertSourceVersionParams{
				ID: s.uuid.New(), SourceID: uid, WorkspaceID: a.WorkspaceID, Version: maxV + 1,
				Citation: in.Citation, Summary: in.Summary,
				RetrievedOn:   ptrDate(in.RetrievedOn),
				ContentSha256: ptrText(in.ContentSha256),
			}); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := s.audit(ctx, q, a, "update", "source", uid, updated.Revision, cur); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			view, err := s.sourceView(ctx, q, updated)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			return view, nil
		})
}

func (s *Service) RetireSource(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (SourceView, []Problem, error) {
	var zero SourceView
	uid, ok := parseID(id)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.id", "expected a UUID")}, nil
	}
	return mutate(s, ctx, a, "retireSource", cmid, map[string]any{"id": id, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries) (SourceView, []Problem) {
			cur, err := q.GetSource(ctx, dbgen.GetSourceParams{ID: uid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeNotFound, "input.id", "no such source in this workspace.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			updated, err := q.RetireSourceRow(ctx, dbgen.RetireSourceRowParams{
				ID: uid, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					if cur.RetiredAt.Valid {
						return zero, []Problem{problemWithRevision(
							problem(CodeConflict, "expectedRevision", "source is already retired."), cur.Revision)}
					}
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "expectedRevision", "source was modified concurrently."), cur.Revision)}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			if err := s.audit(ctx, q, a, "retire", "source", uid, updated.Revision, cur); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			view, err := s.sourceView(ctx, q, updated)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			return view, nil
		})
}

// ---- reads -------------------------------------------------------------

func (s *Service) GetSource(ctx context.Context, a Actor, id string) (SourceView, []Problem, error) {
	uid, ok := parseID(id)
	if !ok {
		return SourceView{}, []Problem{problem(CodeValidation, "id", "expected a UUID")}, nil
	}
	src, err := s.q().GetSource(ctx, dbgen.GetSourceParams{ID: uid, WorkspaceID: a.WorkspaceID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SourceView{}, []Problem{problem(CodeNotFound, "id", "no such source in this workspace.")}, nil
		}
		return SourceView{}, nil, err
	}
	view, err := s.sourceView(ctx, s.q(), src)
	if err != nil {
		return SourceView{}, nil, err
	}
	return view, nil, nil
}

type SourceFilter struct {
	Kind           *string
	IncludeRetired *bool
}

type SourceConnection struct {
	Items      []SourceView
	NextCursor *string
}

func (s *Service) ListSources(ctx context.Context, a Actor, f SourceFilter, first int32, after *string) (SourceConnection, []Problem, error) {
	if first < 1 || first > 100 {
		return SourceConnection{}, []Problem{problem(CodeValidation, "first", "expected 1..100")}, nil
	}
	afterTS, afterID, probs := decodeDateCursor(after)
	if len(probs) > 0 {
		return SourceConnection{}, probs, nil
	}
	if f.Kind != nil && !sourceKindSet[*f.Kind] {
		return SourceConnection{}, []Problem{problem(CodeValidation, "filter.kind", "unknown SourceKind")}, nil
	}
	rows, err := s.q().ListSources(ctx, dbgen.ListSourcesParams{
		WorkspaceID: a.WorkspaceID, Kind: ptrText(f.Kind),
		IncludeRetired: f.IncludeRetired, AfterCreatedAt: afterTS, AfterID: afterID,
		LimitCount: first + 1,
	})
	if err != nil {
		return SourceConnection{}, nil, err
	}
	hasMore := len(rows) > int(first)
	if hasMore {
		rows = rows[:first]
	}
	out := SourceConnection{Items: make([]SourceView, 0, len(rows))}
	for _, r := range rows {
		v, err := s.sourceView(ctx, s.q(), r)
		if err != nil {
			return SourceConnection{}, nil, err
		}
		out.Items = append(out.Items, v)
	}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeDateCursor(last.CreatedAt, uuidString(last.ID))
		out.NextCursor = &c
	}
	return out, nil, nil
}

// sourceView assembles the aggregate with its versions (newest first).
func (s *Service) sourceView(ctx context.Context, q *dbgen.Queries, src dbgen.Source) (SourceView, error) {
	vers, err := q.ListSourceVersions(ctx, src.ID)
	if err != nil {
		return SourceView{}, err
	}
	v := SourceView{
		ID: uuidString(src.ID), Kind: string(src.Kind), Title: src.Title,
		Publisher: optText(src.Publisher), URL: optText(src.Url),
		Region: optText(src.Region), License: optText(src.License),
		RetiredAt: optTS(src.RetiredAt), Revision: src.Revision,
		CreatedAt: stamp(src.CreatedAt), UpdatedAt: stamp(src.UpdatedAt),
		Versions: make([]SourceVersionView, 0, len(vers)),
	}
	for _, sv := range vers {
		v.Versions = append(v.Versions, SourceVersionView{
			ID: uuidString(sv.ID), SourceID: uuidString(sv.SourceID), Version: sv.Version,
			Citation: sv.Citation, Summary: sv.Summary,
			RetrievedOn:   optDate(sv.RetrievedOn),
			ContentSha256: optText(sv.ContentSha256),
			CreatedAt:     stamp(sv.CreatedAt),
		})
	}
	return v, nil
}

// ---- cursor + scalar helpers (local to this package) --------------------

func encodeDateCursor(t pgtype.Timestamptz, id string) string {
	return base64.StdEncoding.EncodeToString([]byte(t.Time.Format(time.RFC3339Nano) + "|" + id))
}

func decodeDateCursor(cur *string) (pgtype.Timestamptz, pgtype.UUID, []Problem) {
	if cur == nil || *cur == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(*cur)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "invalid cursor")}
	}
	ts, id, found := strings.Cut(string(raw), "|")
	if !found {
		return pgtype.Timestamptz{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "invalid cursor")}
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "invalid cursor")}
	}
	uid, ok := parseID(id)
	if !ok {
		return pgtype.Timestamptz{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "invalid cursor")}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, uid, nil
}

func ptrText(s *string) pgtype.Text {
	if s == nil || *s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func optText(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func ptrDate(s *string) pgtype.Date {
	if s == nil || *s == "" {
		return pgtype.Date{}
	}
	var d pgtype.Date
	_ = d.Scan(*s) // validated upstream
	return d
}

func optDate(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	v := d.Time.Format("2006-01-02")
	return &v
}

func optTS(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	v := t.Time.Format(time.RFC3339Nano)
	return &v
}

func stamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format(time.RFC3339Nano)
}

// dbProblem maps unexpected DB errors to a validation problem carrying the
// constraint detail (constraint violations here always mean input shape the
// service checks missed — never a success-path error).
func dbProblem(field string, err error) Problem {
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return problem(CodeValidation, field, "rejected by data contract: "+msg)
}
