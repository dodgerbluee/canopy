package processor

import (
	dbgen "canopy/server/db/generated"
	"context"
	"github.com/riverqueue/river"
	"testing"
)

func TestProcessedQualityAndBackfill(t *testing.T) {
	e := newWorkerEnv(t)
	id := e.newAsset(t, "iphone-12mp.heic", dbgen.AssetPurposePHOTO)
	if err := e.run(t, id); err != nil {
		t.Fatal(err)
	}
	a := e.asset(t, id)
	if !a.CoverQuality.Valid || a.CoverQualityVersion.Int16 != 1 {
		t.Fatalf("processed quality missing: %+v", a.CoverQuality)
	}
	original := a.CoverQuality.Float64
	if _, err := e.pool.Exec(context.Background(), `UPDATE assets SET cover_quality=NULL,cover_quality_version=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	w := CoverQualityWorker{Pool: e.pool, Store: e.store}
	for i := 0; i < 2; i++ {
		if err := w.Work(context.Background(), &river.Job[CoverQualityArgs]{}); err != nil {
			t.Fatal(err)
		}
	}
	a = e.asset(t, id)
	if a.State != dbgen.AssetStateREADY || !a.CoverQuality.Valid || a.CoverQuality.Float64 != original || a.CoverQualityVersion.Int16 != 1 {
		t.Fatalf("backfill did not converge: %+v", a.CoverQuality)
	}
}
