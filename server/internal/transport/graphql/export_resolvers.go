// Export resolvers (T38.a): requestExport starts a workspace export
// job, exportJob reads its status. Both are owner-scoped through the
// session principal; without a wired portability service they answer
// honestly with CAPABILITY_UNAVAILABLE, never fake job state.
package graphql

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/transport/graphql/gql"
)

func (q queryResolver) ExportJob(ctx context.Context, id string) (*gql.ExportJob, error) {
	if q.r.Port == nil {
		return nil, capabilityUnavailable("exportJob")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	job, err := q.r.Port.GetExportJob(ctx, id, p.WorkspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // unknown or other-workspace ids are simply absent
		}
		return nil, internalError(ctx, err)
	}
	return exportJobOut(job), nil
}

func (m mutationResolver) RequestExport(ctx context.Context, in gql.RequestExportInput, c *string) (*gql.RequestExportPayload, error) {
	if m.r.Port == nil {
		return nil, capabilityUnavailable("requestExport")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	job, err := m.r.Port.RequestExport(ctx, p.WorkspaceID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return &gql.RequestExportPayload{
		ExportJob:        exportJobOut(job),
		Problems:         []*gql.MutationProblem{},
		ClientMutationID: c,
	}, nil
}

func exportJobOut(j dbgen.ExportJob) *gql.ExportJob {
	return &gql.ExportJob{
		ID:             uuidString(j.ID),
		State:          gql.ExportState(j.State),
		SnapshotAt:     dtOut(j.SnapshotAt),
		CreatedAt:      dtOut(j.CreatedAt),
		FinishedAt:     dtPtrOut(j.FinishedAt),
		ExpiresAt:      dtPtrOut(j.ExpiresAt),
		ManifestSha256: textPtr(j.ManifestSha256),
		ErrorCode:      textPtr(j.ErrorCode),
	}
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}
