package graphql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"canopy/server/internal/platform"
)

// T11 acceptance over real HTTP: the frozen purchase operations against
// the shipped resolver stack. The 03 $100 example is reproduced exactly
// (contracts/fixtures/api/purchase-detail-100.json and
// spending-100-example.json), plus the negative transitions from 03:
// conservation errors, void cascade, linked-allocation protection with
// explicit unlink, idempotency and cross-workspace isolation.

const purchaseFields = `id seller purchasedOn currency totalMinor shippingMinor taxMinor revision voidedAt createdAt updatedAt
  lines(first: 100) { items { id purchaseId description quantity lineTotalMinor
    allocations { id purchaseLineId plantId lineMinor taxMinor shippingMinor } }
    pageInfo { hasNextPage endCursor } }
  receipts { id purchaseId asset { id state } }`

const acqFields = `id seller acquiredOn pricingState revision
  initialPurchaseAllocation { id purchaseLineId plantId lineMinor taxMinor shippingMinor }`

const createPurchaseMut = `mutation($input: CreatePurchaseInput!, $cmid: String) {
  createPurchase(input: $input, clientMutationId: $cmid) {
    purchase { ` + purchaseFields + ` }
    problems { code field message currentRevision } clientMutationId } }`

const spendingQ = `query($filter: SpendingFilter) { spending(filter: $filter) {
  knownSpendingByCurrency { currency total { minorUnits currency } }
  unknownAcquisitionCostCount missingAcquisitionRecordCount } }`

func mustPlant(t *testing.T, e *testEnv, name string) string {
	t.Helper()
	r := e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"name": name, "type": "INDIVIDUAL", "category": "OTHER"}})
	plant := dataOf(t, r, "createPlant")["plant"].(map[string]any)
	return mustID(t, plant, "id")
}

func createPurchase(t *testing.T, e *testEnv, input map[string]any, cmid *string) map[string]any {
	t.Helper()
	r := e.gql(createPurchaseMut, map[string]any{"input": input, "cmid": cmid})
	if len(r.Errors) != 0 {
		t.Fatalf("createPurchase top-level errors: %s", r.Raw)
	}
	top := dataOf(t, r, "createPurchase")
	if raw, _ := json.Marshal(top["problems"]); string(raw) != "[]" {
		t.Fatalf("createPurchase problems: %s", r.Raw)
	}
	return top["purchase"].(map[string]any)
}

func allocInput(plantID any, line, tax, ship string) map[string]any {
	return map[string]any{"plantId": plantID, "lineMinor": line, "taxMinor": tax, "shippingMinor": ship}
}

func lineInput(desc string, total string, allocs ...map[string]any) map[string]any {
	return map[string]any{"description": desc, "quantity": "1", "lineTotalMinor": total, "allocations": allocs}
}

func purchaseByID(t *testing.T, e *testEnv, id string) map[string]any {
	t.Helper()
	r := e.gql(`query($id: ID!) { purchase(id: $id) { `+purchaseFields+` } }`, map[string]any{"id": id})
	if len(r.Errors) != 0 {
		t.Fatalf("purchase query errors: %s", r.Raw)
	}
	p, _ := r.Data["purchase"].(map[string]any)
	return p
}

func plantAcq(t *testing.T, e *testEnv, plantID string) map[string]any {
	t.Helper()
	r := e.gql(`query($id: ID!) { plant(id: $id) { id acquisition { `+acqFields+` } } }`,
		map[string]any{"id": plantID})
	pl := dataOf(t, r, "plant")
	acq, _ := pl["acquisition"].(map[string]any)
	return acq
}

func spendingOf(t *testing.T, e *testEnv, filter map[string]any) map[string]any {
	t.Helper()
	r := e.gql(spendingQ, map[string]any{"filter": filter})
	if len(r.Errors) != 0 {
		t.Fatalf("spending errors: %s", r.Raw)
	}
	return dataOf(t, r, "spending")
}

func knownTotal(t *testing.T, sp map[string]any, currency string) string {
	t.Helper()
	rows, _ := sp["knownSpendingByCurrency"].([]any)
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["currency"] == currency {
			return row["total"].(map[string]any)["minorUnits"].(string)
		}
	}
	return ""
}

func lineAllocs(purchase map[string]any, i int) []any {
	items := purchase["lines"].(map[string]any)["items"].([]any)
	return items[i].(map[string]any)["allocations"].([]any)
}

// Lines and allocations keyset by entry position (then id), so content-based
// lookup helpers remain valid, and index-based assertions are meaningful too.
func findLine(purchase map[string]any, desc string) map[string]any {
	for _, raw := range purchase["lines"].(map[string]any)["items"].([]any) {
		if l := raw.(map[string]any); l["description"] == desc {
			return l
		}
	}
	panic("line not found: " + desc)
}

func findAlloc(line map[string]any, plantID any) map[string]any {
	for _, raw := range line["allocations"].([]any) {
		if al := raw.(map[string]any); al["plantId"] == plantID {
			return al
		}
	}
	panic("allocation not found")
}

