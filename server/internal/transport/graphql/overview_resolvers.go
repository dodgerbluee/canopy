// Today overview composition (T28): one bounded read for the dashboard —
// due work (workspace-local today-30d..+7d, first 10), the six newest
// journal entries, and the most recent weather snapshot across the
// workspace's locations (null until a location exists and was fetched).
// Page sizes are fixed server-side; the operation takes no client
// arguments so the initial load can never fan out.
package graphql

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/journal"
	"canopy/server/internal/transport/graphql/gql"
)

const (
	overviewDueLookback  = 30
	overviewDueLookahead = 7
	overviewDueFirst     = 10
	overviewEntriesFirst = 6
)

func (q queryResolver) TodayOverview(ctx context.Context) (*gql.TodayOverview, error) {
	if q.r.Care == nil || q.r.Journal == nil {
		return nil, capabilityUnavailable("todayOverview")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	// Due window in the workspace timezone, identical ordering to
	// careAgenda; overdue rows keep their original dates.
	ws, err := q.r.DB.GetWorkspace(ctx, p.OwnerID)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	loc, err := time.LoadLocation(ws.Timezone)
	if err != nil {
		loc = time.UTC
	}
	today := time.Now().In(loc)
	from := today.AddDate(0, 0, -overviewDueLookback).Format("2006-01-02")
	to := today.AddDate(0, 0, overviewDueLookahead).Format("2006-01-02")
	page, probs, err := q.r.Care.CareAgenda(ctx, careActor(p), &from, &to, nil, overviewDueFirst, nil)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return nil, careProblemsError(probs)
	}
	occ := make([]*gql.CareOccurrence, 0, len(page.Items))
	for _, v := range page.Items {
		occ = append(occ, occurrenceOut(v))
	}
	due := &gql.CareOccurrenceConnection{Items: occ, PageInfo: carePageInfo(page.NextCursor)}

	// Newest-first journal lines, same hydration path as the journal
	// query so entries render identically.
	_, recent, err := q.journalPage(ctx, p, journal.Query{First: overviewEntriesFirst})
	if err != nil {
		return nil, err
	}

	// Conditions: latest snapshot across every workspace location;
	// absent location/fetch honestly means null, never a placeholder.
	var conditions *gql.TodayConditions
	row, err := q.r.DB.LatestWorkspaceWeather(ctx, p.WorkspaceID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, internalError(ctx, err)
		}
	} else {
		snap := weatherSnapshotFromRow(row)
		out, err := weatherSnapshotOut(snap)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		conditions = &gql.TodayConditions{Snapshot: out, LocationName: row.LocationName}
	}

	return &gql.TodayOverview{DueTasks: due, RecentEntries: recent, Conditions: conditions}, nil
}

func weatherSnapshotFromRow(r dbgen.LatestWorkspaceWeatherRow) dbgen.WeatherSnapshot {
	return dbgen.WeatherSnapshot{
		ID: r.ID, WorkspaceID: r.WorkspaceID, LocationID: r.LocationID,
		Provider: r.Provider, FetchedAt: r.FetchedAt,
		ValidFrom: r.ValidFrom, ValidUntil: r.ValidUntil,
		HighTempC: r.HighTempC, LowTempC: r.LowTempC,
		RainfallMm: r.RainfallMm, Et0Mm: r.Et0Mm,
		License: r.License, ExpiresAt: r.ExpiresAt,
	}
}
