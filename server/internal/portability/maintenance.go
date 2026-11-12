package portability

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
)

// IsMaintenance reports whether the stack is quiesced. A missing row or
// query error reads as not-maintenance: the flag only ever blocks more
// when it is positively set.
func IsMaintenance(ctx context.Context, pool *pgxpool.Pool) bool {
	on, err := dbgen.New(pool).GetMaintenanceFlag(ctx)
	return err == nil && on
}

// SetMaintenance flips the quiesce flag. A backup sets it for the
// duration of the dump (writes/uploads refused, queue paused, running
// jobs drained) and always clears it afterwards, even on failure —
// "failed backup/unquiesce" is an explicit verification case.
func SetMaintenance(ctx context.Context, pool *pgxpool.Pool, on bool) error {
	_, err := dbgen.New(pool).SetMaintenanceFlag(ctx, on)
	return err
}