// The 03 example end to end: one $100.00 purchase incl $5 tax + $5
// shipping, two $45.00 lines on two figs; each fig owes $50.00; the
// workspace total is ONE $100.00 expense (lines/allocations never
// multiply it).
func TestPurchase100ExampleOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	figA := mustPlant(t, e, "Fig 'Brown Turkey'")
	figB := mustPlant(t, e, "Fig 'Hardy Chicago'")

	p := createPurchase(t, e, map[string]any{
		"seller": "Fig nursery", "purchasedOn": "2026-03-10", "currency": "USD",
		"totalMinor": "10000", "shippingMinor": "500", "taxMinor": "500",
		"lines": []any{
			lineInput("Fig tree A", "4500", allocInput(figA, "4500", "250", "250")),
			lineInput("Fig tree B", "4500", allocInput(figB, "4500", "250", "250")),
		},
	}, nil)

	if p["totalMinor"] != "10000" || p["taxMinor"] != "500" || p["shippingMinor"] != "500" {
		t.Fatalf("header amounts: %v", p)
	}
	if p["seller"] != "Fig nursery" || p["purchasedOn"] != "2026-03-10" || p["revision"] != float64(1) {
		t.Fatalf("header fields: %v", p)
	}
	if p["voidedAt"] != nil {
		t.Fatalf("fresh purchase must not be voided: %v", p["voidedAt"])
	}
	items := p["lines"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 lines: %v", items)
	}
	for _, want := range []string{"Fig tree A", "Fig tree B"} {
		l := findLine(p, want)
		if l["lineTotalMinor"] != "4500" {
			t.Fatalf("line %s: %v", want, l)
		}
		if l["purchaseId"] != p["id"] {
			t.Fatalf("line %s purchaseId: %v", want, l)
		}
		allocs := l["allocations"].([]any)
		if len(allocs) != 1 {
			t.Fatalf("line %s allocations: %v", want, allocs)
		}
		al := allocs[0].(map[string]any)
		if al["lineMinor"] != "4500" || al["taxMinor"] != "250" || al["shippingMinor"] != "250" {
			t.Fatalf("line %s allocation amounts: %v", want, al)
		}
		if al["purchaseLineId"] != l["id"] {
			t.Fatalf("allocation purchaseLineId: %v", al)
		}
	}
	if pg := p["lines"].(map[string]any)["pageInfo"].(map[string]any); pg["hasNextPage"] != false || pg["endCursor"] != nil {
		t.Fatalf("lines pageInfo: %v", pg)
	}
	if rc, _ := p["receipts"].([]any); len(rc) != 0 {
		t.Fatalf("fresh purchase has receipts: %v", rc)
	}

	// Detail query returns the same shape.
	if again := purchaseByID(t, e, p["id"].(string)); again["totalMinor"] != "10000" {
		t.Fatalf("purchase detail: %v", again)
	}

	// Workspace total: exactly one 10000 expense, not 2×4500 or 4×5000.
	sp := spendingOf(t, e, nil)
	if got := knownTotal(t, sp, "USD"); got != "10000" {
		t.Fatalf("workspace total: want 10000, got %q (%v)", got, sp)
	}
	if sp["missingAcquisitionRecordCount"] != float64(2) || sp["unknownAcquisitionCostCount"] != float64(0) {
		t.Fatalf("counts: %v", sp)
	}

	// Each fig owes its share: 4500 + 250 + 250 = 5000.
	for _, fig := range []string{figA, figB} {
		sp := spendingOf(t, e, map[string]any{"plantId": fig})
		if got := knownTotal(t, sp, "USD"); got != "5000" {
			t.Fatalf("plant %s spending: want 5000, got %q", fig, got)
		}
	}
}

// Conservation failures quote the exact expected/found amounts and roll
// the whole transaction back: a purchase never lands half-allocated.
func TestPurchaseConservationErrorsOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	// Allocation under-sum: 4499 vs 4500.
	r := e.gql(createPurchaseMut, map[string]any{"input": map[string]any{
		"currency": "USD", "totalMinor": "10000", "shippingMinor": "500", "taxMinor": "500",
		"lines": []any{
			lineInput("Fig A", "4500", allocInput(fig, "4499", "250", "250")),
			lineInput("Fig B", "4500", allocInput(fig, "4500", "250", "250")),
		},
	}, "cmid": nil})
	probs := payloadProblems(t, r, "createPurchase")
	wantProblemCode(t, probs, "VALIDATION")
	msg, _ := json.Marshal(probs)
	if !strings.Contains(string(msg), "4499") || !strings.Contains(string(msg), "4500") {
		t.Fatalf("problem must quote exact amounts: %s", msg)
	}

	// Overhead exceeds total.
	r = e.gql(createPurchaseMut, map[string]any{"input": map[string]any{
		"currency": "USD", "totalMinor": "10000", "shippingMinor": "5000", "taxMinor": "6000",
		"lines": []any{lineInput("Fig A", "0")},
	}, "cmid": nil})
	wantProblemCode(t, payloadProblems(t, r, "createPurchase"), "VALIDATION")

	// Unknown plant: NOT_FOUND, nothing persisted.
	r = e.gql(createPurchaseMut, map[string]any{"input": map[string]any{
		"currency": "USD", "totalMinor": "4500",
		"lines": []any{lineInput("Fig A", "4500", allocInput("5f0c1b6e-1111-4aaa-8bbb-0000000000ee", "4500", "0", "0"))},
	}, "cmid": nil})
	wantProblemCode(t, payloadProblems(t, r, "createPurchase"), "NOT_FOUND")

	// DB rollback midway through allocation: no purchase survived.
	r = e.gql(`query { purchases(first: 10) { items { id } } }`, nil)
	if items := dataOf(t, r, "purchases")["items"].([]any); len(items) != 0 {
		t.Fatalf("failed creates must leave no rows: %v", items)
	}
}

