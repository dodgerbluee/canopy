package knowledge

import "strings"

// Reviewed multi-crop adapters keep neighboring crop sections out of both the
// model prompt and deterministic extraction. The publication hash still covers
// the fetched original, and every retained quote also exists in that original.
func planningSourceText(source Source, text string) string {
	if source.ID != "osu-tomato" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "Tomatillos") {
			return strings.Join(lines[:i], "\n")
		}
	}
	// The reviewed boundary must still exist after publisher layout changes.
	// Returning no section is safer than mixing guidance for a neighboring crop.
	return ""
}
