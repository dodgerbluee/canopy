// Journal query (T16): the chronological photo series read over the
// journal projection. plantId null = workspace-wide timeline; default
// order (occurredAt DESC, id DESC); opaque keyset cursors bound to the
// filter signature.
//
// Union members are added by their owning tasks: PHOTO (T16) and
// ACTIVITY/MEASUREMENT (T17) here; PURCHASE (T18, allocation-keyed).
// Types without a mapping never have projection rows yet, so requesting
// them honestly yields an empty page rather than fabricated entries.
package graphql

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vektah/gqlparser/v2/gqlerror"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/journal"
	"canopy/server/internal/media"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

func (q queryResolver) Journal(ctx context.Context, plantID *string, first *int, after *string, filter *gql.JournalFilter) (*gql.JournalConnection, error) {
	if q.r.Journal == nil {
		return nil, capabilityUnavailable("journal")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	qz := journal.Query{}
	if first != nil {
		qz.First = int32(*first)
	}
	if after != nil {
		qz.After = after
	}
	if plantID != nil {
		id, ok := journalUUID(*plantID)
		if !ok {
			return nil, &gqlerror.Error{
				Message:    "plantId must be a UUID.",
				Extensions: map[string]any{"code": "VALIDATION"},
			}
		}
		qz.PlantID = &id
	}
	if filter != nil {
		for _, t := range filter.EntryTypes {
			qz.EntryTypes = append(qz.EntryTypes, string(t))
		}
		if filter.From != nil {
			t := timeOf(*filter.From)
			qz.From = &t
		}
		if filter.To != nil {
			t := timeOf(*filter.To)
			qz.To = &t
		}
		if filter.IncludeTombstoned != nil {
			qz.IncludeTombstoned = *filter.IncludeTombstoned
		}
	}

	_, conn, err := q.journalPage(ctx, p, qz)
	return conn, err
}

// journalPage runs one journal read and hydrates the union members
// (one load per type per page). Shared with the T28 todayOverview
// composition so both paths render entries identically.
func (q queryResolver) journalPage(ctx context.Context, p httpx.Principal, qz journal.Query) (journal.Page, *gql.JournalConnection, error) {
	page, err := q.r.Journal.List(ctx, p.WorkspaceID, qz)
	if err != nil {
		if errors.Is(err, journal.ErrInvalidCursor) {
			return journal.Page{}, nil, invalidCursorError()
		}
		return journal.Page{}, nil, internalError(ctx, err)
	}

	// batch-hydrate union members (one load per type per page)
	var photoIDs, activityIDs, measurementIDs, allocationIDs, careIDs, insightIDs []pgtype.UUID
	for _, e := range page.Items {
		id, ok := journalUUID(e.SourceID)
		if !ok {
			continue
		}
		switch e.EntryType {
		case "PHOTO":
			photoIDs = append(photoIDs, id)
		case "ACTIVITY":
			activityIDs = append(activityIDs, id)
		case "MEASUREMENT":
			measurementIDs = append(measurementIDs, id)
		case "PURCHASE":
			allocationIDs = append(allocationIDs, id)
		case "CARE":
			careIDs = append(careIDs, id)
		case "INSIGHT":
			insightIDs = append(insightIDs, id)
		}
	}
	photos := map[string]media.PhotoView{}
	if len(photoIDs) > 0 {
		if q.r.Media == nil {
			return journal.Page{}, nil, capabilityUnavailable("journal.photos")
		}
		if photos, err = q.r.Media.PhotosByIDs(ctx, p.WorkspaceID, photoIDs); err != nil {
			return journal.Page{}, nil, internalError(ctx, err)
		}
	}
	activities := map[string]journal.ActivityView{}
	if len(activityIDs) > 0 {
		if activities, err = q.r.Journal.ActivitiesByIDs(ctx, p.WorkspaceID, activityIDs); err != nil {
			return journal.Page{}, nil, internalError(ctx, err)
		}
	}
	measurements := map[string]journal.MeasurementView{}
	if len(measurementIDs) > 0 {
		if measurements, err = q.r.Journal.MeasurementsByIDs(ctx, p.WorkspaceID, measurementIDs); err != nil {
			return journal.Page{}, nil, internalError(ctx, err)
		}
	}
	allocations := map[string]dbgen.PurchaseAllocation{}
	if len(allocationIDs) > 0 {
		if allocations, err = q.r.Journal.AllocationsByIDs(ctx, p.WorkspaceID, allocationIDs); err != nil {
			return journal.Page{}, nil, internalError(ctx, err)
		}
	}
	// CARE members (T24): the projection row carries plant/time/summary;
	// the occurrence row adds kind/activityKind. Tombstoned completions
	// keep their occurrence rows, so hydration never drops a member.
	careOccs := map[string]dbgen.CareOccurrence{}
	if len(careIDs) > 0 {
		rows, err := q.r.DB.ListOccurrencesByIDs(ctx, dbgen.ListOccurrencesByIDsParams{
			WorkspaceID: p.WorkspaceID, Ids: careIDs,
		})
		if err != nil {
			return journal.Page{}, nil, internalError(ctx, err)
		}
		for _, r := range rows {
			careOccs[uuidString(r.ID)] = r
		}
	}
	// INSIGHT members (T33): accepted synthesis projects the journal line;
	// the run row adds the analysis kind.
	insightRuns := map[string]dbgen.ListAnalysisRunsByIDsRow{}
	if len(insightIDs) > 0 {
		rows, err := q.r.DB.ListAnalysisRunsByIDs(ctx, dbgen.ListAnalysisRunsByIDsParams{
			WorkspaceID: p.WorkspaceID, Ids: insightIDs,
		})
		if err != nil {
			return journal.Page{}, nil, internalError(ctx, err)
		}
		for _, r := range rows {
			insightRuns[uuidString(r.ID)] = r
		}
	}

	tomb := func(e journal.EntryView) *scalars.DateTime {
		if e.TombstonedAt != nil {
			d := scalars.DateTime(*e.TombstonedAt)
			return &d
		}
		return nil
	}
	items := make([]gql.JournalEntry, 0, len(page.Items))
	for _, e := range page.Items {
		switch e.EntryType {
		case "PHOTO":
			ph, ok := photos[e.SourceID]
			if !ok {
				// FK cascade makes projection-without-photo impossible; skip
				// defensively rather than emit a broken union member.
				continue
			}
			entry := &gql.PhotoEntry{
				ID:           e.ID,
				PlantID:      e.PlantID,
				OccurredAt:   scalars.DateTime(e.OccurredAt),
				CreatedAt:    scalars.DateTime(e.CreatedAt),
				Summary:      e.Summary,
				TombstonedAt: tomb(e),
				Photo:        photoOut(ph),
			}
			items = append(items, entry)
		case "ACTIVITY":
			act, ok := activities[e.SourceID]
			if !ok {
				continue
			}
			items = append(items, &gql.ActivityEntry{
				ID:           e.ID,
				PlantID:      e.PlantID,
				OccurredAt:   scalars.DateTime(e.OccurredAt),
				CreatedAt:    scalars.DateTime(e.CreatedAt),
				Summary:      e.Summary,
				TombstonedAt: tomb(e),
				Activity:     activityOut(act),
			})
		case "MEASUREMENT":
			mm, ok := measurements[e.SourceID]
			if !ok {
				continue
			}
			items = append(items, &gql.MeasurementEntry{
				ID:           e.ID,
				PlantID:      e.PlantID,
				OccurredAt:   scalars.DateTime(e.OccurredAt),
				CreatedAt:    scalars.DateTime(e.CreatedAt),
				Summary:      e.Summary,
				TombstonedAt: tomb(e),
				Measurement:  measurementOut(mm),
			})
		case "PURCHASE":
			al, ok := allocations[e.SourceID]
			if !ok {
				continue
			}
			alloc, err := toAllocation(al)
			if err != nil {
				return journal.Page{}, nil, internalError(ctx, err)
			}
			items = append(items, &gql.PurchaseEntry{
				ID:           e.ID,
				PlantID:      e.PlantID,
				OccurredAt:   scalars.DateTime(e.OccurredAt),
				CreatedAt:    scalars.DateTime(e.CreatedAt),
				Summary:      e.Summary,
				TombstonedAt: tomb(e),
				Allocation:   alloc,
			})
		case "CARE":
			occ, ok := careOccs[e.SourceID]
			if !ok {
				continue
			}
			entry := &gql.CareEntry{
				ID:           e.ID,
				PlantID:      e.PlantID,
				OccurredAt:   scalars.DateTime(e.OccurredAt),
				CreatedAt:    scalars.DateTime(e.CreatedAt),
				Summary:      e.Summary,
				TombstonedAt: tomb(e),
				OccurrenceID: e.SourceID,
				Kind:         gql.RuleKind(occ.Kind),
			}
			if occ.ActivityKind.Valid {
				ak := gql.ActivityKind(occ.ActivityKind.ActivityKind)
				entry.ActivityKind = &ak
			}
			items = append(items, entry)
		case "INSIGHT":
			run, ok := insightRuns[e.SourceID]
			if !ok {
				// Run redaction keeps journal lines (03: evidence refs are
				// redacted, not silently rewritten); without the run row
				// the kind is unknowable, so skip the member defensively.
				continue
			}
			items = append(items, &gql.InsightEntry{
				ID:           e.ID,
				PlantID:      e.PlantID,
				OccurredAt:   scalars.DateTime(e.OccurredAt),
				CreatedAt:    scalars.DateTime(e.CreatedAt),
				Summary:      e.Summary,
				TombstonedAt: tomb(e),
				RunID:        e.SourceID,
				Kind:         gql.AnalysisKind(run.Kind),
			})
		}
	}

	pageInfo := &gql.PageInfo{HasNextPage: page.HasNext}
	if page.EndCursor != "" {
		ec := page.EndCursor
		pageInfo.EndCursor = &ec
	}
	return page, &gql.JournalConnection{Items: items, PageInfo: pageInfo}, nil
}

func journalUUID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}

func timeOf(d scalars.DateTime) time.Time { return time.Time(d) }
