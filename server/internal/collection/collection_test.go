package collection

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// scratch builds a disposable migrated schema and returns pool + actor.
// Same discipline as the T06/T07/T08 suites: throwaway schema per test,
// dropped after use.
func scratch(t *testing.T) (context.Context, *pgxpool.Pool, Actor) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("t09_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			c.Close(context.Background())
		}
	})
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

	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "x"})
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: ids.New(), OwnerID: owner.ID, DisplayName: "T09", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return ctx, pool, Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: ids.New()}
}

// secondWorkspace adds an isolated workspace to the same schema; its rows
// must never be reachable from the primary actor.
func secondWorkspace(t *testing.T, pool *pgxpool.Pool) Actor {
	t.Helper()
	ctx := context.Background()
	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "x"})
	if err != nil {
		t.Fatalf("owner2: %v", err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: ids.New(), OwnerID: owner.ID, DisplayName: "Other", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("workspace2: %v", err)
	}
	return Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: ids.New()}
}

func svc(pool *pgxpool.Pool) *Service {
	return New(pool, slog.New(slog.DiscardHandler), nil)
}

func ptr[T any](v T) *T { return &v }

func mustDate(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

func hasCode(ps []Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}

func mustCreatePlant(t *testing.T, s *Service, ctx context.Context, a Actor, name string) PlantResult {
	t.Helper()
	res, problems, err := s.CreatePlant(ctx, a, CreatePlantInput{Name: name, Type: "INDIVIDUAL", Category: "OTHER"}, nil)
	if err != nil {
		t.Fatalf("createPlant %q: %v", name, err)
	}
	if len(problems) > 0 {
		t.Fatalf("createPlant %q problems: %+v", name, problems)
	}
	return res
}

// --- plant aggregate -------------------------------------------------------

func TestCreatePlantUnknownProvenance(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	q := dbgen.New(pool)

	seller := "estate sale"
	res, problems, err := s.CreatePlant(ctx, a, CreatePlantInput{
		Name:        "Monstera",
		Type:        "INDIVIDUAL",
		Category:    "OTHER",
		Acquisition: &AcquisitionInput{Seller: &seller, PricingState: "UNKNOWN", AcquiredOn: mustDate("2024-05-01")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("problems: %+v", problems)
	}

	plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: mustParse(t, res.ID), WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatalf("plant readback: %v", err)
	}
	// Unknowns stay unknown: nothing was invented.
	if plant.AcquiredOn.Valid && plant.AcquiredOn.Time.Format("2006-01-02") != "2024-05-01" {
		t.Fatalf("acquired_on projection wrong: %v", plant.AcquiredOn)
	}
	if plant.ScientificName.Valid || plant.Cultivar.Valid || plant.Count.Valid || plant.PlantedOn.Valid {
		t.Fatalf("unknown fields must stay null: %+v", plant)
	}
	acq, err := q.GetAcquisitionByPlant(ctx, dbgen.GetAcquisitionByPlantParams{PlantID: mustParse(t, res.ID), WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatalf("acquisition readback: %v", err)
	}
	if acq.PricingState != dbgen.PricingStateUNKNOWN {
		t.Fatalf("pricing state: %v", acq.PricingState)
	}
	if !acq.Seller.Valid || acq.Seller.String != seller {
		t.Fatalf("seller lost: %+v", acq)
	}
	// Audit trail written with the session id as request id.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_revisions WHERE workspace_id=$1 AND action='createPlant'`, a.WorkspaceID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows=%d err=%v", n, err)
	}
}

func TestTreeIdentityDistinct(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	r1 := mustCreatePlant(t, s, ctx, a, "Fern")
	r2 := mustCreatePlant(t, s, ctx, a, "Fern")
	if r1.ID == r2.ID {
		t.Fatalf("same-name plants share identity: %s", r1.ID)
	}
}

func TestPricingBackedAcquisitionNeedsPurchase(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	_, problems, err := s.CreatePlant(ctx, a, CreatePlantInput{
		Name: "Priced", Type: "INDIVIDUAL", Category: "OTHER",
		Acquisition: &AcquisitionInput{PricingState: "KNOWN"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// T11 shipped pricing: KNOWN is accepted only with initialPurchase.
	if !hasCode(problems, CodeValidation) {
		t.Fatalf("want VALIDATION for KNOWN without initialPurchase, got %+v", problems)
	}
	// Nothing half-written.
	if _, _, err := s.CreatePlant(ctx, a, CreatePlantInput{Name: "Priced", Type: "INDIVIDUAL", Category: "OTHER"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStaleUpdateConflict(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	p := mustCreatePlant(t, s, ctx, a, "Race")
	name := "v2"
	if _, problems, err := s.UpdatePlant(ctx, a, UpdatePlantInput{ID: p.ID, ExpectedRevision: 1, Name: &name}, nil); err != nil || len(problems) > 0 {
		t.Fatalf("first update: %v %+v", err, problems)
	}
	// Second writer still believes revision 1.
	other := "v3"
	_, problems, err := s.UpdatePlant(ctx, a, UpdatePlantInput{ID: p.ID, ExpectedRevision: 1, Name: &other}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Code != CodeConflict || problems[0].CurrentRevision == nil || *problems[0].CurrentRevision != 2 {
		t.Fatalf("want CONFLICT currentRevision=2, got %+v", problems)
	}
}

func TestArchiveRestoreHistoryPersists(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	q := dbgen.New(pool)

	loc, problems, err := s.CreateLocation(ctx, a, CreateLocationInput{Name: "Window", Situation: "INDOOR"}, nil)
	if err != nil || len(problems) > 0 {
		t.Fatalf("location: %v %+v", err, problems)
	}
	p := mustCreatePlant(t, s, ctx, a, "Fig")
	rev := int32(1)
	for _, note := range []string{"first", "second"} {
		n := note
		_, problems, err := s.SetPlantContext(ctx, a, SetPlantContextInput{
			PlantID: p.ID, ExpectedRevision: rev,
			Context: &ContextInput{LocationID: &loc.ID, Placement: "CONTAINER", MediumNote: &n},
		}, nil)
		if err != nil || len(problems) > 0 {
			t.Fatalf("setContext %s: %v %+v", note, err, problems)
		}
		rev++
	}
	// Archive (revision is now 3).
	if _, problems, err := s.ArchivePlant(ctx, a, p.ID, 3, nil); err != nil || len(problems) > 0 {
		t.Fatalf("archive: %v %+v", err, problems)
	}
	plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: mustParse(t, p.ID), WorkspaceID: a.WorkspaceID})
	if err != nil || plant.Status != dbgen.PlantStatusARCHIVED {
		t.Fatalf("status after archive: %+v err=%v", plant, err)
	}
	// Read history survives archiving: both context rows remain, current
	// context still resolves.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plant_contexts WHERE plant_id=$1`, mustParse(t, p.ID)).Scan(&n); err != nil || n != 2 {
		t.Fatalf("context history rows=%d err=%v", n, err)
	}
	pc, err := s.GetCurrentContext(ctx, mustParse(t, p.ID))
	if err != nil || !pc.LocationID.Valid || uuidString(pc.LocationID) != loc.ID {
		t.Fatalf("current context after archive: %+v err=%v", pc, err)
	}
	// Restore returns it to ACTIVE.
	if _, problems, err := s.RestorePlant(ctx, a, p.ID, 4, nil); err != nil || len(problems) > 0 {
		t.Fatalf("restore: %v %+v", err, problems)
	}
}

func TestPurgePlant(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	p := mustCreatePlant(t, s, ctx, a, "Doomed")

	if _, problems, err := s.PurgePlant(ctx, a, p.ID, 1, "kinda", nil); err != nil || !hasCode(problems, CodeValidation) {
		t.Fatalf("wrong confirm phrase must be refused: %v %+v", err, problems)
	}
	// A purchase allocation no longer blocks the purge (T38.a deletion
	// matrix): the reference becomes a deleted-plant marker and the
	// money stays on the books.
	ids := platform.UUIDv4{}
	purID, lineID, allocID := ids.New(), ids.New(), ids.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchases (id, workspace_id, currency, total_minor) VALUES ($1,$2,'USD',100)`,
		purID, a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchase_lines (id, workspace_id, purchase_id, description, quantity, line_total_minor)
		 VALUES ($1,$2,$3,'soil',1,100)`, lineID, a.WorkspaceID, purID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchase_allocations (id, workspace_id, purchase_line_id, plant_id, line_minor)
		 VALUES ($1,$2,$3,$4,100)`, allocID, a.WorkspaceID, lineID, mustParse(t, p.ID)); err != nil {
		t.Fatal(err)
	}
	if _, problems, err := s.PurgePlant(ctx, a, p.ID, 1, "PERMANENTLY_DELETE_PLANT", nil); err != nil || len(problems) > 0 {
		t.Fatalf("purge with allocations must succeed under the deletion matrix: %v %+v", err, problems)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plants WHERE id=$1`, mustParse(t, p.ID)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plant still present: %d %v", n, err)
	}
	var plantNull bool
	var lineMinor int
	if err := pool.QueryRow(ctx,
		`SELECT plant_id IS NULL, line_minor FROM purchase_allocations WHERE id=$1`, allocID,
	).Scan(&plantNull, &lineMinor); err != nil {
		t.Fatal(err)
	}
	if !plantNull || lineMinor != 100 {
		t.Fatalf("allocation must survive as deleted-plant marker with amounts: null=%v minor=%d", plantNull, lineMinor)
	}
}

// --- idempotency ------------------------------------------------------------

func TestIdempotentReplay(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	in := CreatePlantInput{Name: "Replayed", Type: "INDIVIDUAL", Category: "OTHER"}
	cmid := "cm-1"
	r1, problems, err := s.CreatePlant(ctx, a, in, &cmid)
	if err != nil || len(problems) > 0 {
		t.Fatalf("first: %v %+v", err, problems)
	}
	r2, problems, err := s.CreatePlant(ctx, a, in, &cmid)
	if err != nil || len(problems) > 0 {
		t.Fatalf("replay: %v %+v", err, problems)
	}
	if r1.ID != r2.ID {
		t.Fatalf("replay created a second plant: %s vs %s", r1.ID, r2.ID)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plants WHERE name='Replayed'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("plants=%d err=%v", n, err)
	}
	// Same key, different body: honest conflict, never a silent overwrite.
	other := CreatePlantInput{Name: "Different", Type: "INDIVIDUAL", Category: "OTHER"}
	_, problems, err = s.CreatePlant(ctx, a, other, &cmid)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(problems, CodeIdempotencyConflict) {
		t.Fatalf("want IDEMPOTENCY_CONFLICT, got %+v", problems)
	}
}

// --- locations ---------------------------------------------------------------

func TestLocationDepthAndCycle(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)

	// Chain of five is fine.
	var chain []LocationResult
	parent := (*string)(nil)
	for i := 0; i < 5; i++ {
		res, problems, err := s.CreateLocation(ctx, a, CreateLocationInput{
			Name: fmt.Sprintf("L%d", i), ParentLocation: parent, Situation: "INDOOR",
		}, nil)
		if err != nil || len(problems) > 0 {
			t.Fatalf("depth %d: %v %+v", i, err, problems)
		}
		chain = append(chain, res)
		p := res.ID
		parent = &p
	}
	// Sixth would exceed depth 5.
	if _, problems, err := s.CreateLocation(ctx, a, CreateLocationInput{
		Name: "L6", ParentLocation: parent, Situation: "INDOOR",
	}, nil); err != nil || !hasCode(problems, CodeValidation) {
		t.Fatalf("depth 6 must be refused: %v %+v", err, problems)
	}
	// Cycle: re-parent L0 under L2 (its own descendant).
	l2 := chain[2].ID
	if _, problems, err := s.UpdateLocation(ctx, a, UpdateLocationInput{
		ID: chain[0].ID, ExpectedRevision: 1, ParentLocation: &l2, ParentLocationPresent: true,
	}, nil); err != nil || !hasCode(problems, CodeValidation) {
		t.Fatalf("cycle must be refused: %v %+v", err, problems)
	}
	// Self-parent.
	self := chain[1].ID
	if _, problems, err := s.UpdateLocation(ctx, a, UpdateLocationInput{
		ID: self, ExpectedRevision: 1, ParentLocation: &self, ParentLocationPresent: true,
	}, nil); err != nil || !hasCode(problems, CodeValidation) {
		t.Fatalf("self-parent must be refused: %v %+v", err, problems)
	}
	// Moving L2 (subtree depth 3) under a fresh root keeps depth 4: allowed.
	root2, problems, err := s.CreateLocation(ctx, a, CreateLocationInput{Name: "R2", Situation: "OUTDOOR"}, nil)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	if _, problems, err := s.UpdateLocation(ctx, a, UpdateLocationInput{
		ID: chain[2].ID, ExpectedRevision: 1, ParentLocation: &root2.ID, ParentLocationPresent: true,
	}, nil); err != nil || len(problems) > 0 {
		t.Fatalf("legal re-parent refused: %v %+v", err, problems)
	}
}

func TestLocationPresenceSemantics(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	q := dbgen.New(pool)

	l1, problems, err := s.CreateLocation(ctx, a, CreateLocationInput{
		Name: "Shelf", Situation: "INDOOR", Label: ptr("living room"),
	}, nil)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	// Omitted parent/label keep current values (the frozen query always
	// re-assigns them).
	name := "Shelf Renamed"
	if _, problems, err := s.UpdateLocation(ctx, a, UpdateLocationInput{
		ID: l1.ID, ExpectedRevision: 1, Name: &name,
	}, nil); err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	row, err := q.GetLocation(ctx, dbgen.GetLocationParams{ID: mustParse(t, l1.ID), WorkspaceID: a.WorkspaceID})
	if err != nil || !row.Label.Valid || row.Label.String != "living room" {
		t.Fatalf("omitted label must persist: %+v err=%v", row, err)
	}
	// Explicit null clears.
	if _, problems, err := s.UpdateLocation(ctx, a, UpdateLocationInput{
		ID: l1.ID, ExpectedRevision: 2, LabelPresent: true, Label: nil,
	}, nil); err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	row, err = q.GetLocation(ctx, dbgen.GetLocationParams{ID: mustParse(t, l1.ID), WorkspaceID: a.WorkspaceID})
	if err != nil || row.Label.Valid {
		t.Fatalf("explicit null label must clear: %+v err=%v", row, err)
	}
}

func TestLocationCoordinatesRange(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	var n pgtype.Numeric
	if err := n.Scan("91"); err != nil {
		t.Fatal(err)
	}
	if _, problems, err := s.CreateLocation(ctx, a, CreateLocationInput{
		Name: "Pole+", Situation: "OUTDOOR", Latitude: &n,
	}, nil); err != nil || !hasCode(problems, CodeValidation) {
		t.Fatalf("lat 91 must be refused: %v %+v", err, problems)
	}
}

// --- tags ---------------------------------------------------------------------

func TestTagLifecycle(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	tag, problems, err := s.CreateTag(ctx, a, "Ferns", nil)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	if _, problems, err := s.CreateTag(ctx, a, " ferns ", nil); err != nil || !hasCode(problems, CodeConflict) {
		t.Fatalf("case-insensitive duplicate must conflict: %v %+v", err, problems)
	}
	if _, problems, err := s.UpdateTag(ctx, a, tag.ID, 1, "Ferns ", nil); err != nil || len(problems) > 0 {
		t.Fatalf("rename to same label (self) must be allowed: %v %+v", err, problems)
	}
	if _, problems, err := s.UpdateTag(ctx, a, tag.ID, 2, "Other", nil); err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	if _, problems, err := s.ArchiveTag(ctx, a, tag.ID, 3, nil); err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	if _, problems, err := s.ArchiveTag(ctx, a, tag.ID, 4, nil); err != nil || !hasCode(problems, CodeConflict) {
		t.Fatalf("double archive must conflict: %v %+v", err, problems)
	}
}

func TestSetPlantTagsRejectsForeignTag(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	other := secondWorkspace(t, pool)
	foreign, problems, err := s.CreateTag(ctx, other, "NotMine", nil)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	p := mustCreatePlant(t, s, ctx, a, "Tagged")
	if _, problems, err := s.SetPlantTags(ctx, a, SetPlantTagsInput{
		PlantID: p.ID, ExpectedRevision: 1, TagIDs: []string{foreign.ID},
	}, nil); err != nil || !hasCode(problems, CodeNotFound) {
		t.Fatalf("cross-workspace tag must be NOT_FOUND: %v %+v", err, problems)
	}
}

// --- context cross-workspace ---------------------------------------------------

func TestCrossWorkspaceContextDenied(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	other := secondWorkspace(t, pool)
	foreignLoc, problems, err := s.CreateLocation(ctx, other, CreateLocationInput{Name: "Their shelf", Situation: "INDOOR"}, nil)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	p := mustCreatePlant(t, s, ctx, a, "Mine")
	_, problems, err = s.SetPlantContext(ctx, a, SetPlantContextInput{
		PlantID: p.ID, ExpectedRevision: 1,
		Context: &ContextInput{LocationID: &foreignLoc.ID, Placement: "CONTAINER"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(problems, CodeNotFound) {
		t.Fatalf("foreign location must be NOT_FOUND, got %+v", problems)
	}
	// And the plant's own list view never shows the other workspace's rows.
	page, err := s.ListPlants(ctx, other, PlantQuery{Order: OrderNameAsc})
	if err != nil || len(page.Rows) != 0 {
		t.Fatalf("other workspace sees my plants: %+v err=%v", page.Rows, err)
	}
}

// --- acquisitions ---------------------------------------------------------------

func TestAcquisitionRecordUpdateProjection(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	q := dbgen.New(pool)
	p := mustCreatePlant(t, s, ctx, a, "Orchid")

	seller := "market stall"
	res, problems, err := s.RecordAcquisition(ctx, a, AcquisitionInput{
		Seller: &seller, PricingState: "UNKNOWN", AcquiredOn: mustDate("2023-02-14"),
	}, p.ID, nil, nil)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	// Projection mirrored onto the plant.
	plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: mustParse(t, p.ID), WorkspaceID: a.WorkspaceID})
	if err != nil || !plant.AcquiredOn.Valid || plant.AcquiredOn.Time.Format("2006-01-02") != "2023-02-14" {
		t.Fatalf("projection: %+v err=%v", plant, err)
	}
	// One acquisition per plant.
	if _, problems, err := s.RecordAcquisition(ctx, a, AcquisitionInput{PricingState: "UNKNOWN"}, p.ID, nil, nil); err != nil || !hasCode(problems, CodeConflict) {
		t.Fatalf("duplicate acquisition must conflict: %v %+v", err, problems)
	}
	// Update keeps the projection in step.
	newOn := mustDate("2023-03-01")
	if _, problems, err := s.UpdateAcquisition(ctx, a, UpdateAcquisitionInput{
		ID: res.ID, ExpectedRevision: 1, AcquiredOn: newOn,
	}, nil); err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	plant, err = q.GetPlant(ctx, dbgen.GetPlantParams{ID: mustParse(t, p.ID), WorkspaceID: a.WorkspaceID})
	if err != nil || plant.AcquiredOn.Time.Format("2006-01-02") != "2023-03-01" {
		t.Fatalf("projection after update: %+v err=%v", plant, err)
	}
	// KNOWN without a pricing source is a validation failure (T11 ships
	// pricing; fresh plant: the duplicate check would answer first).
	fresh := mustCreatePlant(t, s, ctx, a, "Priced")
	if _, problems, err := s.RecordAcquisition(ctx, a, AcquisitionInput{PricingState: "KNOWN"}, fresh.ID, nil, nil); err != nil || !hasCode(problems, CodeValidation) {
		t.Fatalf("KNOWN without a price source must be rejected: %v %+v", err, problems)
	}
}

// --- pagination ------------------------------------------------------------------

func TestKeysetPagination(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	for _, n := range []string{"alpha", "Bravo", "charlie"} {
		mustCreatePlant(t, s, ctx, a, n)
	}
	page1, err := s.ListPlants(ctx, a, PlantQuery{First: 2, Order: OrderNameAsc})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Rows) != 2 || !page1.HasNext || page1.EndCursor == nil {
		t.Fatalf("page1: %+v", page1)
	}
	// Ordering is case-insensitive: alpha, Bravo.
	if page1.Rows[0].Name != "alpha" || page1.Rows[1].Name != "Bravo" {
		t.Fatalf("order: %s %s", page1.Rows[0].Name, page1.Rows[1].Name)
	}
	page2, err := s.ListPlants(ctx, a, PlantQuery{First: 2, After: page1.EndCursor, Order: OrderNameAsc})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Rows) != 1 || page2.HasNext || page2.Rows[0].Name != "charlie" {
		t.Fatalf("page2: %+v", page2)
	}
	// Cursor bound to its ordering: a nameAsc cursor is invalid for createdDesc.
	if _, err := s.ListPlants(ctx, a, PlantQuery{First: 2, After: page1.EndCursor, Order: OrderCreatedDesc}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-order cursor must be rejected, got %v", err)
	}
	// Garbage cursor.
	bad := base64.RawURLEncoding.EncodeToString([]byte("nope"))
	if _, err := s.ListPlants(ctx, a, PlantQuery{First: 2, After: &bad, Order: OrderNameAsc}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("garbage cursor must be rejected, got %v", err)
	}
}

func TestDefaultStatusVisibility(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	p := mustCreatePlant(t, s, ctx, a, "Ghost")
	if _, problems, err := s.ArchivePlant(ctx, a, p.ID, 1, nil); err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	def, err := s.ListPlants(ctx, a, PlantQuery{Order: OrderNameAsc, Statuses: []string{"ACTIVE", "DEAD"}})
	if err != nil || len(def.Rows) != 0 {
		t.Fatalf("archived plant visible by default: %+v err=%v", def.Rows, err)
	}
	arch, err := s.ListPlants(ctx, a, PlantQuery{Order: OrderNameAsc, Statuses: []string{"ARCHIVED"}})
	if err != nil || len(arch.Rows) != 1 {
		t.Fatalf("explicit ARCHIVED must include it: %+v err=%v", arch.Rows, err)
	}
}

func mustParse(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, ok := parseUUID(s)
	if !ok {
		t.Fatalf("bad uuid %q", s)
	}
	return u
}
