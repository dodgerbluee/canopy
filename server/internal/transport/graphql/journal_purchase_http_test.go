package graphql

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
)

// T18 acceptance over the real stack: the unified timeline carries all
// four R1 record types, purchase entries are allocation-aware (one entry
// per plant-bearing allocation, exact minor amounts on the card, totals
// never derived from cards), unknown purchase dates stay explicit,
// void/update lifecycle moves or tombstones entries in the same tx, and
// a 50-plant mixed page hydrates within the frozen query budget (<=8).

const journalAllQ = `query($plantId: ID, $first: Int, $after: String, $filter: JournalFilter) {
  journal(plantId: $plantId, first: $first, after: $after, filter: $filter) {
    items {
      __typename
      ... on PhotoEntry { id plantId occurredAt createdAt summary tombstonedAt
        photo { id takenAt dateSource viewpoint isCover revision deletedAt
                asset { id state createdAt } } }
      ... on ActivityEntry { id plantId occurredAt createdAt summary tombstonedAt
        activity { id kind occurredAt timezone note quantity unit product revision voidedAt } }
      ... on MeasurementEntry { id plantId occurredAt createdAt summary tombstonedAt
        measurement { id quantityKind value unit observedAt method uncertainty activityId revision } }
      ... on PurchaseEntry { id plantId occurredAt createdAt summary tombstonedAt
        allocation { id purchaseLineId plantId lineMinor taxMinor shippingMinor } }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

func journalItems(t *testing.T, m *mediaEnv, vars map[string]any) []map[string]any {
	t.Helper()
	r := m.gql(journalAllQ, vars)
	if len(r.Errors) > 0 {
		t.Fatalf("journal: %s", r.Raw)
	}
	conn := dataOf(t, r, "journal")
	var items []map[string]any
	if raw, ok := conn["items"].([]any); ok {
		for _, it := range raw {
			items = append(items, it.(map[string]any))
		}
	}
	return items
}

func TestPurchaseJournalHTTP(t *testing.T) {
	m := newMediaEnv(t, 1<<30)
	plantA := mustPlantGQL(t, m)
	plantB := mustPlantGQL(t, m)

	// --- create: two allocations on one line project two entries -------
	in := map[string]any{
		"seller": "Nursery Co", "purchasedOn": "2026-05-10", "currency": "USD",
		"totalMinor": "10000", "taxMinor": "1000", "shippingMinor": "1000",
		"lines": []any{map[string]any{
			"description": "Fig tree", "quantity": "1", "lineTotalMinor": "8000",
			"allocations": []any{
				map[string]any{"plantId": plantA, "lineMinor": "5000", "taxMinor": "500", "shippingMinor": "500"},
				map[string]any{"plantId": plantB, "lineMinor": "3000", "taxMinor": "500", "shippingMinor": "500"},
			},
		}},
	}
	payload, probs := runMut(t, m, "createPurchase", "CreatePurchaseInput", in, nil,
		"purchase { id revision lines(first:10) { items { id allocations { id plantId } } } }")
	if len(probs) > 0 {
		t.Fatalf("createPurchase problems: %v", probs)
	}
	pur := payload["purchase"].(map[string]any)
	purchaseID := pur["id"].(string)

	entries := journalItems(t, m, map[string]any{"first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}}})
	if len(entries) != 2 {
		t.Fatalf("want 2 purchase entries, got %d: %s", len(entries), jsonOf(entries))
	}
	byPlant := map[string]map[string]any{}
	for _, e := range entries {
		if e["__typename"] != "PurchaseEntry" {
			t.Fatalf("typename %v", e["__typename"])
		}
		if e["summary"] != "Purchased from Nursery Co" {
			t.Errorf("summary = %v", e["summary"])
		}
		// purchasedOn is known: entry orders at that date (UTC midnight),
		// not at the creation instant.
		if got := e["occurredAt"].(string); got[:10] != "2026-05-10" {
			t.Errorf("occurredAt = %s, want the purchasedOn date", got)
		}
		byPlant[e["plantId"].(string)] = e
	}
	if len(byPlant) != 2 {
		t.Fatalf("entries must be one per allocated plant: %s", jsonOf(entries))
	}
	// card carries the exact allocation amounts (never a derived total)
	al := byPlant[plantA]["allocation"].(map[string]any)
	if al["lineMinor"] != "5000" || al["taxMinor"] != "500" || al["shippingMinor"] != "500" {
		t.Errorf("allocation amounts = %v", al)
	}
	if al["plantId"] != plantA {
		t.Errorf("allocation.plantId = %v", al["plantId"])
	}

	// --- plant scope: only that plant's allocation entry ---------------
	plantEntries := journalItems(t, m, map[string]any{"plantId": plantA, "first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}}})
	if len(plantEntries) != 1 || plantEntries[0]["plantId"] != plantA {
		t.Fatalf("plant-scoped purchase entries: %s", jsonOf(plantEntries))
	}

	// --- unknown purchase date stays explicit: occurredAt == createdAt --
	payload, probs = runMut(t, m, "createPurchase", "CreatePurchaseInput", map[string]any{
		"currency": "USD", "totalMinor": "5000",
		"lines": []any{map[string]any{
			"description": "Supplies + one plant", "quantity": "1", "lineTotalMinor": "5000",
			"allocations": []any{map[string]any{"plantId": plantA, "lineMinor": "5000", "taxMinor": "0", "shippingMinor": "0"}},
		}},
	}, nil, "purchase { id }")
	if len(probs) > 0 {
		t.Fatalf("createPurchase(undated) problems: %v", probs)
	}
	undated := journalItems(t, m, map[string]any{"plantId": plantA, "first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}}})
	if len(undated) != 2 {
		t.Fatalf("want 2 entries on plantA, got %d", len(undated))
	}
	var found bool
	for _, e := range undated {
		if e["summary"] == "Purchased" && e["occurredAt"] == e["createdAt"] {
			found = true
		}
	}
	if !found {
		t.Errorf("undated purchase must order by createdAt with no invented date: %s", jsonOf(undated))
	}

	// --- void: entries tombstone, visible only with includeTombstoned ---
	_, probs = runMut(t, m, "voidPurchase", "VoidPurchaseInput",
		map[string]any{"id": purchaseID, "expectedRevision": 1}, nil, "purchase { id revision }")
	if len(probs) > 0 {
		t.Fatalf("voidPurchase problems: %v", probs)
	}
	active := journalItems(t, m, map[string]any{"first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}}})
	if len(active) != 1 { // only the undated plantA entry remains active
		t.Fatalf("active purchase entries after void: %s", jsonOf(active))
	}
	all := journalItems(t, m, map[string]any{"first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}, "includeTombstoned": true}})
	if len(all) != 3 {
		t.Fatalf("with includeTombstoned want 3, got %d", len(all))
	}
	var tomb int
	for _, e := range all {
		if e["tombstonedAt"] != nil {
			tomb++
		}
	}
	if tomb != 2 {
		t.Errorf("want 2 tombstoned entries, got %d", tomb)
	}

	// --- update: plant reassignment moves the entry (in tx) -------------
	payload, probs = runMut(t, m, "createPurchase", "CreatePurchaseInput", map[string]any{
		"currency": "USD", "totalMinor": "4000",
		"lines": []any{map[string]any{
			"description": "Graft", "quantity": "1", "lineTotalMinor": "4000",
			"allocations": []any{map[string]any{"plantId": plantA, "lineMinor": "4000", "taxMinor": "0", "shippingMinor": "0"}},
		}},
	}, nil, "purchase { id revision lines(first:10) { items { id lineTotalMinor allocations { id plantId lineMinor taxMinor shippingMinor } } } }")
	if len(probs) > 0 {
		t.Fatalf("createPurchase(graft) problems: %v", probs)
	}
	graft := payload["purchase"].(map[string]any)
	line := graft["lines"].(map[string]any)["items"].([]any)[0].(map[string]any)
	alloc := line["allocations"].([]any)[0].(map[string]any)
	_, probs = runMut(t, m, "updatePurchase", "UpdatePurchaseInput", map[string]any{
		"id": graft["id"], "expectedRevision": 1,
		"lines": []any{map[string]any{
			"id": line["id"], "description": "Graft", "quantity": "1", "lineTotalMinor": "4000",
			"allocations": []any{map[string]any{
				"id": alloc["id"], "plantId": plantB,
				"lineMinor": "4000", "taxMinor": "0", "shippingMinor": "0",
			}},
		}},
	}, nil, "purchase { id revision }")
	if len(probs) > 0 {
		t.Fatalf("updatePurchase problems: %v", probs)
	}
	aEntries := journalItems(t, m, map[string]any{"plantId": plantA, "first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}}})
	for _, e := range aEntries {
		if e["allocation"].(map[string]any)["id"] == alloc["id"] {
			t.Fatal("reassigned entry still on the old plant")
		}
	}
	bEntries := journalItems(t, m, map[string]any{"plantId": plantB, "first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}}})
	var moved bool
	for _, e := range bEntries {
		al := e["allocation"].(map[string]any)
		if al["id"] == alloc["id"] && al["lineMinor"] == "4000" {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("reassigned entry missing on the new plant: %s", jsonOf(bEntries))
	}

	// --- atomic path: createPlant(initialPurchase) projects too ---------
	r := m.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"name": "Market lemon", "type": "INDIVIDUAL", "category": "CITRUS",
			"acquisition":     map[string]any{"pricingState": "KNOWN", "seller": "Market"},
			"initialPurchase": map[string]any{"currency": "USD", "totalMinor": "2500", "seller": "Market"},
		}})
	plantC := mustID(t, dataOf(t, r, "createPlant")["plant"].(map[string]any), "id")
	cEntries := journalItems(t, m, map[string]any{"plantId": plantC, "first": 50,
		"filter": map[string]any{"entryTypes": []string{"PURCHASE"}}})
	if len(cEntries) != 1 || cEntries[0]["summary"] != "Purchased from Market" {
		t.Fatalf("atomic purchase entry missing: %s", jsonOf(cEntries))
	}

	// --- monetary totals come from the aggregate, never from cards -----
	// spending sums purchase allocations (10000 voided -> excluded;
	// 5000 undated + 4000 grafted + 2500 atomic = 11500). The journal
	// cards carry no money, so this aggregate is computed purely from
	// the purchase rows.
	sr := m.gql(spendingQ, nil)
	spend := dataOf(t, sr, "spending")
	raw, _ := json.Marshal(spend["knownSpendingByCurrency"])
	var totals []struct {
		Currency string `json:"currency"`
		Total    struct {
			MinorUnits string `json:"minorUnits"`
			Currency   string `json:"currency"`
		} `json:"total"`
	}
	if err := json.Unmarshal(raw, &totals); err != nil || len(totals) != 1 ||
		totals[0].Currency != "USD" || totals[0].Total.MinorUnits != "11500" {
		t.Errorf("spending aggregate = %s, want exactly 11500 USD from purchase rows only", raw)
	}
}

func TestJournalQueryBudgetHTTP(t *testing.T) {
	tr := &countTracer{}
	m := newMediaEnvTraced(t, 1<<30, tr)
	ctx := context.Background()

	// 50 plants, each with one of every R1 record type + projection rows.
	seed := []string{
		`INSERT INTO plants (id, workspace_id, name, type, category)
		 SELECT gen_random_uuid(), $1, 'P'||i, 'INDIVIDUAL', 'OTHER' FROM generate_series(1,50) i`,
		`INSERT INTO activities (id, workspace_id, plant_id, kind, occurred_at, timezone, quantity, unit)
		 SELECT gen_random_uuid(), $1, id, 'WATER',
                now() - ((abs(hashtext(id::text)) % 100) || ' minutes')::interval, 'UTC', 1.5, 'L'
		 FROM plants WHERE workspace_id = $1`,
		`INSERT INTO measurements (id, workspace_id, plant_id, quantity_kind, value, unit, observed_at)
		 SELECT gen_random_uuid(), $1, id, 'LENGTH', 12, 'CM',
                now() - ((abs(hashtext(id::text)) % 100) || ' minutes')::interval
		 FROM plants WHERE workspace_id = $1`,
		`INSERT INTO purchases (id, workspace_id, currency, total_minor, purchased_on)
		 SELECT id, $1, 'USD', 1000, DATE '2026-06-01' FROM plants WHERE workspace_id = $1`,
		`INSERT INTO purchase_lines (id, workspace_id, purchase_id, description, quantity, line_total_minor)
		 SELECT id, $1, id, 'starter', 1, 1000 FROM plants WHERE workspace_id = $1`,
		`INSERT INTO purchase_allocations (id, workspace_id, purchase_line_id, plant_id, line_minor)
		 SELECT id, $1, id, id, 1000 FROM plants WHERE workspace_id = $1`,
		`INSERT INTO assets (id, workspace_id, purpose, state, byte_count, sha256, storage_key)
		 SELECT id, $1, 'PHOTO', 'READY', 10, '0000000000000000000000000000000000000000000000000000000000000000', 'k/'||id
		 FROM plants WHERE workspace_id = $1`,
		`INSERT INTO plant_photos (id, workspace_id, plant_id, asset_id, date_source, viewpoint)
		 SELECT id, $1, id, id, 'UPLOAD_FALLBACK', 'WHOLE' FROM plants WHERE workspace_id = $1`,
		`INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
		 SELECT gen_random_uuid(), $1, plant_id, 'ACTIVITY', id, occurred_at, 'Watered 1.5 L'
		   FROM activities WHERE workspace_id = $1`,
		`INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
		 SELECT gen_random_uuid(), $1, plant_id, 'MEASUREMENT', id, observed_at, 'Height 12 cm'
		   FROM measurements WHERE workspace_id = $1`,
		`INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
		 SELECT gen_random_uuid(), $1, pa.plant_id, 'PURCHASE', pa.id,
                now() - ((abs(hashtext(pa.id::text)) % 100) || ' minutes')::interval, 'Purchased'
		   FROM purchase_allocations pa WHERE pa.workspace_id = $1`,
		`INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
		 SELECT gen_random_uuid(), $1, pp.plant_id, 'PHOTO', pp.id,
                now() - ((abs(hashtext(pp.id::text)) % 100) || ' minutes')::interval, 'Photo'
		   FROM plant_photos pp WHERE pp.workspace_id = $1`,
	}
	for _, stmt := range seed {
		if _, err := m.pool.Exec(ctx, stmt, m.wsID); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}

	tr.n.Store(0)
	items := journalItems(t, m, map[string]any{"first": 25})
	n := tr.n.Load()

	if len(items) != 25 {
		t.Fatalf("want 25 items, got %d", len(items))
	}
	seen := map[string]int{}
	for _, e := range items {
		seen[e["__typename"].(string)]++
	}
	for _, want := range []string{"PhotoEntry", "ActivityEntry", "MeasurementEntry", "PurchaseEntry"} {
		if seen[want] == 0 {
			t.Errorf("mixed page must carry every R1 type; missing %s (page: %v)", want, seen)
		}
	}
	t.Logf("50-plant mixed journal page: %d queries (budget 8)", n)
	if n > 8 {
		t.Errorf("journal page issued %d queries, budget is 8", n)
	}
}

// TestSearchBoundsHTTP pins the frozen "max 200 chars" bound on the
// free-text search inputs; bounded search is part of the T18 surface.
func TestSearchBoundsHTTP(t *testing.T) {
	m := newMediaEnv(t, 1<<30)
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'q'
	}
	q := string(long)

	for _, tc := range []struct {
		name, query string
		vars        map[string]any
	}{
		{"plants", `query($filter: PlantFilter) { plants(filter: $filter) { items { id } pageInfo { hasNextPage } } }`,
			map[string]any{"filter": map[string]any{"query": q}}},
		{"tags", `query($filter: TagFilter) { tags(filter: $filter) { items { id } pageInfo { hasNextPage } } }`,
			map[string]any{"filter": map[string]any{"query": q}}},
		{"purchases", `query($filter: PurchaseFilter) { purchases(filter: $filter) { items { id } pageInfo { hasNextPage } } }`,
			map[string]any{"filter": map[string]any{"sellerQuery": q}}},
	} {
		r := m.gql(tc.query, tc.vars)
		if len(r.Errors) == 0 {
			t.Errorf("%s: 201-char search must be rejected", tc.name)
			continue
		}
		if r.Errors[0].Extensions["code"] != "VALIDATION" {
			t.Errorf("%s: want VALIDATION top-level error, got %s", tc.name, r.Raw)
		}
	}

	// bounded search still works end to end
	plantID := mustPlantGQL(t, m) // "Test fig"
	r := m.gql(`query($filter: PlantFilter) { plants(filter: $filter) { items { id name } } }`,
		map[string]any{"filter": map[string]any{"query": "fig"}})
	items := dataOf(t, r, "plants")["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != plantID {
		t.Errorf("plant search: %s", r.Raw)
	}
}

// ---- helpers ---------------------------------------------------------------

type countTracer struct{ n atomic.Int64 }

func (c *countTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (c *countTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
