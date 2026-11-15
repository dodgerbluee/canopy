package knowledge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"canopy/server/internal/planning"
)

var planningMonthRE = regexp.MustCompile(`(?i)\b(january|jan\.?|february|feb\.?|march|mar\.?|april|apr\.?|may|june|jun\.?|july|jul\.?|august|aug\.?|september|sept?\.?|october|oct\.?|november|nov\.?|december|dec\.?)\b`)
var planningDayRE = regexp.MustCompile(`^\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
var planningRangeRE = regexp.MustCompile(`^\s*(?:to|through|until|[-–—])\s*$`)
var planningNegativeRE = regexp.MustCompile(`(?i)\b(do not|don't|never|avoid|not recommended|cannot|except|until)\b`)
var planningYearRE = regexp.MustCompile(`\b[1-9][0-9]{3}\b`)
var planningISODateRE = regexp.MustCompile(`\b[0-9]{4}-[0-9]{2}-[0-9]{2}\b`)
var planningRelativeRE = regexp.MustCompile(`(?i)(?:(\d{1,3})(?:\s*(?:to|through|[-–—])\s*(\d{1,3}))?\s*(days?|weeks?)\s+)?(before|after|following|from)\s+(?:the\s+)?(last(?:\s+expected)?(?:\s+spring)?\s+frost|first(?:\s+expected)?(?:\s+fall|\s+autumn)?\s+frost|sowing|transplanting|transplants|planting|harvest)\b`)
var planningInequalityRE = regexp.MustCompile(`(?i)\b(before|after|following|prior to)\b`)

type plantingTimeHit struct {
	start, end int
	kind       string
	month, day int
	season     planning.Season
	label      string
}

func monthNumber(raw string) int {
	raw = strings.ToLower(strings.TrimSuffix(raw, "."))
	for i, name := range calendarMonths {
		if raw == name || strings.HasPrefix(name, raw) {
			return i + 1
		}
	}
	return 0
}
func unknownPlantingWindow(label string) []planning.GuidanceWindow {
	if strings.TrimSpace(label) == "" {
		label = "Timing is not specified in this source."
	}
	return []planning.GuidanceWindow{{Precision: "UNKNOWN", Label: label}}
}

// Source phrases, not model-supplied dates, determine every bound. Unsupported
// conditions remain readable but unplotted; no climate anchor is synthesized.
func parsePlantingTiming(raw, hemisphere string) []planning.GuidanceWindow {
	text := strings.Join(strings.Fields(raw), " ")
	if text == "" {
		return unknownPlantingWindow("")
	}
	// Negative recommendations are constraints, not positive planting windows.
	if planningNegativeRE.MatchString(text) {
		return unknownPlantingWindow(text)
	}
	relative := planningRelativeRE.FindAllStringSubmatch(text, -1)
	if len(relative) > 0 {
		remaining := planningRelativeRE.ReplaceAllString(text, "")
		if relativeHasAdditionalWindow(remaining) {
			return unknownPlantingWindow(text)
		}
		windows := []planning.GuidanceWindow{}
		for _, match := range relative {
			anchor := strings.ToLower(match[5])
			kind := "UNKNOWN"
			switch {
			case strings.HasPrefix(anchor, "last"):
				kind = "LAST_FROST"
			case strings.HasPrefix(anchor, "first"):
				kind = "FIRST_FROST"
			case anchor == "sowing":
				kind = "SOWING"
			case strings.HasPrefix(anchor, "transplant"):
				kind = "TRANSPLANTING"
			case anchor == "planting":
				kind = "PLANTING"
			case anchor == "harvest":
				kind = "HARVEST"
			}
			r := &planning.RelativeWindow{Anchor: kind}
			if match[1] != "" {
				from, _ := strconv.Atoi(match[1])
				to := from
				if match[2] != "" {
					to, _ = strconv.Atoi(match[2])
				}
				if from > to {
					return unknownPlantingWindow(text)
				}
				if strings.HasPrefix(strings.ToLower(match[3]), "week") {
					from *= 7
					to *= 7
				}
				if strings.EqualFold(match[4], "before") {
					from, to = -to, -from
				}
				r.FromDays = &from
				r.ToDays = &to
			}
			windows = append(windows, planning.GuidanceWindow{Precision: "RELATIVE", Label: text, Relative: r})
		}
		if len(windows) > planning.MaxGuidanceWindows {
			return unknownPlantingWindow(text)
		}
		return windows
	}
	if planningInequalityRE.MatchString(text) {
		return unknownPlantingWindow(text)
	}
	if matches := planningISODateRE.FindAllStringIndex(text, -1); len(matches) > 0 {
		if len(matches) > planning.MaxGuidanceWindows {
			return unknownPlantingWindow(text)
		}
		windows := []planning.GuidanceWindow{}
		for i := 0; i < len(matches); i++ {
			from := text[matches[i][0]:matches[i][1]]
			to := from
			if i+1 < len(matches) && planningRangeRE.MatchString(text[matches[i][1]:matches[i+1][0]]) {
				i++
				to = text[matches[i][0]:matches[i][1]]
			}
			period := &planning.Period{Precision: planning.DateRangePrecision, Dates: &planning.DateRange{From: from, To: to}}
			if len(planning.ValidatePeriod(period)) > 0 {
				return unknownPlantingWindow(text)
			}
			windows = append(windows, planning.GuidanceWindow{Precision: "ABSOLUTE", Label: text, Absolute: period})
		}
		return windows
	}

	// A year attached to a textual date must not silently become annual advice.
	if planningYearRE.MatchString(text) {
		return unknownPlantingWindow(text)
	}
	hits := plantingMonthHits(text)
	for _, at := range seasonRE.FindAllStringSubmatchIndex(strings.ToLower(text), -1) {
		word := strings.ToUpper(text[at[4]:at[5]])
		if word == "FALL" {
			word = "AUTUMN"
		}
		hits = append(hits, plantingTimeHit{start: at[0], end: at[1], kind: "SEASON", season: planning.Season(word), label: text[at[0]:at[1]]})
	}
	// Stable tiny-list ordering also keeps month/season mixtures distinct.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].start < hits[j-1].start; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	if len(hits) == 0 || len(hits) > planning.MaxGuidanceWindows {
		return unknownPlantingWindow(text)
	}
	var hemi *string
	if hemisphere == "NORTHERN" || hemisphere == "SOUTHERN" {
		hemi = &hemisphere
	}
	windows := []planning.GuidanceWindow{}
	for i := 0; i < len(hits); i++ {
		first, last := hits[i], hits[i]
		if i+1 < len(hits) && planningRangeRE.MatchString(text[first.end:hits[i+1].start]) {
			last = hits[i+1]
			i++
			if first.kind != last.kind {
				return unknownPlantingWindow(text)
			}
		}
		label := text[first.start:last.end]
		if first.kind == "SEASON" {
			seasons := []planning.Season{planning.Spring, planning.Summer, planning.Autumn, planning.Winter}
			a, b := 0, 0
			for j, s := range seasons {
				if first.season == s {
					a = j
				}
				if last.season == s {
					b = j
				}
			}
			for j := a; ; j = (j + 1) % 4 {
				season := seasons[j]
				windows = append(windows, planning.GuidanceWindow{Precision: "ANNUAL", Label: label, Annual: &planning.AnnualPeriod{Precision: "SEASON", Season: &season}, Hemisphere: hemi})
				if j == b {
					break
				}
			}
		} else {
			annual := &planning.AnnualPeriod{Precision: first.kind}
			if first.kind == "DATE_RANGE" {
				annual.Dates = &planning.AnnualDateRange{From: &planning.MonthDay{Month: first.month, Day: first.day}, To: &planning.MonthDay{Month: last.month, Day: last.day}}
			} else {
				annual.Months = &planning.AnnualMonthRange{From: first.month, To: last.month}
			}
			if !planning.ValidAnnualPeriod(annual) {
				return unknownPlantingWindow(fmt.Sprintf("Review source timing: %s", text))
			}
			windows = append(windows, planning.GuidanceWindow{Precision: "ANNUAL", Label: label, Annual: annual, Hemisphere: hemi})
		}
	}
	if len(windows) > planning.MaxGuidanceWindows {
		return unknownPlantingWindow(text)
	}
	return windows
}

var plantingTrailingDays = regexp.MustCompile(`^\s*(?:to|through|[-–—])\s*(\d{1,2})(?:st|nd|rd|th)?\b`)

func plantingMonthHits(text string) []plantingTimeHit {
	hits := []plantingTimeHit{}
	for _, at := range planningMonthRE.FindAllStringIndex(text, -1) {
		label := text[at[0]:at[1]]
		if label == "may" {
			continue
		}
		hit := plantingTimeHit{start: at[0], end: at[1], kind: "MONTH_RANGE", month: monthNumber(label), label: label}
		if hit.end < len(text) && text[hit.end] == '.' {
			hit.end++
		}
		if day := planningDayRE.FindStringSubmatchIndex(text[hit.end:]); day != nil {
			number, _ := strconv.Atoi(text[hit.end+day[2] : hit.end+day[3]])
			hit.day = number
			hit.end += day[1]
			hit.kind = "DATE_RANGE"
		}
		hits = append(hits, hit)
		if hit.kind == "DATE_RANGE" {
			if tail := plantingTrailingDays.FindStringSubmatchIndex(text[hit.end:]); tail != nil {
				day, _ := strconv.Atoi(text[hit.end+tail[2] : hit.end+tail[3]])
				hits = append(hits, plantingTimeHit{start: hit.end + tail[2], end: hit.end + tail[3], kind: "DATE_RANGE", month: hit.month, day: day})
			}
		}
	}
	return hits
}

// Completeness includes day bounds and numeric relative intervals; matching
// month names alone must not accept a clipped date or frost-relative range.
func missingPlantingTiming(source, timing string) bool {
	if len(missingTiming(source, timing)) > 0 {
		return true
	}
	tokens := func(text string) map[string]bool {
		out := map[string]bool{}
		for _, hit := range plantingMonthHits(text) {
			out[fmt.Sprintf("m:%d:%d", hit.month, hit.day)] = true
		}
		for _, match := range planningRelativeRE.FindAllStringSubmatch(text, -1) {
			unit := strings.TrimSuffix(strings.ToLower(match[3]), "s")
			out["r:"+match[1]+":"+match[2]+":"+unit+":"+strings.ToLower(match[4])+":"+normalize(match[5])] = true
		}
		for _, date := range planningISODateRE.FindAllString(text, -1) {
			out["d:"+date] = true
		}
		return out
	}
	have := tokens(timing)
	for token := range tokens(source) {
		if !have[token] {
			return true
		}
	}
	return false
}

func relativeHasAdditionalWindow(remaining string) bool {
	if len(plantingMonthHits(remaining)) > 0 {
		return true
	}
	for _, at := range seasonRE.FindAllStringIndex(strings.ToLower(remaining), -1) {
		suffix := strings.TrimSpace(remaining[at[1]:])
		// "fall tomatoes" and "spring plantings" qualify the crop being timed;
		// the source's frost-relative interval remains the window. A standalone
		// "in spring or ..." is an additional window and cannot be discarded.
		words := strings.Fields(suffix)
		cropWords := strings.Join(words[:min(2, len(words))], " ")
		if regexp.MustCompile(`(?i)^(plantings?|crops?|transplants?|seedlings?|sowing)\b`).MatchString(suffix) || cropFor(cropWords, "", "") != "" {
			continue
		}
		return true
	}
	return false
}