// Unallocated supplies (plantId null) carry their share of overhead and
// count in the workspace total but never in a plant total.
func TestUnallocatedSuppliesOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	p := createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "10000", "shippingMinor": "500", "taxMinor": "500",
		"lines": []any{lineInput("Potting mix 2 bags", "9000",
			allocInput(fig, "4500", "250", "250"),
			allocInput(nil, "4500", "250", "250")),
		},
	}, nil)
	allocs := findLine(p, "Potting mix 2 bags")["allocations"].([]any)
	if len(allocs) != 2 {
		t.Fatalf("want 2 allocations: %v", allocs)
	}
	if findAlloc(findLine(p, "Potting mix 2 bags"), nil)["lineMinor"] != "4500" {
		t.Fatalf("supplies bucket missing or wrong size")
	}

	if got := knownTotal(t, spendingOf(t, e, nil), "USD"); got != "10000" {
		t.Fatalf("workspace total: %q", got)
	}
	if got := knownTotal(t, spendingOf(t, e, map[string]any{"plantId": fig}), "USD"); got != "5000" {
		t.Fatalf("plant total must exclude supplies bucket: %q", got)
	}
}

// recordAcquisition with newPurchase prices atomically; voiding the
// purchase reverts every acquisition priced from it to UNKNOWN while
// keeping provenance, and voided purchases leave the spending totals.
func TestVoidCascadeAndAtomicPricingOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	figA := mustPlant(t, e, "Fig A")
	figB := mustPlant(t, e, "Fig B")

	// Plant A: atomic recordAcquisition + newPurchase.
	r := e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { `+acqFields+` }
		purchase { id totalMinor } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"plantId": figA, "seller": "Friend", "acquiredOn": "2025-07-01", "pricingState": "KNOWN",
			"newPurchase": map[string]any{"currency": "USD", "totalMinor": "2500", "lineDescription": "Fig A cutting"},
		}})
	if len(r.Errors) != 0 {
		t.Fatalf("recordAcquisition errors: %s", r.Raw)
	}
	top := dataOf(t, r, "recordAcquisition")
	if raw, _ := json.Marshal(top["problems"]); string(raw) != "[]" {
		t.Fatalf("recordAcquisition problems: %s", r.Raw)
	}
	acqA := top["acquisition"].(map[string]any)
	if acqA["pricingState"] != "KNOWN" {
		t.Fatalf("acquisition state: %v", acqA)
	}
	ipa := acqA["initialPurchaseAllocation"].(map[string]any)
	if ipa["lineMinor"] != "2500" {
		t.Fatalf("initial allocation: %v", ipa)
	}
	p1 := top["purchase"].(map[string]any)
	if p1 == nil || p1["totalMinor"] != "2500" {
		t.Fatalf("atomic purchase missing: %v", top)
	}

	// Plant B: pre-existing shared purchase, linked by allocation id.
	p2 := createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "9000",
		"lines": []any{lineInput("Fig B bare root", "9000", allocInput(figB, "9000", "0", "0"))},
	}, nil)
	allocB := lineAllocs(p2, 0)[0].(map[string]any)["id"].(string)
	r = e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { `+acqFields+` } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"plantId": figB, "pricingState": "KNOWN", "purchaseAllocationId": allocB,
		}})
	acqB := dataOf(t, r, "recordAcquisition")["acquisition"].(map[string]any)
	if acqB["pricingState"] != "KNOWN" {
		t.Fatalf("plant B acquisition: %v (%s)", acqB, r.Raw)
	}

	// Both known: 2500 + 9000, counted once each.
	sp := spendingOf(t, e, nil)
	if got := knownTotal(t, sp, "USD"); got != "11500" {
		t.Fatalf("combined total: %q", got)
	}
	if sp["unknownAcquisitionCostCount"] != float64(0) || sp["missingAcquisitionRecordCount"] != float64(0) {
		t.Fatalf("counts: %v", sp)
	}

	// Void the shared purchase: plant B reverts to UNKNOWN, provenance
	// (seller null here, but the acquisition row) survives.
	r = e.gql(`mutation($input: VoidPurchaseInput!) {
		voidPurchase(input: $input) { purchase { id voidedAt revision } problems { code field message currentRevision } } }`,
		map[string]any{"input": map[string]any{"id": p2["id"], "expectedRevision": 1}})
	voided := dataOf(t, r, "voidPurchase")["purchase"].(map[string]any)
	if voided["voidedAt"] == nil {
		t.Fatalf("voidedAt not set: %s", r.Raw)
	}

	acqB = plantAcq(t, e, figB)
	if acqB["pricingState"] != "UNKNOWN" || acqB["initialPurchaseAllocation"] != nil {
		t.Fatalf("void cascade must revert acquisition to UNKNOWN: %v", acqB)
	}
	// Plant A's own purchase is untouched.
	if acqA = plantAcq(t, e, figA); acqA["pricingState"] != "KNOWN" {
		t.Fatalf("unrelated acquisition changed: %v", acqA)
	}

	// Spending: voided excluded, unknown counted.
	sp = spendingOf(t, e, nil)
	if got := knownTotal(t, sp, "USD"); got != "2500" {
		t.Fatalf("post-void total: %q", got)
	}
	if sp["unknownAcquisitionCostCount"] != float64(1) {
		t.Fatalf("post-void unknown count: %v", sp)
	}

	// Voided excluded from lists by default, visible on request.
	r = e.gql(`query($f: PurchaseFilter) { purchases(filter: $f) { items { id voidedAt } } }`,
		map[string]any{"f": map[string]any{"includeVoided": true}})
	if items := dataOf(t, r, "purchases")["items"].([]any); len(items) != 2 {
		t.Fatalf("includeVoided list: %v", items)
	}
	r = e.gql(`query { purchases(first: 10) { items { id voidedAt } } }`, nil)
	if items := dataOf(t, r, "purchases")["items"].([]any); len(items) != 1 {
		t.Fatalf("default list must exclude voided: %v", items)
	}
}

