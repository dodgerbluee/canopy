package graphql

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
)

func plantingGuidanceOut(row *dbgen.PlantingGuidanceSnapshot) (*gql.PlantingGuidanceSnapshot, error) {
	if row == nil {
		return nil, nil
	}
	facts, err := planning.DecodeGuidanceFacts(row.Facts)
	if err != nil {
		return nil, err
	}
	out := &gql.PlantingGuidanceSnapshot{ID: uuidString(row.ID), Generation: int(row.Generation), State: gql.KnowledgeState(row.State), Message: row.Message, Facts: []*gql.PlantingGuidanceFact{}, Assumptions: append([]string{}, row.Assumptions...), RetrievedAt: dtPtrOut(row.RetrievedAt), CreatedAt: dtOut(row.CreatedAt), UpdatedAt: dtOut(row.UpdatedAt)}
	for _, f := range facts {
		fact := plantingGuidanceFactOut(f)
		out.Facts = append(out.Facts, fact)
	}
	return out, nil
}
func plantingWindowOut(w planning.GuidanceWindow) *gql.PlantingGuidanceWindow {
	out := &gql.PlantingGuidanceWindow{Precision: gql.PlantingGuidancePrecision(w.Precision), Label: w.Label, Absolute: plantingPeriodOut(w.Absolute)}
	if w.Hemisphere != nil {
		v := gql.PlantingHemisphere(*w.Hemisphere)
		out.Hemisphere = &v
	}
	if a := w.Annual; a != nil {
		out.Annual = &gql.PlantingAnnualPeriod{Precision: gql.PlantingAnnualPrecision(a.Precision)}
		if a.Months != nil {
			out.Annual.Months = &gql.PlantingAnnualMonthRange{From: a.Months.From, To: a.Months.To}
		}
		if a.Dates != nil {
			out.Annual.Dates = &gql.PlantingAnnualDateRange{From: &gql.PlantingMonthDay{Month: a.Dates.From.Month, Day: a.Dates.From.Day}, To: &gql.PlantingMonthDay{Month: a.Dates.To.Month, Day: a.Dates.To.Day}}
		}
		if a.Season != nil {
			v := gql.PlantingSeason(*a.Season)
			out.Annual.Season = &v
		}
	}
	if r := w.Relative; r != nil {
		out.Relative = &gql.PlantingRelativeWindow{Anchor: gql.PlantingTimingAnchor(r.Anchor), FromDays: r.FromDays, ToDays: r.ToDays}
	}
	return out
}

func plantingGuidanceFactOut(f planning.GuidanceFact) *gql.PlantingGuidanceFact {
	fact := &gql.PlantingGuidanceFact{ID: f.ID, Phase: gql.PlantingPhaseKind(f.Phase), Label: f.Label, Summary: f.Summary, Conditions: append([]string{}, f.Conditions...), Windows: []*gql.PlantingGuidanceWindow{}, SourceTitle: f.SourceTitle, SourceURL: f.SourceURL, Publisher: f.Publisher, SourceRegion: f.SourceRegion, RetrievedAt: scalars.DateTime(f.RetrievedAt), ContentSha256: f.ContentSHA256}
	for _, w := range f.Windows {
		fact.Windows = append(fact.Windows, plantingWindowOut(w))
	}
	return fact
}
