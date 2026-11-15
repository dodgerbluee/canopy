package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Guidance is a publication recommendation. The chosen target period remains a
// separate plan value, and recurring advice carries no invented projection year.
type MonthDay struct {
	Month int `json:"month"`
	Day   int `json:"day"`
}
type AnnualDateRange struct {
	From *MonthDay `json:"from"`
	To   *MonthDay `json:"to"`
}
type AnnualMonthRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}
type AnnualPeriod struct {
	Precision string            `json:"precision"`
	Dates     *AnnualDateRange  `json:"dates,omitempty"`
	Months    *AnnualMonthRange `json:"months,omitempty"`
	Season    *Season           `json:"season,omitempty"`
}
type RelativeWindow struct {
	Anchor   string `json:"anchor"`
	FromDays *int   `json:"fromDays"`
	ToDays   *int   `json:"toDays"`
}
type GuidanceWindow struct {
	Precision  string          `json:"precision"`
	Label      string          `json:"label"`
	Absolute   *Period         `json:"absolute,omitempty"`
	Annual     *AnnualPeriod   `json:"annual,omitempty"`
	Relative   *RelativeWindow `json:"relative,omitempty"`
	Hemisphere *string         `json:"hemisphere,omitempty"`
}
type GuidanceFact struct {
	ID            string           `json:"id"`
	Phase         string           `json:"phase"`
	Label         string           `json:"label"`
	Summary       string           `json:"summary"`
	Conditions    []string         `json:"conditions"`
	Windows       []GuidanceWindow `json:"windows"`
	SourceTitle   string           `json:"sourceTitle"`
	SourceURL     string           `json:"sourceUrl"`
	Publisher     string           `json:"publisher"`
	SourceRegion  string           `json:"sourceRegion"`
	RetrievedAt   time.Time        `json:"retrievedAt"`
	ContentSHA256 string           `json:"contentSha256"`
	// Immutable extraction provenance, retained in export/backup. Product API
	// adapters expose the public source fields; they do not dump internal prompts.
	SourceID          string `json:"sourceId,omitempty"`
	ExtractionVersion int    `json:"extractionVersion,omitempty"`
	Evidence          string `json:"evidence,omitempty"`
	TimingText        string `json:"timingText,omitempty"`
	ExtractionModel   string `json:"extractionModel,omitempty"`
}

type GuidanceSubject struct {
	Name, ScientificName, Cultivar, Category string
	Method, Placement, LocationID            string
	Quantity                                 *int32
	Notes                                    string
}
type GuidanceContext struct {
	Fingerprint string
	State       string // PENDING when research may run; otherwise an honest terminal state
	Message     string
	Assumptions []string
}
type GuidanceResult struct {
	ContextChanged bool
	State, Message string
	Facts          []GuidanceFact
	Assumptions    []string
}

// Providers inspect configured context without network calls. Research receives
// public-source work; implementations must never send private plan notes to a model.
type GuidanceProvider interface {
	Context(context.Context, pgtype.UUID, GuidanceSubject) (GuidanceContext, error)
	Research(context.Context, pgtype.UUID, GuidanceSubject, GuidanceContext) (GuidanceResult, error)
}

var ErrGuidanceContextChanged = errors.New("planning guidance context changed")
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func DecodeGuidanceFacts(raw []byte) ([]GuidanceFact, error) {
	if len(raw) > 262144 {
		return nil, errors.New("guidance exceeds its storage bound")
	}
	var facts []GuidanceFact
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&facts); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing guidance data")
	}
	if facts == nil {
		return nil, errors.New("guidance facts must be an array")
	}
	if err := ValidateGuidanceFacts(facts); err != nil {
		return nil, err
	}
	return facts, nil
}
func ValidateGuidanceFacts(facts []GuidanceFact) error {
	if len(facts) > MaxGuidanceFacts {
		return errors.New("too many guidance facts")
	}
	fail := func() error { return errors.New("invalid stored planting guidance shape") }
	for _, f := range facts {
		u, err := url.Parse(f.SourceURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return fail()
		}
		if f.ID == "" || !contains(f.Phase, "START_INDOORS", "DIRECT_SOW", "TRANSPLANT", "PLANT_OUT", "GROWTH", "BLOOM", "HARVEST", "DORMANCY", "PROTECT") || f.Label == "" || f.Summary == "" || f.SourceTitle == "" || f.Publisher == "" || f.SourceRegion == "" || f.RetrievedAt.IsZero() || !sha256Pattern.MatchString(f.ContentSHA256) {
			return fail()
		}
		if f.Conditions == nil || f.Windows == nil || len(f.Conditions) > MaxGuidanceConditions || len(f.Windows) > MaxGuidanceWindows {
			return fail()
		}
		for _, w := range f.Windows {
			if w.Label == "" || (w.Hemisphere != nil && !contains(*w.Hemisphere, "NORTHERN", "SOUTHERN")) {
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
			case "UNKNOWN":
				if bodies != 0 {
					return fail()
				}
			case "ABSOLUTE":
				if bodies != 1 || w.Absolute == nil || len(ValidatePeriod(w.Absolute)) > 0 {
					return fail()
				}
			case "ANNUAL":
				if bodies != 1 || w.Annual == nil || !ValidAnnualPeriod(w.Annual) {
					return fail()
				}
			case "RELATIVE":
				if bodies != 1 || w.Relative == nil || !contains(w.Relative.Anchor, "LAST_FROST", "FIRST_FROST", "SOWING", "TRANSPLANTING", "PLANTING", "HARVEST", "UNKNOWN") {
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
			default:
				return fail()
			}
		}
	}
	return nil
}
func ValidAnnualPeriod(p *AnnualPeriod) bool {
	if p == nil {
		return false
	}
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
	case "DATE_RANGE":
		if p.Dates == nil {
			return false
		}
		for _, day := range []*MonthDay{p.Dates.From, p.Dates.To} {
			if day == nil || day.Month < 1 || day.Month > 12 || day.Day < 1 {
				return false
			}
			last := time.Date(2000, time.Month(day.Month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
			if day.Day > last {
				return false
			}
		}
		return true
	case "MONTH_RANGE":
		return p.Months != nil && p.Months.From >= 1 && p.Months.From <= 12 && p.Months.To >= 1 && p.Months.To <= 12
	case "SEASON":
		return p.Season != nil && contains(string(*p.Season), "SPRING", "SUMMER", "AUTUMN", "WINTER")
	}
	return false
}
