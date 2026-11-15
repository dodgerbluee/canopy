package graphql

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
)

// Read-side bounds also govern the complexity charge below. PLAN03 must validate
// citations against fetched sources before READY; this adapter checks shape only.

var plantingSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)

func plantingGuidanceOut(row *dbgen.PlantingGuidanceSnapshot) (*gql.PlantingGuidanceSnapshot, error) {
	if row == nil {
		return nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(row.Facts, &raw); err != nil {
		return nil, err
	}
	if len(raw) > planning.MaxGuidanceFacts {
		return nil, errors.New("too many planting guidance facts")
	}
	out := &gql.PlantingGuidanceSnapshot{ID: uuidString(row.ID), Generation: int(row.Generation), State: gql.KnowledgeState(row.State), Message: row.Message, Facts: []*gql.PlantingGuidanceFact{}, Assumptions: append([]string{}, row.Assumptions...), RetrievedAt: dtPtrOut(row.RetrievedAt), CreatedAt: dtOut(row.CreatedAt), UpdatedAt: dtOut(row.UpdatedAt)}
	for _, data := range raw {
		type factBody gql.PlantingGuidanceFact
		f := &gql.PlantingGuidanceFact{}
		wire := struct {
			*factBody
			RetrievedAt time.Time `json:"retrievedAt"`
		}{factBody: (*factBody)(f)}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&wire); err != nil {
			return nil, err
		}
		f.RetrievedAt = scalars.DateTime(wire.RetrievedAt)
		if err := validatePlantingFact(f, wire.RetrievedAt); err != nil {
			return nil, err
		}
		out.Facts = append(out.Facts, f)
	}
	return out, nil
}
func validatePlantingFact(f *gql.PlantingGuidanceFact, retrieved time.Time) error {
	fail := func() error { return errors.New("invalid stored planting guidance shape") }
	u, err := url.Parse(f.SourceURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return fail()
	}
	if f.ID == "" || !f.Phase.IsValid() || f.Label == "" || f.Summary == "" || f.SourceTitle == "" || f.Publisher == "" || f.SourceRegion == "" || retrieved.IsZero() || !plantingSHA.MatchString(f.ContentSha256) {
		return fail()
	}
	if f.Conditions == nil || f.Windows == nil || len(f.Conditions) > planning.MaxGuidanceConditions || len(f.Windows) > planning.MaxGuidanceWindows {
		return fail()
	}
	for _, w := range f.Windows {
		if w == nil || !w.Precision.IsValid() || w.Label == "" || (w.Hemisphere != nil && !w.Hemisphere.IsValid()) {
			return fail()
		}
		bodies := 0
		if w.Absolute != nil {
			bodies++
		}
		if w.Annual != nil {
			bodies++
		}
		if w.Relative != nil {
			bodies++
		}
		switch w.Precision {
		case gql.PlantingGuidancePrecisionUnknown:
			if bodies != 0 {
				return fail()
			}
		case gql.PlantingGuidancePrecisionAbsolute:
			if bodies != 1 || w.Absolute == nil {
				return fail()
			}
			p := w.Absolute
			input := &gql.PlantingPeriodInput{Precision: p.Precision, Year: p.Year}
			if p.Dates != nil {
				input.Dates = &gql.PlantingDateRangeInput{From: p.Dates.From, To: p.Dates.To}
			}
			if p.Months != nil {
				input.Months = &gql.PlantingMonthRangeInput{From: p.Months.From, To: p.Months.To}
			}
			if p.Season != nil {
				input.Season = &gql.PlantingSeasonPeriodInput{Season: p.Season.Season, Year: p.Season.Year}
			}
			if len(planning.ValidatePeriod(plantingPeriodIn(input))) > 0 {
				return fail()
			}
		case gql.PlantingGuidancePrecisionAnnual:
			if bodies != 1 || w.Annual == nil || !validPlantingAnnual(w.Annual) {
				return fail()
			}
		case gql.PlantingGuidancePrecisionRelative:
			if bodies != 1 || w.Relative == nil || !w.Relative.Anchor.IsValid() {
				return fail()
			}
			r := w.Relative
			if r.FromDays != nil && r.ToDays != nil && *r.FromDays > *r.ToDays {
				return fail()
			}
			for _, v := range []*int{r.FromDays, r.ToDays} {
				if v != nil && (*v < -2147483648 || *v > 2147483647) {
					return fail()
				}
			}
		}
	}
	return nil
}
func validPlantingAnnual(p *gql.PlantingAnnualPeriod) bool {
	bodies := 0
	if p.Dates != nil {
		bodies++
	}
	if p.Months != nil {
		bodies++
	}
	if p.Season != nil {
		bodies++
	}
	if bodies != 1 {
		return false
	}
	switch p.Precision {
	case gql.PlantingAnnualPrecisionDateRange:
		if p.Dates == nil {
			return false
		}
		for _, day := range []*gql.PlantingMonthDay{p.Dates.From, p.Dates.To} {
			if day == nil {
				return false
			}
			raw := fmt.Sprintf("2000-%02d-%02d", day.Month, day.Day)
			var date scalars.Date
			if date.UnmarshalGQL(raw) != nil {
				return false
			}
		}
		return true
	case gql.PlantingAnnualPrecisionMonthRange:
		return p.Months != nil && p.Months.From >= 1 && p.Months.From <= 12 && p.Months.To >= 1 && p.Months.To <= 12
	case gql.PlantingAnnualPrecisionSeason:
		return p.Season != nil && p.Season.IsValid()
	}
	return false
}