// Acquisition-linked allocations cannot vanish, move lines, or change
// plant without the explicit unlinkAcquisitionIds confirmation; the
// unlink reverts the acquisition to UNKNOWN first, and re-pricing is
// then allowed. KNOWN→KNOWN direct re-pricing is refused.
func TestLinkedAllocationProtectionOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	p := createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "10000", "shippingMinor": "500", "taxMinor": "500",
		"lines": []any{
			lineInput("Fig", "4500", allocInput(fig, "4500", "250", "250")),
			lineInput("Supplies", "4500", allocInput(nil, "4500", "250", "250")),
		},
	}, nil)
	figLine := findLine(p, "Fig")
	line1ID := figLine["id"].(string)
	allocID := findAlloc(figLine, fig)["id"].(string)

	r := e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { id revision pricingState } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"plantId": fig, "pricingState": "KNOWN", "purchaseAllocationId": allocID}})
	acq := dataOf(t, r, "recordAcquisition")["acquisition"].(map[string]any)
	acqID := mustID(t, acq, "id")

	// Drop the linked allocation without unlink: refused.
	r = e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"id": p["id"], "expectedRevision": 1,
			"lines": []any{lineInput("Fig", "4500", allocInput(fig, "4500", "250", "250"))},
		}})
	wantProblemCode(t, payloadProblems(t, r, "updatePurchase"), "VALIDATION")

	// Move the linked allocation to the other line without unlink: refused.
	r = e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"id": p["id"], "expectedRevision": 1,
			"lines": []any{
				lineInput("Supplies", "4500"),
				lineInput("Fig", "4500", map[string]any{"id": allocID, "plantId": fig, "lineMinor": "4500", "taxMinor": "250", "shippingMinor": "250"}),
			},
		}})
	wantProblemCode(t, payloadProblems(t, r, "updatePurchase"), "VALIDATION")

	// Explicit unlink: succeeds, acquisition reverts to UNKNOWN first.
	r = e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id revision
			lines(first: 100) { items { id description } } }
			problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"id": p["id"], "expectedRevision": 1, "unlinkAcquisitionIds": []string{acqID},
			"lines": []any{
				map[string]any{"id": line1ID, "description": "Fig", "quantity": "1", "lineTotalMinor": "4500",
					"allocations": []any{map[string]any{"id": allocID, "plantId": fig, "lineMinor": "4500", "taxMinor": "250", "shippingMinor": "250"}}},
				lineInput("Supplies", "4500", allocInput(nil, "4500", "250", "250")),
			},
		}})
	upd := dataOf(t, r, "updatePurchase")
	if raw, _ := json.Marshal(upd["problems"]); string(raw) != "[]" {
		t.Fatalf("unlink update failed: %s", r.Raw)
	}
	if upd["purchase"].(map[string]any)["revision"] != float64(2) {
		t.Fatalf("revision bump: %v", upd["purchase"])
	}
	if acq = plantAcq(t, e, fig); acq["pricingState"] != "UNKNOWN" || acq["initialPurchaseAllocation"] != nil {
		t.Fatalf("unlink must revert acquisition: %v", acq)
	}

	// Re-price from the retained allocation (acquisition revision moved).
	r = e.gql(`mutation($input: PriceAcquisitionInput!) {
		priceAcquisition(input: $input) { acquisition { id revision pricingState } problems { code field message currentRevision } } }`,
		map[string]any{"input": map[string]any{
			"acquisitionId": acqID, "expectedRevision": mustInt(acq["revision"]), "purchaseAllocationId": allocID,
		}})
	repriced := dataOf(t, r, "priceAcquisition")["acquisition"].(map[string]any)
	if repriced["pricingState"] != "KNOWN" {
		t.Fatalf("re-price: %v (%s)", repriced, r.Raw)
	}

	// KNOWN -> KNOWN directly: CONFLICT (must revert first).
	r = e.gql(`mutation($input: PriceAcquisitionInput!) {
		priceAcquisition(input: $input) { acquisition { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"acquisitionId": acqID, "expectedRevision": mustInt(repriced["revision"]), "purchaseAllocationId": allocID,
		}})
	wantProblemCode(t, payloadProblems(t, r, "priceAcquisition"), "CONFLICT")

	// revertToUnknown detaches without voiding the purchase.
	r = e.gql(`mutation($input: PriceAcquisitionInput!) {
		priceAcquisition(input: $input) { acquisition { pricingState revision } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"acquisitionId": acqID, "expectedRevision": mustInt(repriced["revision"]), "revertToUnknown": true,
		}})
	reverted := dataOf(t, r, "priceAcquisition")["acquisition"].(map[string]any)
	if reverted["pricingState"] != "UNKNOWN" {
		t.Fatalf("revert: %v", reverted)
	}
	if still := purchaseByID(t, e, p["id"].(string)); still == nil || still["voidedAt"] != nil {
		t.Fatalf("financial purchase must survive the unlink: %v", still)
	}

	// Stale revision on the purchase: CONFLICT with currentRevision.
	r = e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id } problems { code field message currentRevision } } }`,
		map[string]any{"input": map[string]any{"id": p["id"], "expectedRevision": 1, "seller": "renamed"}})
	probs := payloadProblems(t, r, "updatePurchase")
	wantProblemCode(t, probs, "CONFLICT")
	if probs[0]["currentRevision"] != float64(2) {
		t.Fatalf("currentRevision: %v", probs[0])
	}
}

func mustInt(v any) int { return int(v.(float64)) }

// REPAIR-02: an edit that RETAINS the linked allocation — a date-only edit
// that resends the same line/allocation ids (what the web form always sends),
// or an amount change — must succeed. The line tree is replaced by
// delete+re-insert inside one transaction; the acquisition FK must not reject
// the transient delete. RESTRICT is never deferred, so it aborted the delete
// ("line replacement failed"); NO ACTION DEFERRABLE INITIALLY DEFERRED
// re-checks at COMMIT, after the re-insert with the retained id has landed.
func TestUpdatePurchaseRetainsLinkedAllocationOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	p := createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "10000", "shippingMinor": "500", "taxMinor": "500",
		"lines": []any{
			lineInput("Fig", "4500", allocInput(fig, "4500", "250", "250")),
			lineInput("Supplies", "4500", allocInput(nil, "4500", "250", "250")),
		},
	}, nil)
	figLine := findLine(p, "Fig")
	lineID := figLine["id"].(string)
	allocID := findAlloc(figLine, fig)["id"].(string)

	r := e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { id revision pricingState } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"plantId": fig, "pricingState": "KNOWN", "purchaseAllocationId": allocID}})
	dataOf(t, r, "recordAcquisition")

	linkedLines := func(figTotal string) []any {
		return []any{
			map[string]any{"id": lineID, "description": "Fig", "quantity": "1", "lineTotalMinor": figTotal,
				"allocations": []any{map[string]any{"id": allocID, "plantId": fig, "lineMinor": figTotal, "taxMinor": "250", "shippingMinor": "250"}}},
			lineInput("Supplies", "4500", allocInput(nil, "4500", "250", "250")),
		}
	}
	updQ := `mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id revision } problems { code field message } } }`

	// Date-only edit, lines resent with retained ids: must succeed.
	r = e.gql(updQ, map[string]any{"input": map[string]any{
		"id": p["id"], "expectedRevision": 1, "purchasedOn": "2026-04-01",
		"lines": linkedLines("4500"),
	}})
	upd := dataOf(t, r, "updatePurchase")
	if raw, _ := json.Marshal(upd["problems"]); string(raw) != "[]" {
		t.Fatalf("retained-linked date edit must succeed: %s", r.Raw)
	}
	if upd["purchase"].(map[string]any)["revision"] != float64(2) {
		t.Fatalf("revision bump: %v", upd["purchase"])
	}
	acq := plantAcq(t, e, fig)
	if acq["pricingState"] != "KNOWN" {
		t.Fatalf("acquisition must stay KNOWN: %v", acq)
	}
	if got, _ := acq["initialPurchaseAllocation"].(map[string]any)["id"].(string); got != allocID {
		t.Fatalf("acquisition link must survive the edit: %v", acq)
	}

	// Amount edit on the linked line (the owner's 20.00 -> 240.00 case):
	// retained ids, new amounts — must succeed and keep the link.
	r = e.gql(updQ, map[string]any{"input": map[string]any{
		"id": p["id"], "expectedRevision": 2, "totalMinor": "29500",
		"lines": linkedLines("24000"),
	}})
	upd = dataOf(t, r, "updatePurchase")
	if raw, _ := json.Marshal(upd["problems"]); string(raw) != "[]" {
		t.Fatalf("retained-linked amount edit must succeed: %s", r.Raw)
	}
	if acq = plantAcq(t, e, fig); acq["pricingState"] != "KNOWN" {
		t.Fatalf("acquisition after amount edit: %v", acq)
	}
}

func lineDescs(purchase map[string]any) []string {
	items := purchase["lines"].(map[string]any)["items"].([]any)
	out := make([]string, len(items))
	for i, raw := range items {
		out[i] = raw.(map[string]any)["description"].(string)
	}
	return out
}

// Lines and allocations come back in the owner's entry order — at create,
// and again after a line-replacement edit reorders them. Before the
// position column reads sorted by random UUID: the edit form could show a
// purchase's lines in an order nobody entered, and reshuffle them after
// every edit (the UI07 e2e journey caught this as a flake).
func TestPurchaseLineOrderPreservedOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	p := createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "10000", "shippingMinor": "0", "taxMinor": "0",
		"lines": []any{
			lineInput("First", "3000", allocInput(nil, "1500", "0", "0"), allocInput(fig, "1500", "0", "0")),
			lineInput("Second", "3000", allocInput(nil, "3000", "0", "0")),
			lineInput("Third", "4000", allocInput(nil, "4000", "0", "0")),
		},
	}, nil)
	if got := strings.Join(lineDescs(p), ","); got != "First,Second,Third" {
		t.Fatalf("create must preserve entry order: %v", got)
	}
	if a := lineAllocs(p, 0); a[0].(map[string]any)["plantId"] != nil || a[1].(map[string]any)["plantId"] != fig {
		t.Fatalf("allocation entry order must survive create: %v", a)
	}

	// Reorder via edit with retained ids: Third, First, Second.
	kept := func(desc string, total string) map[string]any {
		l := findLine(p, desc)
		allocs := []any{}
		for _, raw := range l["allocations"].([]any) {
			al := raw.(map[string]any)
			allocs = append(allocs, map[string]any{
				"id": al["id"], "plantId": al["plantId"], "lineMinor": al["lineMinor"],
				"taxMinor": al["taxMinor"], "shippingMinor": al["shippingMinor"],
			})
		}
		return map[string]any{"id": l["id"], "description": desc, "quantity": "1",
			"lineTotalMinor": total, "allocations": allocs}
	}
	r := e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id revision } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"id": p["id"], "expectedRevision": 1,
			"lines": []any{kept("Third", "4000"), kept("First", "3000"), kept("Second", "3000")},
		}})
	upd := dataOf(t, r, "updatePurchase")
	if raw, _ := json.Marshal(upd["problems"]); string(raw) != "[]" {
		t.Fatalf("reordering edit must succeed: %s", r.Raw)
	}
	after := purchaseByID(t, e, mustID(t, upd["purchase"].(map[string]any), "id"))
	if got := strings.Join(lineDescs(after), ","); got != "Third,First,Second" {
		t.Fatalf("edit must persist the new entry order: %v", got)
	}
	if a := lineAllocs(after, 1); a[0].(map[string]any)["plantId"] != nil || a[1].(map[string]any)["plantId"] != fig {
		t.Fatalf("allocation entry order must survive the edit: %v", a)
	}
}

// Idempotent replay returns the same purchase; a different payload under
// the same clientMutationId is an IDEMPOTENCY_CONFLICT.
func TestPurchaseIdempotencyOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	input := map[string]any{
		"currency": "USD", "totalMinor": "1000",
		"lines": []any{lineInput("Cutting", "1000", allocInput(fig, "1000", "0", "0"))},
	}
	cmid := "cm-purchase-1"
	p1 := createPurchase(t, e, input, &cmid)
	p2 := createPurchase(t, e, input, &cmid)
	if p1["id"] != p2["id"] {
		t.Fatalf("replay must return the same purchase: %v vs %v", p1["id"], p2["id"])
	}

	r := e.gql(createPurchaseMut, map[string]any{"input": map[string]any{
		"currency": "USD", "totalMinor": "9999",
		"lines": []any{lineInput("Different", "9999", allocInput(fig, "9999", "0", "0"))},
	}, "cmid": cmid})
	wantProblemCode(t, payloadProblems(t, r, "createPurchase"), "IDEMPOTENCY_CONFLICT")

	r = e.gql(`query { purchases(first: 10) { items { id } } }`, nil)
	if items := dataOf(t, r, "purchases")["items"].([]any); len(items) != 1 {
		t.Fatalf("replay must not duplicate: %v", items)
	}
}

// Foreign purchases are invisible: query null, mutations NOT_FOUND.
func TestPurchaseCrossWorkspaceOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	ctx := context.Background()
	ids := platform.UUIDv4{}

	owner := ids.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO owner (id, password_hash) VALUES ($1,'x')`, owner); err != nil {
		t.Fatal(err)
	}
	ws := ids.New()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO workspace (id, owner_id, display_name, timezone, default_currency)
		 VALUES ($1,$2,'Foreign','UTC','USD')`, ws, owner); err != nil {
		t.Fatal(err)
	}
	foreignPurch := ids.New()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO purchases (id, workspace_id, currency, total_minor) VALUES ($1,$2,'USD',5000)`,
		foreignPurch, ws); err != nil {
		t.Fatal(err)
	}

	if got := purchaseByID(t, e, uuidStr(foreignPurch)); got != nil {
		t.Fatalf("foreign purchase leaked: %v", got)
	}
	r := e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"id": uuidStr(foreignPurch), "expectedRevision": 1, "seller": "stolen"}})
	wantProblemCode(t, payloadProblems(t, r, "updatePurchase"), "NOT_FOUND")

	r = e.gql(`query { purchases(first: 10) { items { id } } }`, nil)
	if items := dataOf(t, r, "purchases")["items"].([]any); len(items) != 0 {
		t.Fatalf("foreign purchase in list: %v", items)
	}
}

