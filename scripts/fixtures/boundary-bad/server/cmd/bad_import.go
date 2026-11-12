package cmd

// Deliberately forbidden: cmd/ must not import the generated db package
// directly; it goes through internal packages. Used by boundary_check fixtures.
import _ "canopy/server/db/generated"
