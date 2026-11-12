package knowledge

import "strings"

// Preserve meaningful paragraph/list/table boundaries while normalizing text
// within each block. Flattening an entire page made unrelated weather, soil,
// irrigation and harvest rows look like one supporting sentence.
func normalizeSourceText(text string) string {
	blocks := []string{}
	for _, line := range strings.Split(text, "\n") {
		if normalized := strings.Join(strings.Fields(line), " "); normalized != "" {
			blocks = append(blocks, normalized)
		}
	}
	return strings.Join(blocks, "\n")
}

func sourceBlock(tag string) bool {
	switch tag {
	case "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6", "table", "tr", "td", "th", "br", "article", "section", "ul", "ol", "dl", "dt", "dd":
		return true
	}
	return false
}