// Keyset order is (purchased_on DESC NULLS LAST, id DESC): dated
// purchases first, NULL-date purchases last, cursors page cleanly.
func TestPurchasePaginationOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	mk := func(seller string, on any) string {
		p := createPurchase(t, e, map[string]any{
			"seller": seller, "purchasedOn": on, "currency": "USD", "totalMinor": "100",
			"lines": []any{lineInput("x", "100", allocInput(fig, "100", "0", "0"))},
		}, nil)
		return p["id"].(string)
	}
	pJan := mk("Acme January", "2026-01-01")
	pFeb := mk("Acme February", "2026-02-01")
	pNull := mk("No date", nil)

	r := e.gql(`query($first: Int, $after: String) { purchases(first: $first, after: $after) {
		items { id seller purchasedOn } pageInfo { hasNextPage endCursor } } }`,
		map[string]any{"first": 2})
	page := dataOf(t, r, "purchases")
	items := page["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != pFeb || items[1].(map[string]any)["id"] != pJan {
		t.Fatalf("page 1: %s", r.Raw)
	}
	if page["pageInfo"].(map[string]any)["hasNextPage"] != true {
		t.Fatalf("page 1 hasNextPage: %v", page["pageInfo"])
	}
	cur := page["pageInfo"].(map[string]any)["endCursor"].(string)

	r = e.gql(`query($first: Int, $after: String) { purchases(first: $first, after: $after) {
		items { id seller purchasedOn } pageInfo { hasNextPage endCursor } } }`,
		map[string]any{"first": 2, "after": cur})
	page = dataOf(t, r, "purchases")
	items = page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != pNull {
		t.Fatalf("page 2 must hold the NULL-date purchase last: %s", r.Raw)
	}
	if page["pageInfo"].(map[string]any)["hasNextPage"] != false {
		t.Fatalf("page 2 hasNextPage: %v", page["pageInfo"])
	}

	// Seller filter.
	r = e.gql(`query($f: PurchaseFilter) { purchases(filter: $f) { items { id } } }`,
		map[string]any{"f": map[string]any{"sellerQuery": "February"}})
	if items := dataOf(t, r, "purchases")["items"].([]any); len(items) != 1 ||
		items[0].(map[string]any)["id"] != pFeb {
		t.Fatalf("sellerQuery filter: %s", r.Raw)
	}

	// Malformed cursor: honest VALIDATION top-level error, not a fake page.
	r = e.gql(`query($after: String) { purchases(after: $after) { items { id } } }`,
		map[string]any{"after": "not-a-cursor"})
	if len(r.Errors) == 0 || r.Errors[0].Extensions["code"] != "VALIDATION" {
		t.Fatalf("bad cursor: %s", r.Raw)
	}
}

