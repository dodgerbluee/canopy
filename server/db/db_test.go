// Package db_test runs the T02.r1 data-contract acceptance suite against a real
// PostgreSQL 17 (PGTEST_DSN). Each test gets an isolated schema with the full
// migration applied, so constraint behavior is verified, not assumed.
package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
)

func mustPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("t02_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	// Apply only the Up sections (goose directives are SQL comments; a
	// raw exec would otherwise also run the Down sections).
	up, err := candb.AllUp()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	if _, err := admin.Exec(ctx, up); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	admin.Close(ctx)

	poolDSN := dsn + "&search_path=" + schema
	pool, err := pgxpool.New(ctx, poolDSN)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		a, err := pgx.Connect(ctx, dsn)
		if err == nil {
			_, _ = a.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			a.Close(ctx)
		}
	})
	return pool
}

func uid() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

func num(t *testing.T, s string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		t.Fatalf("numeric %q: %v", s, err)
	}
	return n
}

func day(y int, m time.Month, d int) pgtype.Date {
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

func ts(v time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: v, Valid: true}
}

type seed struct {
	pool                   *pgxpool.Pool
	ownerID, wsID, plantID pgtype.UUID
	q                      *dbgen.Queries
}

func seedWorld(t *testing.T) seed {
	t.Helper()
	pool := mustPool(t)
	q := dbgen.New(pool)
	ctx := context.Background()
	o, w, p := uid(), uid(), uid()
	if _, err := pool.Exec(ctx,
		"INSERT INTO owner (id, password_hash) VALUES ($1, 'x')", o); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if _, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: w, OwnerID: o, DisplayName: "Backyard", Timezone: "America/Los_Angeles",
		DefaultCurrency: "USD",
	}); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if _, err := q.InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: p, WorkspaceID: w, Name: "Meyer lemon",
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryCITRUS,
	}); err != nil {
		t.Fatalf("plant: %v", err)
	}
	return seed{pool: pool, ownerID: o, wsID: w, plantID: p, q: q}
}

func expectDBError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected constraint violation containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

// ---------- measurement kind/unit compatibility ----------

func TestMeasurementKindUnitMismatchRejected(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	_, err := s.q.InsertMeasurement(ctx, dbgen.InsertMeasurementParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		QuantityKind: dbgen.QuantityKindVOLUME, Unit: dbgen.MeasurementUnitCM,
		Value: num(t, "82.5"), ObservedAt: ts(time.Now()),
		Method: dbgen.MeasurementMethodMANUAL,
	})
	expectDBError(t, err, "check constraint")
}

func TestMeasurementValidAndExact(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	m, err := s.q.InsertMeasurement(ctx, dbgen.InsertMeasurementParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		QuantityKind: dbgen.QuantityKindLENGTH, Unit: dbgen.MeasurementUnitCM,
		Value: num(t, "82.5"), ObservedAt: ts(time.Now()),
		Method: dbgen.MeasurementMethodMANUAL,
	})
	if err != nil {
		t.Fatalf("valid measurement: %v", err)
	}
	var txt string
	if err := s.pool.QueryRow(ctx,
		"SELECT value::text FROM measurements WHERE id = $1", m.ID).Scan(&txt); err != nil {
		t.Fatal(err)
	}
	if txt != "82.5" {
		t.Fatalf("exact numeric round-trip failed: %s", txt)
	}
}

// ---------- activity quantity/unit rules ----------

