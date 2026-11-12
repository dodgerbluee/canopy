package identity

import (
	dbgen "canopy/server/db/generated"
)

// NewQueries is the composition shim that lets cmd/** reach the generated
// query surface without importing db/generated directly (boundary rule:
// generated code is imported only from server/db/** and server/internal/**).
// *pgxpool.Pool satisfies dbgen.DBTX.
func NewQueries(db dbgen.DBTX) *dbgen.Queries {
	return dbgen.New(db)
}