// createPlant with initialPurchase prices the acquisition atomically;
// the UNKNOWN+initialPurchase and KNOWN-without-purchase combinations
// are refused.
func TestCreatePlantWithInitialPurchaseOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())

	r := e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id name acquisition { `+acqFields+` } }
		problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"name": "Priced fig", "type": "INDIVIDUAL", "category": "OTHER",
			"acquisition":     map[string]any{"seller": "Nursery", "acquiredOn": "2026-03-10", "pricingState": "KNOWN"},
			"initialPurchase": map[string]any{"currency": "USD", "totalMinor": "3000", "purchasedOn": "2026-03-10", "seller": "Nursery"},
		}})
	if len(r.Errors) != 0 {
		t.Fatalf("createPlant errors: %s", r.Raw)
	}
	plant := dataOf(t, r, "createPlant")["plant"].(map[string]any)
	plantID := mustID(t, plant, "id")
	acq := plant["acquisition"].(map[string]any)
	if acq["pricingState"] != "KNOWN" {
		t.Fatalf("acquisition: %v", acq)
	}
	ipa := acq["initialPurchaseAllocation"].(map[string]any)
	if ipa["lineMinor"] != "3000" || ipa["taxMinor"] != "0" || ipa["shippingMinor"] != "0" {
		t.Fatalf("initial allocation amounts: %v", ipa)
	}

	// The atomic purchase is a normal purchase: line description is the
	// plant name, allocation points back at the plant.
	r = e.gql(`query { purchases(first: 10) { items { id totalMinor
		lines(first: 100) { items { description allocations { plantId lineMinor } } } } } }`, nil)
	items := dataOf(t, r, "purchases")["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("atomic purchase count: %v", items)
	}
	p := items[0].(map[string]any)
	if p["totalMinor"] != "3000" {
		t.Fatalf("atomic purchase total: %v", p)
	}
	l := p["lines"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if l["description"] != "Priced fig" {
		t.Fatalf("line description: %v", l)
	}
	if al := l["allocations"].([]any)[0].(map[string]any); al["plantId"] != plantID || al["lineMinor"] != "3000" {
		t.Fatalf("atomic allocation: %v", al)
	}

	// UNKNOWN + initialPurchase: refused.
	r = e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"name": "Bad combo", "type": "INDIVIDUAL", "category": "OTHER",
			"acquisition":     map[string]any{"pricingState": "UNKNOWN"},
			"initialPurchase": map[string]any{"currency": "USD", "totalMinor": "100"},
		}})
	wantProblemCode(t, payloadProblems(t, r, "createPlant"), "VALIDATION")

	// KNOWN without initialPurchase: refused.
	r = e.gql(`mutation($input: CreatePlantInput!) {
		createPlant(input: $input) { plant { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"name": "Known no price", "type": "INDIVIDUAL", "category": "OTHER",
			"acquisition": map[string]any{"pricingState": "KNOWN"},
		}})
	wantProblemCode(t, payloadProblems(t, r, "createPlant"), "VALIDATION")

	// Nothing extra landed from the refused creates.
	r = e.gql(`query { plants(first: 10) { items { id } } }`, nil)
	if items := dataOf(t, r, "plants")["items"].([]any); len(items) != 1 {
		t.Fatalf("refused plants persisted: %v", items)
	}
}

// The four acquisition states from 03 (acquisition-states.json): KNOWN
// via allocation, UNKNOWN, FREE as a zero-total purchase, and no
// record at all. UNKNOWN forbids pricing sources; both sources at once
// are refused.
func TestAcquisitionStatesOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())

	// FREE: zero-total purchase, displays as priced (not missing).
	free := mustPlant(t, e, "Fig cutting from friend")
	r := e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { `+acqFields+` } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"plantId": free, "seller": "Friend", "acquiredOn": "2025-07-01", "pricingState": "KNOWN",
			"newPurchase": map[string]any{"currency": "USD", "totalMinor": "0", "lineDescription": "Free cutting"},
		}})
	acq := dataOf(t, r, "recordAcquisition")["acquisition"].(map[string]any)
	if acq["pricingState"] != "KNOWN" {
		t.Fatalf("free acquisition: %v (%s)", acq, r.Raw)
	}
	ipa := acq["initialPurchaseAllocation"].(map[string]any)
	if ipa["lineMinor"] != "0" || ipa["taxMinor"] != "0" || ipa["shippingMinor"] != "0" {
		t.Fatalf("free allocation: %v", ipa)
	}

	// UNKNOWN with a pricing source: refused.
	unk := mustPlant(t, e, "Meyer lemon")
	r = e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"plantId": unk, "pricingState": "UNKNOWN",
			"newPurchase": map[string]any{"currency": "USD", "totalMinor": "100", "lineDescription": "x"},
		}})
	wantProblemCode(t, payloadProblems(t, r, "recordAcquisition"), "VALIDATION")

	// Both sources at once: refused.
	r = e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{
			"plantId": unk, "pricingState": "KNOWN", "purchaseAllocationId": "5f0c1b6e-1111-4aaa-8bbb-0000000000ee",
			"newPurchase": map[string]any{"currency": "USD", "totalMinor": "100", "lineDescription": "x"},
		}})
	wantProblemCode(t, payloadProblems(t, r, "recordAcquisition"), "VALIDATION")

	// Plain UNKNOWN lands; the third plant has no record at all.
	r = e.gql(`mutation($input: RecordAcquisitionInput!) {
		recordAcquisition(input: $input) { acquisition { pricingState } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"plantId": unk, "seller": "Local nursery", "pricingState": "UNKNOWN"}})
	if acq := dataOf(t, r, "recordAcquisition")["acquisition"].(map[string]any); acq["pricingState"] != "UNKNOWN" {
		t.Fatalf("unknown acquisition: %v", acq)
	}
	none := mustPlant(t, e, "Blackberry group")
	if acq := plantAcq(t, e, none); acq != nil {
		t.Fatalf("plant without acquisition record must project null: %v", acq)
	}

	// Counts: 1 unknown acquisition, 1 missing record; free purchase is
	// a real (zero) expense in the currency total.
	sp := spendingOf(t, e, nil)
	if sp["unknownAcquisitionCostCount"] != float64(1) || sp["missingAcquisitionRecordCount"] != float64(1) {
		t.Fatalf("counts: %v", sp)
	}
	if got := knownTotal(t, sp, "USD"); got != "0" {
		t.Fatalf("free purchase total: %q", got)
	}
}