func TestActivityQuantityUnitRules(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	base := dbgen.InsertActivityParams{
		WorkspaceID: s.wsID, PlantID: s.plantID,
		OccurredAt: ts(time.Now()), Timezone: "America/Los_Angeles",
	}

	bad := base
	bad.ID = uid()
	bad.Kind = dbgen.ActivityKindWATER
	bad.Quantity = ptrNum(num(t, "2"))
	bad.Unit = dbgen.NullMeasurementUnit{MeasurementUnit: dbgen.MeasurementUnitG, Valid: true}
	_, err := s.q.InsertActivity(ctx, bad)
	expectDBError(t, err, "check constraint") // water in grams

	bad2 := base
	bad2.ID = uid()
	bad2.Kind = dbgen.ActivityKindPRUNE
	bad2.Quantity = ptrNum(num(t, "1"))
	_, err = s.q.InsertActivity(ctx, bad2)
	expectDBError(t, err, "check constraint") // prune with quantity but no unit

	ok := base
	ok.ID = uid()
	ok.Kind = dbgen.ActivityKindWATER
	ok.Quantity = ptrNum(num(t, "2"))
	ok.Unit = dbgen.NullMeasurementUnit{MeasurementUnit: dbgen.MeasurementUnitL, Valid: true}
	if _, err := s.q.InsertActivity(ctx, ok); err != nil {
		t.Fatalf("water 2 L: %v", err)
	}

	ok2 := base
	ok2.ID = uid()
	ok2.Kind = dbgen.ActivityKindNOTE
	if _, err := s.q.InsertActivity(ctx, ok2); err != nil {
		t.Fatalf("note without quantity: %v", err)
	}
}

func ptrNum(n pgtype.Numeric) *pgtype.Numeric { return &n }

// ---------- purchase money invariants ----------

func TestPurchaseOverheadCannotExceedTotal(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	_, err := s.q.InsertPurchase(ctx, dbgen.InsertPurchaseParams{
		ID: uid(), WorkspaceID: s.wsID, Currency: "USD",
		TotalMinor: num(t, "100"), ShippingMinor: num(t, "60"), TaxMinor: num(t, "50"),
	})
	expectDBError(t, err, "check constraint")
}

func TestPurchaseNegativeAmountsRejected(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	_, err := s.q.InsertPurchase(ctx, dbgen.InsertPurchaseParams{
		ID: uid(), WorkspaceID: s.wsID, Currency: "USD", TotalMinor: num(t, "-1"),
		ShippingMinor: num(t, "0"), TaxMinor: num(t, "0"),
	})
	expectDBError(t, err, "check constraint")
}

// ---------- acquisition pricing-state invariants ----------

