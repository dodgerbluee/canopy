package knowledge

import (
	"regexp"
	"strings"
	"time"
)

var harvestVerbRE = regexp.MustCompile(`(?i)\b(ripen\w*|ripe|harvest(?:ed|ing)?|matur(?:e|es|ing))\b`)
var fruitSubjectRE = regexp.MustCompile(`(?i)\b(fruits?|crops?|berries|berry|variet(?:y|ies)|types)\b`)

// Audit explicit positive harvest statements, independently of the model's
// choice of facts. This catches an omitted second crop in a separate sentence.
// Relative/negative statements and mixed actions are not absolute schedules.
func harvestPassages(text string) []string {
	passages := []string{}
	seen := map[string]bool{}
	harvestHeading := false
	for _, sentence := range sentenceEndRE.Split(normalizeSourceText(text), -1) {
		sentence = strings.TrimSpace(sentence)
		fromHeading := harvestHeading
		harvestHeading = len(sentence) < 60 && regexp.MustCompile(`(?i)\bharvest\b`).MatchString(sentence) && len(timingHits(sentence)) == 0
		if len(sentence) > 500 || (!fromHeading && !harvestVerbRE.MatchString(sentence)) || unsafeTimingRE.MatchString(strings.ToLower(sentence)) || !phaseTimingMatches(sentence) {
			continue
		}
		// "Mature trees" is an age condition, not a fruit maturity statement.
		if !fromHeading && !regexp.MustCompile(`(?i)\b(ripen\w*|ripe|harvest(?:ed|ing)?)\b`).MatchString(sentence) && !fruitSubjectRE.MatchString(sentence) {
			continue
		}
		if len(timingHits(sentence)) == 0 || seen[sentence] {
			continue
		}
		seen[sentence] = true
		passages = append(passages, sentence)
	}
	return passages
}

func verifiedHarvestTiming(drafts []DraftFact, source Source, text string) string {
	var verified strings.Builder
	for _, draft := range drafts {
		if draft.Kind != "HARVEST" || draft.timingUnverified {
			continue
		}
		if _, err := ValidateFacts(source, text, []DraftFact{draft}, time.Now().UTC()); err != nil {
			continue
		}
		verified.WriteString(draft.Timing)
		verified.WriteByte(' ')
	}
	return verified.String()
}

func addsHarvestCoverage(passages []string, drafts []DraftFact, candidate DraftFact, source Source, text string) bool {
	wanted := map[string]bool{}
	for _, key := range missingTiming(strings.Join(passages, " "), verifiedHarvestTiming(drafts, source, text)) {
		wanted[key] = true
	}
	for _, hit := range timingHits(candidate.Timing) {
		if wanted[strings.TrimSpace(hit.key)] {
			return true
		}
	}
	return false
}

func uncoveredHarvest(passages []string, drafts []DraftFact, source Source, text string) []string {
	verified := verifiedHarvestTiming(drafts, source, text)
	missing := []string{}
	for _, passage := range passages {
		if len(missingTiming(passage, verified)) > 0 {
			missing = append(missing, passage)
		}
	}
	return missing
}
