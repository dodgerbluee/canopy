package media

import (
	"strings"
	"unicode"
)

// SanitizeFilename turns a client-supplied name into a safe display
// string only: last path segment, control characters removed,
// whitespace collapsed, capped at 200 runes. The result is never used
// as a path or storage key; nil means "no usable name".
func SanitizeFilename(name string) *string {
	// last segment for both POSIX and Windows separators
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || name == "." || name == ".." {
		return nil
	}
	r := []rune(name)
	if len(r) > 200 {
		name = string(r[:200])
	}
	return &name
}
