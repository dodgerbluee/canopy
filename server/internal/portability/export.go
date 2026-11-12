package portability

import (
	"canopy/server/internal/agentcontext"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// RequestExport starts (or returns the live) workspace export. At most
// one QUEUED/RUNNING export exists per workspace at a time; a live job
// is returned instead of piling up builds. The job row and the river
// job land in the same transaction.
func (s *Service) RequestExport(ctx context.Context, ws pgtype.UUID) (dbgen.ExportJob, error) {
	var result dbgen.ExportJob
	err := platform.RunTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := agentcontext.Enter(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('export:' || $1::text,0))`, uuidString(ws)); err != nil {
			return err
		}
		qtx := dbgen.New(tx)
		origin, agent := agentcontext.From(ctx)
		if agent {
			var previous string
			err := tx.QueryRow(ctx, `SELECT COALESCE(resource->>'ID',resource->>'id','') FROM agent_operations WHERE grant_id=$1 AND operation_id=$2`, origin.GrantID, origin.OperationID).Scan(&previous)
			if err != nil {
				return err
			}
			if previous != "" {
				id, ok := parseUUID(previous)
				if !ok {
					return errors.New("invalid export operation reference")
				}
				result, err = qtx.GetExportJob(ctx, dbgen.GetExportJobParams{ID: id, WorkspaceID: ws})
				return err
			}
		}
		live, err := qtx.ListLiveExportJobs(ctx, ws)
		if err != nil {
			return err
		}
		if len(live) > 0 {
			result, err = qtx.GetExportJob(ctx, dbgen.GetExportJobParams{ID: live[0], WorkspaceID: ws})
			if err != nil {
				return err
			}
			if agent && (uuidString(result.AgentGrantID) != origin.GrantID || uuidString(result.AgentCredentialID) != origin.CredentialID) {
				return errors.New("another workspace export is in progress; try again later")
			}
			return agentcontext.Complete(ctx, tx, result)
		}
		now := s.clock.Now()
		result, err = qtx.InsertExportJob(ctx, dbgen.InsertExportJobParams{ID: s.uuid.New(), WorkspaceID: ws, SnapshotAt: ts(now), ExpiresAt: ts(now.Add(ExportTTL))})
		if err != nil {
			return err
		}
		if _, err = s.Queue.InsertTx(ctx, tx, ExportBuildArgs{ExportID: uuidString(result.ID)}, nil); err != nil {
			return err
		}
		return agentcontext.Complete(ctx, tx, result)
	})
	return result, err
}

// GetExportJob is the owner-scoped status read.
func (s *Service) GetExportJob(ctx context.Context, id string, ws pgtype.UUID) (dbgen.ExportJob, error) {
	uid, ok := parseUUID(id)
	if !ok {
		return dbgen.ExportJob{}, pgx.ErrNoRows
	}
	return dbgen.New(s.Pool).GetExportJob(ctx, dbgen.GetExportJobParams{ID: uid, WorkspaceID: ws})
}

// ErrExportNotReady reports a download attempt on a job without bytes.
var ErrExportNotReady = errors.New("export has no downloadable archive")
