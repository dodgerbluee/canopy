package knowledge

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type timingHit struct {
	start, end int
	key        string
	months     []int
	basis      string
}

var calendarMonths = []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
var seasonMonths = map[string][]int{"spring": {3, 4, 5}, "summer": {6, 7, 8}, "fall": {9, 10, 11}, "autumn": {9, 10, 11}, "winter": {12, 1, 2}}
var seasonRE = regexp.MustCompile(`\b(?:(early|mid|middle|late)[ -]?)?(spring|summer|fall|autumn|winter)\b`)
var rangeRE = regexp.MustCompile(`\b(to|through|until)\b|[-–—]`)
var unsafeTimingRE = regexp.MustCompile(`\b(avoid|never|except|not|before|after|following)\b|\bprior to\b`)
var yearRoundRE = regexp.MustCompile(`\byear[ -]round\b`)

func timingHits(text string) []timingHit {
	lower := strings.ToLower(text)
	hits := []timingHit{}
	for i, month := range calendarMonths {
		for _, at := range regexp.MustCompile(`\b`+month+`\b`).FindAllStringIndex(lower, -1) {
			// Lowercase "may" is usually a verb, not an asserted calendar date.
			if month == "may" && text[at[0]:at[1]] == "may" {
				continue
			}
			hits = append(hits, timingHit{at[0], at[1], month, []int{i + 1}, "MONTH"})
		}
	}
	for _, at := range seasonRE.FindAllStringSubmatchIndex(lower, -1) {
		word := lower[at[4]:at[5]]
		months := append([]int{}, seasonMonths[word]...)
		qualifier := ""
		if at[2] >= 0 {
			qualifier = lower[at[2]:at[3]]
		}
		switch qualifier {
		case "early":
			months = months[:1]
		case "mid", "middle":
			months = months[1:2]
		case "late":
			months = months[2:]
		}
		// Autumn and fall are the same timing concept, as are mid/middle.
		if word == "autumn" {
			word = "fall"
		}
		if qualifier == "middle" {
			qualifier = "mid"
		}
		hits = append(hits, timingHit{at[0], at[1], qualifier + " " + word, months, "SEASON"})
	}
	for _, at := range yearRoundRE.FindAllStringIndex(lower, -1) {
		hits = append(hits, timingHit{at[0], at[1], "year-round", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, "SEASON"})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start < hits[j].start })
	return hits
}

// ParseTiming combines all supported clauses, retaining discrete months and
// spanning only explicit ranges. Mixed month/season wording is approximate.
func ParseTiming(text string) ([]int, string) {
	return parseTimingForHemisphere(text, "NORTHERN")
}

func parseTimingForHemisphere(text, hemisphere string) ([]int, string) {
	if unsafeTimingRE.MatchString(strings.ToLower(text)) {
		return []int{}, "UNSPECIFIED"
	}
	hits := timingHits(text)
	for i := range hits {
		if hits[i].basis != "SEASON" {
			continue
		}
		if hemisphere != "NORTHERN" && hemisphere != "SOUTHERN" {
			return []int{}, "UNSPECIFIED"
		}
		if hemisphere == "SOUTHERN" {
			for j, m := range hits[i].months {
				hits[i].months[j] = (m+5)%12 + 1
			}
		}
	}
	if len(hits) == 0 {
		return []int{}, "UNSPECIFIED"
	}
	set := map[int]bool{}
	basis := "MONTH"
	for _, h := range hits {
		for _, m := range h.months {
			set[m] = true
		}
		if h.basis == "SEASON" {
			basis = "SEASON"
		}
	}
	for i := 0; i+1 < len(hits); i++ {
		a, b := hits[i], hits[i+1]
		if !rangeRE.MatchString(strings.ToLower(text[a.end:b.start])) {
			continue
		}
		// Season windows are ordered chronologically, including winter wrap.
		start, end := a.months[0], b.months[len(b.months)-1]
		for m := start; m != end; m = m%12 + 1 {
			set[m] = true
		}
	}
	months := []int{}
	for m := 1; m <= 12; m++ {
		if set[m] {
			months = append(months, m)
		}
	}
	return months, basis
}

var sentenceEndRE = regexp.MustCompile(`[.!?]["”']?\s+|\n+`)

// Include the enclosing sentence so a model cannot quote just "in September"
// and drop "continuing through October" from the same source statement.
// Boundaries never extend into the next sentence's unrelated care advice.
func supportingSentences(source, evidence string) []string {
	source = normalizeSourceText(source)
	searchable := strings.ReplaceAll(source, "\n", " ")
	contexts := []string{}
	for offset := 0; offset < len(source); {
		at := strings.Index(searchable[offset:], evidence)
		if at < 0 {
			break
		}
		start := offset + at
		end := start + len(evidence)
		left := 0
		for _, boundary := range sentenceEndRE.FindAllStringIndex(source[:start], -1) {
			left = boundary[1]
		}
		right := len(source)
		// If evidence includes a terminal period, do not add the next sentence.
		trimmed := strings.TrimRight(evidence, `"”'`)
		if strings.HasSuffix(trimmed, ".") || strings.HasSuffix(trimmed, "!") || strings.HasSuffix(trimmed, "?") {
			right = end
		} else if boundary := sentenceEndRE.FindStringIndex(source[end:]); boundary != nil {
			right = end + boundary[1]
		}
		if right-left <= 1500 {
			contexts = append(contexts, strings.TrimSpace(source[left:right]))
		}
		offset = start + len(evidence)
	}
	return contexts
}

