// Package db embeds the goose migrations so cmd/migrate is self-contained.
package db

import (
	"embed"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var Migrations embed.FS

// AllUp returns the Up sections of every migration in filename order.
// Disposable test schemas and the e2e bootstrap use this so they always
// carry the full accepted schema, never a hand-picked single file.
func AllUp() (string, error) {
	ents, err := Migrations.ReadDir("migrations")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		raw, err := Migrations.ReadFile("migrations/" + n)
		if err != nil {
			return "", err
		}
		up, _, found := strings.Cut(string(raw), "-- +goose Down")
		if !found {
			return "", &MigrationError{Name: n}
		}
		b.WriteString(up)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// MigrationError reports a migration file without a goose Down marker.
type MigrationError struct{ Name string }

func (e *MigrationError) Error() string {
	return "migration " + e.Name + " missing goose Down marker"
}
