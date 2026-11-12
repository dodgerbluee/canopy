package purchases

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/journal"
	"canopy/server/internal/money"
	"canopy/server/internal/platform"
)

// AllocationInput is one allocation in a command. PlantID nil =
// unallocated supplies bucket.
type AllocationInput struct {
	ID       *string // retained allocation id on update
	PlantID  *string
	Line     int64
	Tax      int64
	Shipping int64
}

// LineInput is one purchase line in a command.
type LineInput struct {
	ID          *string // retained line id on update
	Description string
	Quantity    string // decimal string, > 0
	LineTotal   int64
	Allocations []AllocationInput
}

// CreatePurchaseInput mirrors CreatePurchaseInput with amounts already
// parsed to minor ints by the transport.
type CreatePurchaseInput struct {
	Seller      *string
	PurchasedOn *time.Time
	Currency    string
	Total       int64
	Tax         int64
	Shipping    int64
	Lines       []LineInput
}

// Result identifies the affected purchase aggregate.
type Result struct {
	ID       string `json:"id"`
	Revision int32  `json:"revision"`
}

var quantityRe = regexp.MustCompile(`^\d+(\.\d{1,6})?$`)

// validateShape checks currency, line/quantity bounds and id well-formedness
// before any DB work; returns payload problems.
func validateShape(in CreatePurchaseInput, fieldPrefix string) []Problem {
	var out []Problem
	if !money.ValidCurrency(in.Currency) {
		out = append(out, problem(CodeValidation, fieldPrefix+".currency", "currency must be an ISO-4217 three-letter code"))
	}
	if len(in.Lines) == 0 {
		out = append(out, problem(CodeValidation, fieldPrefix+".lines", "at least one line is required"))
	}
	if len(in.Lines) > 100 {
		out = append(out, problem(CodeValidation, fieldPrefix+".lines", "at most 100 lines per purchase"))
	}
	for i, l := range in.Lines {
		f := fmt.Sprintf("%s.lines[%d]", fieldPrefix, i)
		if len(l.Description) < 1 || len(l.Description) > 200 {
			out = append(out, problem(CodeValidation, f+".description", "description must be 1..200 characters"))
		}
		if !validQuantity(l.Quantity) {
			out = append(out, problem(CodeValidation, f+".quantity", "quantity must be a positive decimal with at most 6 fraction digits"))
		}
		if len(l.Allocations) > 25 {
			// The complexity model charges plain lists at the SDL default
			// page size (25); enforcing the same cap server-side keeps the
			// worst-case read cost provably equal to the charged cost.
			out = append(out, problem(CodeValidation, f+".allocations", "at most 25 allocations per line"))
		}
		for _, al := range l.Allocations {
			if al.ID != nil {
				if _, ok := parseUUID(*al.ID); !ok {
					out = append(out, problem(CodeValidation, f+".allocations", "malformed allocation id"))
				}
			}
			if al.PlantID != nil {
				if _, ok := parseUUID(*al.PlantID); !ok {
					out = append(out, problem(CodeValidation, f+".allocations", "malformed plantId"))
				}
			}
		}
	}
	return out
}

func validQuantity(s string) bool {
	if !quantityRe.MatchString(s) {
		return false
	}
	d, ok := new(big.Rat).SetString(s)
	if !ok {
		return false
	}
	return d.Sign() > 0 && d.Cmp(big.NewRat(1_000_000_000, 1)) < 0
}

func specOf(in CreatePurchaseInput) money.PurchaseSpec {
	spec := money.PurchaseSpec{Total: in.Total, Tax: in.Tax, Shipping: in.Shipping}
	for i, l := range in.Lines {
		ls := money.LineSpec{Index: i, Description: l.Description, Total: l.LineTotal}
		for j, al := range l.Allocations {
			ls.Allocations = append(ls.Allocations, money.AllocationSpec{
				Index: j, PlantSet: al.PlantID != nil, Line: al.Line, Tax: al.Tax, Shipping: al.Shipping,
			})
		}
		spec.Lines = append(spec.Lines, ls)
	}
	return spec
}

func moneyViolations(vs []money.Violation) []Problem {
	out := make([]Problem, 0, len(vs))
	for _, v := range vs {
		out = append(out, problem(CodeValidation, v.Field, v.Message))
	}
	return out
}