func missingTiming(from, to string) []string {
	have := map[string]bool{}
	for _, h := range timingHits(to) {
		have[h.key] = true
	}
	missing := []string{}
	for _, h := range timingHits(from) {
		if !have[h.key] {
			missing = append(missing, strings.TrimSpace(h.key))
			have[h.key] = true
		}
	}
	return missing
}

// Seasonal paraphrases may describe an explicitly dated source month (March
// as spring). A summary may not introduce a precise month from a broad season.
func unsupportedSummaryTiming(summary, support string) []string {
	supported := timingHits(support)
	missing := []string{}
	for _, claim := range timingHits(summary) {
		found := false
		for _, source := range supported {
			if claim.key == source.key {
				found = true
				break
			}
			if claim.basis != "SEASON" {
				continue
			}
			for _, a := range claim.months {
				for _, b := range source.months {
					if a == b {
						found = true
					}
				}
			}
		}
		if !found {
			missing = append(missing, strings.TrimSpace(claim.key))
		}
	}
	return missing
}

var phaseVerbs = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"FERTILIZE", regexp.MustCompile(`(?i)\b(fertiliz\w*|feeding|apply(?:ing)? (?:nitrogen|fertilizer))\b`)},
	{"PRUNE", regexp.MustCompile(`(?i)\b(prun\w*|cut back|remove (?:dead|spent|unwanted|suckers|shoots))\b`)},
	{"WATER_CHECK", regexp.MustCompile(`(?i)\b(water(?:ing)?|irrigat\w*)\b`)},
	{"HARVEST", regexp.MustCompile(`(?i)\b(harvest(?:ing)?|ripen\w*|matur(?:e|es|ity|ing)|pick(?:ing)?)\b`)},
	{"BLOOM", regexp.MustCompile(`(?i)\b(bloom(?:s|ing)?|flowering)\b`)},
}

// Prevent filling one phase's band with another action's dates from a
// compound sentence. This is deliberately conservative; ambiguous compound
// advice can remain a note rather than widening a calendar band.
func phaseTimingMatches(context string) bool {
	phases := map[string]bool{}
	for _, hit := range timingHits(context) {
		nearest := -1
		phase := ""
		for _, rule := range phaseVerbs {
			for _, at := range rule.pattern.FindAllStringIndex(context[:hit.start], -1) {
				if at[0] > nearest {
					nearest = at[0]
					phase = rule.kind
				}
			}
		}
		if phase != "" {
			phases[phase] = true
		}
	}
	return len(phases) <= 1
}

func unsafeCalendarContext(context string) bool {
	for _, sentence := range sentenceEndRE.Split(context, -1) {
		if len(timingHits(sentence)) > 0 && unsafeTimingRE.MatchString(strings.ToLower(sentence)) {
			return true
		}
	}
	return false
}

func validateTiming(source, evidence, timing, summary string, allowUntimed bool) error {
	contexts := supportingSentences(source, evidence)
	if len(contexts) == 0 {
		return errors.New("supporting source sentence is ambiguous or too long; use a complete bounded statement")
	}
	var last error
	for _, context := range contexts {
		// A paraphrase cannot introduce calendar concepts absent from its source.
		if missing := unsupportedSummaryTiming(summary, context); len(missing) > 0 {
			last = fmt.Errorf("summary introduces unsupported timing (%s); preserve source wording and conditions", strings.Join(missing, ", "))
			continue
		}
		if allowUntimed {
			return nil
		}
		if unsafeCalendarContext(context) {
			if timing == "" {
				return nil
			}
			if months, _ := ParseTiming(timing); len(months) > 0 {
				last = errors.New("source timing is conditional, relative or negative; do not extract an isolated month from it")
				continue
			}
		}
		if missing := missingTiming(context, timing); len(missing) > 0 {
			last = fmt.Errorf("incomplete timing: source sentence includes %s; preserve every window for this phase or split distinct actions/conditions", strings.Join(missing, ", "))
			continue
		}
		if missing := unsupportedSummaryTiming(summary, timing); len(missing) > 0 {
			last = fmt.Errorf("summary and calendar timing disagree (%s); include all supported windows", strings.Join(missing, ", "))
			continue
		}
		if timing != "" {
			months, _ := ParseTiming(timing)
			if len(months) > 0 && !phaseTimingMatches(context) {
				last = errors.New("source sentence mixes dates for different actions; split the actions or retain an untimed conditional note")
				continue
			}
			if len(months) == 0 && len(timingHits(timing)) > 0 {
				last = errors.New("relative or negative timing cannot be plotted as an absolute window; retain it as an untimed conditional note")
				continue
			}
		}
		return nil
	}
	return last
}

func currentFacts(facts []Fact) bool {
	if len(facts) == 0 {
		return false
	}
	for _, f := range facts {
		if f.ExtractionVersion != ExtractionVersion {
			return false
		}
	}
	return true
}
