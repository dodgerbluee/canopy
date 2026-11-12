package platform

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Clock and IDs are ports: services take these interfaces so tests control
// time and identity without touching globals.
type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type IDs interface{ New() pgtype.UUID }

type UUIDv4 struct{}

func (UUIDv4) New() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}
