package collection

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	dbgen "canopy/server/db/generated"
)

// TagResult identifies the affected tag aggregate.
type TagResult struct {
	ID       string `json:"id"`
	Revision int32  `json:"revision"`
}

// CreateTag inserts a workspace tag; labels are unique case-insensitively.
func (s *Service) CreateTag(ctx context.Context, a Actor, label string, cmid *string) (TagResult, []Problem, error) {
	in := map[string]string{"label": label}
	return mutate[TagResult](s, ctx, a, "createTag", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (TagResult, []Problem) {
			norm := strings.TrimSpace(label)
			if norm == "" || len(norm) > 40 {
				return TagResult{}, []Problem{problem(CodeValidation, "input.label", "label must be 1-40 characters")}
			}
			if _, err := q.GetTagByLabel(ctx, dbgen.GetTagByLabelParams{
				WorkspaceID: a.WorkspaceID, Label: norm,
			}); err == nil {
				return TagResult{}, []Problem{problem(CodeConflict, "input.label", "label already in use")}
			} else if !notFoundErr(err) {
				s.Log.Error("tag label lookup", "err", err)
				return TagResult{}, []Problem{problem(CodeValidation, "", "tag lookup failed")}
			}
			row, err := q.InsertTag(ctx, dbgen.InsertTagParams{
				ID:          s.uuid.New(),
				WorkspaceID: a.WorkspaceID,
				Label:       norm,
			})
			if err != nil {
				if isUniqueViolation(err) {
					return TagResult{}, []Problem{problem(CodeConflict, "input.label", "label already in use")}
				}
				s.Log.Error("createTag insert", "err", err)
				return TagResult{}, []Problem{problem(CodeValidation, "input.label", "tag could not be stored")}
			}
			if err := s.audit(ctx, q, a, "createTag", "tag", row.ID, row.Revision, nil); err != nil {
				return TagResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return TagResult{ID: uuidString(row.ID), Revision: row.Revision}, nil
		})
}

// UpdateTag renames a tag under a revision guard; the new label must not
// collide with a different tag.
func (s *Service) UpdateTag(ctx context.Context, a Actor, id string, expectedRevision int32, label string, cmid *string) (TagResult, []Problem, error) {
	in := map[string]any{"id": id, "expectedRevision": expectedRevision, "label": label}
	return mutate[TagResult](s, ctx, a, "updateTag", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (TagResult, []Problem) {
			uid, ok := parseUUID(id)
			if !ok {
				return TagResult{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}
			}
			cur, err := q.GetTag(ctx, dbgen.GetTagParams{ID: uid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return TagResult{}, notFound("input.id", "tag not found")
			}
			norm := strings.TrimSpace(label)
			if norm == "" || len(norm) > 40 {
				return TagResult{}, []Problem{problem(CodeValidation, "input.label", "label must be 1-40 characters")}
			}
			if other, err := q.GetTagByLabel(ctx, dbgen.GetTagByLabelParams{
				WorkspaceID: a.WorkspaceID, Label: norm,
			}); err == nil && other.ID != uid {
				return TagResult{}, []Problem{problem(CodeConflict, "input.label", "label already in use")}
			}
			updated, err := q.UpdateTagLabel(ctx, dbgen.UpdateTagLabelParams{
				ID: uid, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
				Label: norm,
			})
			if err != nil {
				if isUniqueViolation(err) {
					return TagResult{}, []Problem{problem(CodeConflict, "input.label", "label already in use")}
				}
				return TagResult{}, s.conflictProblem(ctx, q, "tags", uid, a.WorkspaceID, err)
			}
			if err := s.audit(ctx, q, a, "updateTag", "tag", uid, updated.Revision, cur); err != nil {
				return TagResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return TagResult{ID: uuidString(uid), Revision: updated.Revision}, nil
		})
}

// ArchiveTag soft-archives a tag; archived tags stay attached to plants but
// cannot be newly assigned.
func (s *Service) ArchiveTag(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (TagResult, []Problem, error) {
	in := map[string]any{"id": id, "expectedRevision": expectedRevision}
	return mutate[TagResult](s, ctx, a, "archiveTag", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (TagResult, []Problem) {
			uid, ok := parseUUID(id)
			if !ok {
				return TagResult{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}
			}
			cur, err := q.GetTag(ctx, dbgen.GetTagParams{ID: uid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return TagResult{}, notFound("input.id", "tag not found")
			}
			if cur.ArchivedAt.Valid {
				return TagResult{}, []Problem{problem(CodeConflict, "input.id", "tag is already archived")}
			}
			updated, err := q.ArchiveTag(ctx, dbgen.ArchiveTagParams{
				ID: uid, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			})
			if err != nil {
				return TagResult{}, s.conflictProblem(ctx, q, "tags", uid, a.WorkspaceID, err)
			}
			if err := s.audit(ctx, q, a, "archiveTag", "tag", uid, updated.Revision, cur); err != nil {
				return TagResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return TagResult{ID: uuidString(uid), Revision: updated.Revision}, nil
		})
}

// ensure pgx import stays used even if error paths change.
var _ = pgx.ErrNoRows