// Multi-currency grouping: totals never merge across currencies.
func TestSpendingMultiCurrencyOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "10000",
		"lines": []any{lineInput("US fig", "10000", allocInput(fig, "10000", "0", "0"))},
	}, nil)
	createPurchase(t, e, map[string]any{
		"currency": "EUR", "totalMinor": "8000",
		"lines": []any{lineInput("EU fig", "8000", allocInput(fig, "8000", "0", "0"))},
	}, nil)

	sp := spendingOf(t, e, nil)
	if got := knownTotal(t, sp, "USD"); got != "10000" {
		t.Fatalf("USD group: %q", got)
	}
	if got := knownTotal(t, sp, "EUR"); got != "8000" {
		t.Fatalf("EUR group: %q", got)
	}
	if rows := sp["knownSpendingByCurrency"].([]any); len(rows) != 2 {
		t.Fatalf("want two currency groups: %v", sp)
	}

	// Currency filter on the list.
	r := e.gql(`query($f: PurchaseFilter) { purchases(filter: $f) { items { currency } } }`,
		map[string]any{"f": map[string]any{"currency": "EUR"}})
	items := dataOf(t, r, "purchases")["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["currency"] != "EUR" {
		t.Fatalf("currency filter: %s", r.Raw)
	}
}

// Header-only amount edits are conservation-checked against the stored
// line tree, and a purchase whose lines are untouched keeps its
// allocations intact.
func TestHeaderOnlyEditOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")

	p := createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "10000", "shippingMinor": "500", "taxMinor": "500",
		"lines": []any{lineInput("Fig", "9000", allocInput(fig, "9000", "500", "500"))},
	}, nil)
	allocID := lineAllocs(p, 0)[0].(map[string]any)["id"].(string)

	// New total inconsistent with the stored lines: refused.
	r := e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"id": p["id"], "expectedRevision": 1, "totalMinor": "11000"}})
	wantProblemCode(t, payloadProblems(t, r, "updatePurchase"), "VALIDATION")

	// Seller-only edit lands; amounts and allocations untouched.
	r = e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id seller revision totalMinor
			lines(first: 100) { items { allocations { id } } } } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"id": p["id"], "expectedRevision": 1, "seller": "Renamed nursery"}})
	top := dataOf(t, r, "updatePurchase")
	if raw, _ := json.Marshal(top["problems"]); string(raw) != "[]" {
		t.Fatalf("seller edit: %s", r.Raw)
	}
	upd := top["purchase"].(map[string]any)
	if upd["seller"] != "Renamed nursery" || upd["revision"] != float64(2) || upd["totalMinor"] != "10000" {
		t.Fatalf("seller edit result: %v", upd)
	}
	if got := lineAllocs(upd, 0)[0].(map[string]any)["id"].(string); got != allocID {
		t.Fatalf("header edit must not churn allocations: %v", got)
	}
}