func TestAcquisitionPricingStateRules(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()

	_, err := s.q.InsertAcquisition(ctx, dbgen.InsertAcquisitionParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		PricingState: dbgen.PricingStateKNOWN,
	})
	expectDBError(t, err, "check constraint") // KNOWN requires allocation

	// UNKNOWN with an allocation id is also rejected.
	p, err := s.q.InsertPurchase(ctx, dbgen.InsertPurchaseParams{
		ID: uid(), WorkspaceID: s.wsID, Currency: "USD", TotalMinor: num(t, "4500"),
		ShippingMinor: num(t, "0"), TaxMinor: num(t, "0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	line, err := s.q.InsertPurchaseLine(ctx, dbgen.InsertPurchaseLineParams{
		ID: uid(), WorkspaceID: s.wsID, PurchaseID: p.ID,
		Description: "tree", Quantity: num(t, "1"), LineTotalMinor: num(t, "4500"),
	})
	if err != nil {
		t.Fatal(err)
	}
	alloc, err := s.q.InsertPurchaseAllocation(ctx, dbgen.InsertPurchaseAllocationParams{
		ID: uid(), WorkspaceID: s.wsID, PurchaseLineID: line.ID, PlantID: s.plantID,
		LineMinor: num(t, "4500"), TaxMinor: num(t, "0"), ShippingMinor: num(t, "0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.q.InsertAcquisition(ctx, dbgen.InsertAcquisitionParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		PricingState: dbgen.PricingStateUNKNOWN, InitialPurchaseAllocationID: alloc.ID,
	})
	expectDBError(t, err, "check constraint")
}

func TestDeferredAllocationFKInOneTransaction(t *testing.T) {
	// createPlant + initial purchase: acquisition references an allocation created
	// later in the same transaction; the FK is DEFERRABLE INITIALLY DEFERRED.
	s := seedWorld(t)
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	qtx := s.q.WithTx(tx)

	acq, err := qtx.InsertAcquisition(ctx, dbgen.InsertAcquisitionParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		PricingState:                dbgen.PricingStateKNOWN,
		InitialPurchaseAllocationID: uid(), // not yet inserted
	})
	if err != nil {
		t.Fatalf("deferred FK should defer the check: %v", err)
	}
	p, err := qtx.InsertPurchase(ctx, dbgen.InsertPurchaseParams{
		ID: uid(), WorkspaceID: s.wsID, Currency: "USD", TotalMinor: num(t, "4500"),
		ShippingMinor: num(t, "0"), TaxMinor: num(t, "0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	line, err := qtx.InsertPurchaseLine(ctx, dbgen.InsertPurchaseLineParams{
		ID: uid(), WorkspaceID: s.wsID, PurchaseID: p.ID,
		Description: "tree", Quantity: num(t, "1"), LineTotalMinor: num(t, "4500"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := qtx.InsertPurchaseAllocation(ctx, dbgen.InsertPurchaseAllocationParams{
		ID: acq.InitialPurchaseAllocationID, WorkspaceID: s.wsID,
		PurchaseLineID: line.ID, PlantID: s.plantID, LineMinor: num(t, "4500"),
		TaxMinor: num(t, "0"), ShippingMinor: num(t, "0"),
	}); err != nil {
		t.Fatalf("allocation: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit must satisfy deferred FK: %v", err)
	}
}

func TestOneAcquisitionPerPlant(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	p := func() {
		if _, err := s.q.InsertAcquisition(ctx, dbgen.InsertAcquisitionParams{
			ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
			PricingState: dbgen.PricingStateUNKNOWN,
		}); err != nil {
			t.Fatal(err)
		}
	}
	p()
	_, err := s.q.InsertAcquisition(ctx, dbgen.InsertAcquisitionParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		PricingState: dbgen.PricingStateUNKNOWN,
	})
	expectDBError(t, err, "duplicate key")
}

// ---------- media invariants ----------

func TestOneCoverPhotoPerPlant(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	mk := func(cover bool) {
		a, err := s.q.InsertAsset(ctx, dbgen.InsertAssetParams{
			ID: uid(), WorkspaceID: s.wsID, Purpose: dbgen.AssetPurposePHOTO,
			ByteCount: 1000, Sha256: strings.Repeat("a", 64),
			StorageKey: uuid.NewString(), DisplayName: pgtype.Text{String: "x.jpg", Valid: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.q.InsertPhoto(ctx, dbgen.InsertPhotoParams{
			ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID, AssetID: a.ID,
			DateSource: dbgen.PhotoDateSourceUPLOADFALLBACK, Viewpoint: dbgen.PhotoViewpointWHOLE,
			IsCover: cover,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	mk(true)
	a, err := s.q.InsertAsset(ctx, dbgen.InsertAssetParams{
		ID: uid(), WorkspaceID: s.wsID, Purpose: dbgen.AssetPurposePHOTO,
		ByteCount: 1000, Sha256: strings.Repeat("b", 64),
		StorageKey: uuid.NewString(), DisplayName: pgtype.Text{String: "y.jpg", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.q.InsertPhoto(ctx, dbgen.InsertPhotoParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID, AssetID: a.ID,
		DateSource: dbgen.PhotoDateSourceUPLOADFALLBACK, Viewpoint: dbgen.PhotoViewpointWHOLE,
		IsCover: true,
	})
	expectDBError(t, err, "duplicate key")
}

func TestAssetSha256Shape(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	_, err := s.q.InsertAsset(ctx, dbgen.InsertAssetParams{
		ID: uid(), WorkspaceID: s.wsID, Purpose: dbgen.AssetPurposePHOTO,
		ByteCount: 1, Sha256: "not-a-hash", StorageKey: uuid.NewString(),
	})
	expectDBError(t, err, "check constraint")
}

// ---------- tag invariants ----------

func TestTagLabelNormalizedUnique(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	if _, err := s.q.InsertTag(ctx, dbgen.InsertTagParams{ID: uid(), WorkspaceID: s.wsID, Label: "Fruiting"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.q.InsertTag(ctx, dbgen.InsertTagParams{ID: uid(), WorkspaceID: s.wsID, Label: "fruiting"})
	expectDBError(t, err, "duplicate key")

	// archived tags free the label for a new live tag
	tag, err := s.q.GetTagByLabel(ctx, dbgen.GetTagByLabelParams{WorkspaceID: s.wsID, Label: "Fruiting"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.q.ArchiveTag(ctx, dbgen.ArchiveTagParams{
		ID: tag.ID, WorkspaceID: s.wsID, Revision: tag.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.q.InsertTag(ctx, dbgen.InsertTagParams{ID: uid(), WorkspaceID: s.wsID, Label: "Fruiting"}); err != nil {
		t.Fatalf("recreate after archive: %v", err)
	}
}

// ---------- journal projection invariants ----------

func TestJournalEntryUniqueAndReprojection(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	act, err := s.q.InsertActivity(ctx, dbgen.InsertActivityParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID, Kind: dbgen.ActivityKindWATER,
		OccurredAt: ts(time.Now()), Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	e1, err := s.q.InsertJournalEntry(ctx, dbgen.InsertJournalEntryParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		EntryType: dbgen.JournalEntryTypeACTIVITY, SourceID: act.ID,
		OccurredAt: act.OccurredAt, Summary: "Watered",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.q.InsertJournalEntry(ctx, dbgen.InsertJournalEntryParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		EntryType: dbgen.JournalEntryTypeACTIVITY, SourceID: act.ID,
		OccurredAt: act.OccurredAt, Summary: "Watered again",
	})
	expectDBError(t, err, "duplicate key")

	// upsert path re-projects without duplicating
	e2, err := s.q.UpsertJournalEntry(ctx, dbgen.UpsertJournalEntryParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		EntryType: dbgen.JournalEntryTypeACTIVITY, SourceID: act.ID,
		OccurredAt: ts(act.OccurredAt.Time.Add(time.Hour)), Summary: "Watered (edited)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e2.ID != e1.ID || e2.Summary != "Watered (edited)" {
		t.Fatalf("reprojection must keep one row and update it: %+v", e2)
	}
	rows, err := s.q.ListJournal(ctx, dbgen.ListJournalParams{
		WorkspaceID: s.wsID, LimitCount: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 journal row, got %d", len(rows))
	}
}

// ---------- optimistic concurrency ----------

func TestRevisionGuard(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	_, err := s.q.UpdatePlant(ctx, dbgen.UpdatePlantParams{
		ID: s.plantID, WorkspaceID: s.wsID, ExpectedRevision: 99,
		Name: pgtype.Text{String: "Renamed", Valid: true},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale revision must update zero rows, got %v", err)
	}
	p, err := s.q.UpdatePlant(ctx, dbgen.UpdatePlantParams{
		ID: s.plantID, WorkspaceID: s.wsID, ExpectedRevision: 1,
		Name: pgtype.Text{String: "Renamed", Valid: true},
	})
	if err != nil {
		t.Fatalf("current revision: %v", err)
	}
	if p.Revision != 2 || p.Name != "Renamed" {
		t.Fatalf("revision bump failed: %+v", p)
	}
	if !p.UpdatedAt.Time.After(p.CreatedAt.Time) {
		t.Fatal("updated_at trigger did not advance")
	}
}

// ---------- idempotency receipts ----------

func TestMutationReceiptIdempotency(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	body, _ := json.Marshal(map[string]any{"ok": true})
	params := dbgen.InsertMutationReceiptParams{
		ID: uid(), WorkspaceID: s.wsID, Operation: "createPlant",
		IdempotencyKey: "cmid-1", RequestHash: strings.Repeat("c", 64),
		Result: body, ExpiresAt: ts(time.Now().Add(24 * time.Hour)),
	}
	first, err := s.q.InsertMutationReceipt(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{
		ID: uid(), WorkspaceID: s.wsID, Operation: "createPlant",
		IdempotencyKey: "cmid-1", RequestHash: strings.Repeat("d", 64),
		Result: body, ExpiresAt: ts(time.Now().Add(24 * time.Hour)),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("duplicate key must return no row (DO NOTHING), got %v", err)
	}
	_ = second
	got, err := s.q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{
		WorkspaceID: s.wsID, Operation: "createPlant", IdempotencyKey: "cmid-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID {
		t.Fatal("replay must see the original receipt")
	}
}

// ---------- misc invariants ----------

func TestLocationSelfParentRejected(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	id := uid()
	_, err := s.q.InsertLocation(ctx, dbgen.InsertLocationParams{
		ID: id, WorkspaceID: s.wsID, Name: "Loop", ParentID: id,
		Situation: dbgen.LocationSituationINDOOR,
	})
	expectDBError(t, err, "check constraint")
}

func TestTextFieldLimits(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	_, err := s.q.InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: uid(), WorkspaceID: s.wsID, Name: strings.Repeat("n", 161),
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
	})
	expectDBError(t, err, "check constraint")
}

func TestPlantCountMustBePositive(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	_, err := s.q.InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: uid(), WorkspaceID: s.wsID, Name: "Group",
		Type: dbgen.PlantTypeGROUP, Count: pgtype.Int4{Int32: 0, Valid: true},
		Category: dbgen.PlantCategoryBLACKBERRY,
	})
	expectDBError(t, err, "check constraint")
}

func TestSessionTokenHashUnique(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	p := func() {
		if _, err := s.q.InsertSession(ctx, dbgen.InsertSessionParams{
			ID: uid(), OwnerID: s.ownerID, TokenHash: "same", CsrfToken: uuid.NewString(),
			ExpiresAt: ts(time.Now().Add(24 * time.Hour)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	p()
	_, err := s.q.InsertSession(ctx, dbgen.InsertSessionParams{
		ID: uid(), OwnerID: s.ownerID, TokenHash: "same", CsrfToken: uuid.NewString(),
		ExpiresAt: ts(time.Now().Add(24 * time.Hour)),
	})
	expectDBError(t, err, "duplicate key")
}

func TestPrivacyEpochBump(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	w, err := s.q.GetWorkspace(ctx, s.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if w.PrivacyEpoch != 0 {
		t.Fatalf("initial epoch: %d", w.PrivacyEpoch)
	}
	n, err := s.q.BumpPrivacyEpoch(ctx, s.wsID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("epoch bump: %d", n)
	}
}

func TestLatestActivitiesForPlantsBounded(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	// 15 activities on the seeded plant, 3 on a second plant
	p2, err := s.q.InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: uid(), WorkspaceID: s.wsID, Name: "Fig",
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryFIG,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := 0; i < 15; i++ {
		if _, err := s.q.InsertActivity(ctx, dbgen.InsertActivityParams{
			ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID, Kind: dbgen.ActivityKindWATER,
			OccurredAt: ts(base.Add(time.Duration(i) * time.Hour)), Timezone: "UTC",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.q.InsertActivity(ctx, dbgen.InsertActivityParams{
			ID: uid(), WorkspaceID: s.wsID, PlantID: p2.ID, Kind: dbgen.ActivityKindWATER,
			OccurredAt: ts(base.Add(time.Duration(i) * time.Hour)), Timezone: "UTC",
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.q.LatestActivitiesForPlants(ctx, dbgen.LatestActivitiesForPlantsParams{
		WorkspaceID: s.wsID, PlantIds: []pgtype.UUID{s.plantID, p2.ID}, PerPlant: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 15 { // 12 + 3
		t.Fatalf("expected 12+3 rows, got %d", len(rows))
	}
}

func TestSpendingAggregationExcludesVoided(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	mk := func(total string, voided bool) {
		p, err := s.q.InsertPurchase(ctx, dbgen.InsertPurchaseParams{
			ID: uid(), WorkspaceID: s.wsID, Currency: "USD", TotalMinor: num(t, total),
			ShippingMinor: num(t, "0"), TaxMinor: num(t, "0"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if voided {
			if _, err := s.q.VoidPurchase(ctx, dbgen.VoidPurchaseParams{
				ID: p.ID, WorkspaceID: s.wsID, Revision: p.Revision,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("1000", false)
	mk("2000", false)
	mk("500", true)
	rows, err := s.q.SpendingByCurrency(ctx, dbgen.SpendingByCurrencyParams{WorkspaceID: s.wsID})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one currency bucket, got %+v", rows)
	}
	f, err := rows[0].TotalMinor.Float64Value()
	if err != nil || f.Float64 != 3000 {
		t.Fatalf("voided purchase leaked into total: %+v (err %v)", rows, err)
	}
}