// CreatePurchase inserts purchase + lines + allocations in one
// transaction after full conservation validation.
func (s *Service) CreatePurchase(ctx context.Context, a Actor, in CreatePurchaseInput, cmid *string) (Result, []Problem, error) {
	wire := map[string]any{"currency": in.Currency, "total": in.Total, "tax": in.Tax, "shipping": in.Shipping,
		"seller": in.Seller, "lines": len(in.Lines)}
	return mutate[Result](s, ctx, a, "createPurchase", cmid, wire,
		func(ctx context.Context, q *dbgen.Queries) (Result, []Problem) {
			if probs := validateShape(in, "input"); len(probs) > 0 {
				return Result{}, probs
			}
			if vs := money.Validate(specOf(in)); len(vs) > 0 {
				return Result{}, moneyViolations(vs)
			}
			// Every referenced plant must exist in this workspace.
			plants, probs := resolvePlants(ctx, q, a, in.Lines)
			if len(probs) > 0 {
				return Result{}, probs
			}
			_ = plants
			prow, err := q.InsertPurchase(ctx, dbgen.InsertPurchaseParams{
				ID: s.uuid.New(), WorkspaceID: a.WorkspaceID,
				Seller: textPtr(in.Seller), PurchasedOn: datePtr(in.PurchasedOn),
				Currency: in.Currency, TotalMinor: minor(in.Total),
				ShippingMinor: minor(in.Shipping), TaxMinor: minor(in.Tax),
			})
			if err != nil {
				s.Log.Error("insert purchase", "err", err)
				return Result{}, []Problem{problem(CodeValidation, "input", "purchase could not be stored")}
			}
			if probs := insertLines(ctx, s, q, a.WorkspaceID, prow.ID, in.Lines); len(probs) > 0 {
				return Result{}, probs
			}
			if err := s.audit(ctx, q, a, "createPurchase", "purchase", prow.ID, prow.Revision, nil); err != nil {
				return Result{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			// journal projection (T18), same tx: one PURCHASE entry per
			// plant-bearing allocation, keyed by allocation id.
			if err := journal.ProjectPurchaseEntries(ctx, q, s.uuid.New, prow); err != nil {
				s.Log.Error("project purchase entries", "err", err)
				return Result{}, []Problem{problem(CodeValidation, "", "journal projection failed")}
			}
			return Result{ID: uuidString(prow.ID), Revision: prow.Revision}, nil
		})
}

func resolvePlants(ctx context.Context, q *dbgen.Queries, a Actor, lines []LineInput) (map[string]bool, []Problem) {
	seen := map[string]bool{}
	for _, l := range lines {
		for _, al := range l.Allocations {
			if al.PlantID == nil || seen[*al.PlantID] {
				continue
			}
			seen[*al.PlantID] = true
			pid, ok := parseUUID(*al.PlantID)
			if !ok {
				return nil, []Problem{problem(CodeValidation, "input.lines.allocations.plantId", "malformed plantId")}
			}
			if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: pid, WorkspaceID: a.WorkspaceID}); err != nil {
				return nil, []Problem{problem(CodeNotFound, "input.lines.allocations.plantId", "plant not found")}
			}
		}
	}
	return seen, nil
}

// insertLines writes the line/allocation tree for a purchase. Retained
// ids are reused verbatim; new rows get fresh ids.
func insertLines(ctx context.Context, s *Service, q *dbgen.Queries, ws, purchaseID pgtype.UUID, lines []LineInput) []Problem {
	for i, l := range lines {
		lineID := s.uuid.New()
		if l.ID != nil {
			u, ok := parseUUID(*l.ID)
			if !ok {
				return []Problem{problem(CodeValidation, fmt.Sprintf("input.lines[%d].id", i), "malformed line id")}
			}
			lineID = u
		}
		qty, ok := scanNumeric(l.Quantity)
		if !ok {
			return []Problem{problem(CodeValidation, fmt.Sprintf("input.lines[%d].quantity", i), "malformed quantity")}
		}
		line, err := q.InsertPurchaseLine(ctx, dbgen.InsertPurchaseLineParams{
			ID: lineID, WorkspaceID: ws, PurchaseID: purchaseID,
			Description: l.Description, Quantity: qty, LineTotalMinor: minor(l.LineTotal),
			Position: int16(i),
		})
		if err != nil {
			if isUniqueViolation(err) {
				return []Problem{problem(CodeConflict, fmt.Sprintf("input.lines[%d].id", i), "line id already exists on another purchase")}
			}
			s.Log.Error("insert purchase line", "err", err)
			return []Problem{problem(CodeValidation, "input.lines", "line could not be stored")}
		}
		for j, al := range l.Allocations {
			allocID := s.uuid.New()
			if al.ID != nil {
				u, ok := parseUUID(*al.ID)
				if !ok {
					return []Problem{problem(CodeValidation, fmt.Sprintf("input.lines[%d].allocations[%d].id", i, j), "malformed allocation id")}
				}
				allocID = u
			}
			var plantID pgtype.UUID
			if al.PlantID != nil {
				u, ok := parseUUID(*al.PlantID)
				if !ok {
					return []Problem{problem(CodeValidation, fmt.Sprintf("input.lines[%d].allocations[%d].plantId", i, j), "malformed plantId")}
				}
				plantID = u
			}
			_, err := q.InsertPurchaseAllocation(ctx, dbgen.InsertPurchaseAllocationParams{
				ID: allocID, WorkspaceID: ws, PurchaseLineID: line.ID, PlantID: plantID,
				LineMinor: minor(al.Line), TaxMinor: minor(al.Tax), ShippingMinor: minor(al.Shipping),
				Position: int16(j),
			})
			if err != nil {
				if isUniqueViolation(err) {
					return []Problem{problem(CodeValidation, fmt.Sprintf("input.lines[%d].allocations", i),
						"a line cannot allocate the same plant twice; merge the amounts")}
				}
				s.Log.Error("insert purchase allocation", "err", err)
				return []Problem{problem(CodeValidation, "input.lines.allocations", "allocation could not be stored")}
			}
		}
	}
	return nil
}

