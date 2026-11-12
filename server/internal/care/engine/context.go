package engine

import (
	"encoding/json"
	"fmt"
	"time"
)

// ContextFact names mirror the frozen ContextFact enum in the care SDL.
const (
	FactCultivar       = "CULTIVAR"
	FactScientificName = "SCIENTIFIC_NAME"
	FactPlacement      = "PLACEMENT"
	FactLocationSitu   = "LOCATION_SITUATION"
	FactPotVolume      = "POT_VOLUME"
	FactRegion         = "REGION"
	FactHemisphere     = "HEMISPHERE"
)

// Context is everything the engine knows about the plant and moment. Empty
// strings mean "unknown"; the engine never guesses.
type Context struct {
	Today Date
	Now   time.Time // clock for weather freshness

	Hemisphere     string // NORTHERN | SOUTHERN | ""
	Region         string
	Situation      string // INDOOR | OUTDOOR | COVERED | ""
	Cultivar       string
	ScientificName string
	Placement      string
	PotVolumeSet   bool

	Weather *Weather // nil when no snapshot exists
}

// MissingFacts returns the required facts the context lacks, in the order
// the rule declared them. Non-empty means the advisory abstains (fail
// closed to "needs context").
func MissingFacts(required []string, ctx Context) []string {
	have := map[string]bool{
		FactRegion:         ctx.Region != "",
		FactHemisphere:     ctx.Hemisphere != "",
		FactLocationSitu:   ctx.Situation != "",
		FactCultivar:       ctx.Cultivar != "",
		FactScientificName: ctx.ScientificName != "",
		FactPlacement:      ctx.Placement != "",
		FactPotVolume:      ctx.PotVolumeSet,
	}
	var out []string
	for _, f := range required {
		if !have[f] {
			out = append(out, f)
		}
	}
	return out
}

// Environment is the profile-version applicability predicate (canonical
// jsonb: {"situations":[...],"hemispheres":[...]}); empty lists mean "any".
type Environment struct {
	Situations  []string
	Hemispheres []string
}

func ParseEnvironment(raw []byte) (Environment, error) {
	var v struct {
		Situations  []string `json:"situations"`
		Hemispheres []string `json:"hemispheres"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return Environment{}, fmt.Errorf("engine: environment json: %w", err)
	}
	return Environment{Situations: v.Situations, Hemispheres: v.Hemispheres}, nil
}

// Matches decides whether a profile version applies to this context.
// A restricted predicate with an unknown fact does NOT silently pass: it
// reports the missing fact so the caller can abstain. Southern-hemisphere
// applicability comes from explicit hemisphere data, never month guesses.
func (e Environment) Matches(ctx Context) (ok bool, missing []string) {
	if len(e.Hemispheres) > 0 {
		if ctx.Hemisphere == "" {
			missing = append(missing, FactHemisphere)
		} else if !contains(e.Hemispheres, ctx.Hemisphere) {
			return false, nil
		}
	}
	if len(e.Situations) > 0 {
		if ctx.Situation == "" {
			missing = append(missing, FactLocationSitu)
		} else if !contains(e.Situations, ctx.Situation) {
			return false, nil
		}
	}
	return len(missing) == 0, missing
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Weather is a provider snapshot; nil members mean the provider did not
// supply that field.
type Weather struct {
	FetchedAt  time.Time
	ValidFrom  time.Time
	ValidUntil time.Time
	HighTempC  *float64
	LowTempC   *float64
	RainfallMm *float64
	Et0Mm      *float64
}

// Freshness implements the 03 bands: fresh <= 6h, stale <= 24h (shown as
// stale), anything older or absent is unavailable.
func (w *Weather) Freshness(now time.Time) string {
	if w == nil {
		return "UNAVAILABLE"
	}
	age := now.Sub(w.FetchedAt)
	switch {
	case age >= 0 && age <= 6*time.Hour:
		return "FRESH"
	case age > 6*time.Hour && age <= 24*time.Hour:
		return "STALE"
	default:
		return "UNAVAILABLE"
	}
}

// Rule is the engine's view of a care rule from a profile version.
type Rule struct {
	RuleKey            string
	Kind               string // CHECK | ACTION
	ActivityKind       string // set only for ACTION
	RequiredFacts      []string
	WeatherSensitivity string // NONE | RAINFALL | FROST
	GuidanceText       string
}

// Advice is the advisory output: why a rule applies, what evidence backs
// it, and which facts make it abstain. The engine NEVER decides that a
// plant was watered, that fertilizer was applied, or what the soil moisture
// is — weather can only elevate a check, and the reason text says so.
type Advice struct {
	Abstain       bool
	MissingFacts  []string
	ReasonText    string
	EvidenceNotes []string
}

// Advise evaluates one rule against the context. Rules with missing
// required facts fail closed to "needs context".
func Advise(r Rule, ctx Context) Advice {
	var a Advice
	if missing := MissingFacts(r.RequiredFacts, ctx); len(missing) > 0 {
		a.Abstain = true
		a.MissingFacts = missing
		a.ReasonText = "Needs context: " + join(missing, ", ") +
			" not recorded for this plant; the advice abstains until those facts are entered."
		return a
	}
	a.ReasonText = r.GuidanceText

	switch r.WeatherSensitivity {
	case "RAINFALL":
		if ctx.Situation == "INDOOR" || ctx.Situation == "COVERED" {
			a.EvidenceNotes = append(a.EvidenceNotes,
				"rainfall ignored for water guidance: indoor/covered context")
			return a
		}
	case "FROST":
		// snapshot notes below
	default: // NONE: weather plays no part in this rule
		return a
	}

	switch ctx.Weather.Freshness(ctx.Now) {
	case "UNAVAILABLE":
		a.EvidenceNotes = append(a.EvidenceNotes,
			"weather snapshot unavailable; guidance proceeds on calendar cadence alone")
	case "STALE":
		a.EvidenceNotes = append(a.EvidenceNotes, "weather snapshot is stale (shown as stale)")
		a.EvidenceNotes = append(a.EvidenceNotes, weatherValues(ctx.Weather)...)
	case "FRESH":
		a.EvidenceNotes = append(a.EvidenceNotes, weatherValues(ctx.Weather)...)
	}
	if r.WeatherSensitivity == "FROST" && ctx.Weather != nil &&
		ctx.Weather.Freshness(ctx.Now) != "UNAVAILABLE" {
		a.EvidenceNotes = append(a.EvidenceNotes,
			"temperature can only elevate a frost check; it never marks work done or asserts soil moisture")
	}
	return a
}

func weatherValues(w *Weather) []string {
	if w == nil {
		return nil
	}
	var out []string
	if w.LowTempC != nil {
		out = append(out, fmt.Sprintf("reported low %.1f°C", *w.LowTempC))
	}
	if w.HighTempC != nil {
		out = append(out, fmt.Sprintf("reported high %.1f°C", *w.HighTempC))
	}
	if w.RainfallMm != nil {
		out = append(out, fmt.Sprintf("reported rainfall %.1fmm", *w.RainfallMm))
	}
	return out
}

func join(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