// Voided purchases refuse further edits.
func TestVoidedPurchaseImmutableOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	fig := mustPlant(t, e, "Fig")
	p := createPurchase(t, e, map[string]any{
		"currency": "USD", "totalMinor": "100",
		"lines": []any{lineInput("x", "100", allocInput(fig, "100", "0", "0"))},
	}, nil)

	r := e.gql(`mutation($input: VoidPurchaseInput!) {
		voidPurchase(input: $input) { purchase { id } problems { code } } }`,
		map[string]any{"input": map[string]any{"id": p["id"], "expectedRevision": 1}})
	if len(payloadProblems(t, r, "voidPurchase")) != 0 {
		t.Fatalf("void: %s", r.Raw)
	}

	r = e.gql(`mutation($input: UpdatePurchaseInput!) {
		updatePurchase(input: $input) { purchase { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"id": p["id"], "expectedRevision": 2, "seller": "zombie"}})
	wantProblemCode(t, payloadProblems(t, r, "updatePurchase"), "CONFLICT")

	// Double void: CONFLICT.
	r = e.gql(`mutation($input: VoidPurchaseInput!) {
		voidPurchase(input: $input) { purchase { id } problems { code field message } } }`,
		map[string]any{"input": map[string]any{"id": p["id"], "expectedRevision": 2}})
	wantProblemCode(t, payloadProblems(t, r, "voidPurchase"), "CONFLICT")
}