func scanNumeric(s string) (pgtype.Numeric, bool) {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil || !n.Valid {
		return pgtype.Numeric{}, false
	}
	return n, true
}

// ---- atomic single-line purchase (shared with collection) -----------------

// AtomicSpec describes the single-line, single-allocation purchase that
// createPlant(initialPurchase) and recordAcquisition/priceAcquisition
// (newPurchase) create inside an existing transaction.
type AtomicSpec struct {
	Currency    string
	Total       int64
	Tax         int64
	Shipping    int64
	Seller      *string
	PurchasedOn *time.Time
	LineDesc    string
	PlantID     pgtype.UUID
}

// CreateAtomic inserts purchase + one line + one allocation for plantID
// inside the caller's transaction. The allocation carries the whole
// purchase (line = total - tax - shipping, plus all overhead), so
// conservation holds by construction.
func CreateAtomic(ctx context.Context, q *dbgen.Queries, ws pgtype.UUID, spec AtomicSpec) (dbgen.Purchase, dbgen.PurchaseLine, dbgen.PurchaseAllocation, error) {
	ids := platform.UUIDv4{}
	prow, err := q.InsertPurchase(ctx, dbgen.InsertPurchaseParams{
		ID: ids.New(), WorkspaceID: ws,
		Seller: textPtr(spec.Seller), PurchasedOn: datePtr(spec.PurchasedOn),
		Currency: spec.Currency, TotalMinor: minor(spec.Total),
		ShippingMinor: minor(spec.Shipping), TaxMinor: minor(spec.Tax),
	})
	if err != nil {
		return dbgen.Purchase{}, dbgen.PurchaseLine{}, dbgen.PurchaseAllocation{}, err
	}
	line, err := q.InsertPurchaseLine(ctx, dbgen.InsertPurchaseLineParams{
		ID: ids.New(), WorkspaceID: ws, PurchaseID: prow.ID,
		Description: spec.LineDesc, Quantity: pgtype.Numeric{Int: big.NewInt(1), Exp: 0, Valid: true},
		LineTotalMinor: minor(spec.Total - spec.Tax - spec.Shipping),
		Position:       0,
	})
	if err != nil {
		return dbgen.Purchase{}, dbgen.PurchaseLine{}, dbgen.PurchaseAllocation{}, err
	}
	alloc, err := q.InsertPurchaseAllocation(ctx, dbgen.InsertPurchaseAllocationParams{
		ID: ids.New(), WorkspaceID: ws, PurchaseLineID: line.ID, PlantID: spec.PlantID,
		LineMinor: minor(spec.Total - spec.Tax - spec.Shipping),
		TaxMinor:  minor(spec.Tax), ShippingMinor: minor(spec.Shipping),
		Position: 0,
	})
	if err != nil {
		return dbgen.Purchase{}, dbgen.PurchaseLine{}, dbgen.PurchaseAllocation{}, err
	}
	// journal projection (T18): atomic acquisitions project their plant
	// card in the caller's tx, same protocol as CreatePurchase.
	if err := journal.ProjectPurchaseEntries(ctx, q, ids.New, prow); err != nil {
		return dbgen.Purchase{}, dbgen.PurchaseLine{}, dbgen.PurchaseAllocation{}, err
	}
	return prow, line, alloc, nil
}
