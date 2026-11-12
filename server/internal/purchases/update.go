package purchases

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/journal"
	"canopy/server/internal/money"
)

// UpdatePurchaseInput mirrors UpdatePurchaseInput. Lines is a full
// replacement set when non-nil; retained lines/allocations keep their
// ids. UnlinkAcquisitionIDs is the explicit opt-in required before an
// acquisition-linked allocation may disappear or move.
type UpdatePurchaseInput struct {
	ID                   string
	ExpectedRevision     int32
	Seller               *string
	PurchasedOn          *time.Time
	Currency             *string
	Total                *int64
	Tax                  *int64
	Shipping             *int64
	UnlinkAcquisitionIDs []string
	Lines                []LineInput
}

// UpdatePurchase applies header edits and (optionally) a full line-set
// replacement in one transaction under a purchase row lock. Acquisition
// links are protected: linked allocations must be retained on the same
// plant unless the command names their acquisitions in
// unlinkAcquisitionIds, in which case those acquisitions are marked
// UNKNOWN first (03 pricing transitions).
func (s *Service) UpdatePurchase(ctx context.Context, a Actor, in UpdatePurchaseInput, cmid *string) (Result, []Problem, error) {
	id, ok := parseUUID(in.ID)
	if !ok {
		return Result{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}, nil
	}
	wire := map[string]any{"id": in.ID, "rev": in.ExpectedRevision, "currency": in.Currency,
		"total": in.Total, "unlink": in.UnlinkAcquisitionIDs, "lines": len(in.Lines)}
	return mutate[Result](s, ctx, a, "updatePurchase", cmid, wire,
		func(ctx context.Context, q *dbgen.Queries) (Result, []Problem) {
			prow, err := q.LockPurchase(ctx, dbgen.LockPurchaseParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return Result{}, notFound("input.id", "purchase not found")
				}
				s.Log.Error("lock purchase", "err", err)
				return Result{}, []Problem{problem(CodeValidation, "", "purchase lock failed")}
			}
			if prow.Revision != in.ExpectedRevision {
				return Result{}, conflictWith(prow.Revision)
			}
			if prow.VoidedAt.Valid {
				return Result{}, []Problem{problem(CodeConflict, "input.id", "purchase is voided and cannot be edited")}
			}

			// Effective header after applying present-field updates.
			cur := headerSpec(prow)
			if in.Total != nil {
				cur.Total = *in.Total
			}
			if in.Tax != nil {
				cur.Tax = *in.Tax
			}
			if in.Shipping != nil {
				cur.Shipping = *in.Shipping
			}
			if in.Currency != nil && !money.ValidCurrency(*in.Currency) {
				return Result{}, []Problem{problem(CodeValidation, "input.currency", "currency must be an ISO-4217 three-letter code")}
			}

			if in.Lines != nil {
				if probs := s.replaceLines(ctx, q, a, id, prow, in); len(probs) > 0 {
					return Result{}, probs
				}
				// Conservation against the effective header.
				spec := specOf(CreatePurchaseInput{
					Currency: prow.Currency, Total: cur.Total, Tax: cur.Tax, Shipping: cur.Shipping,
					Lines: in.Lines,
				})
				if vs := money.Validate(spec); len(vs) > 0 {
					return Result{}, moneyViolations(vs)
				}
			} else if in.Total != nil || in.Tax != nil || in.Shipping != nil {
				// Header-only amount change: existing allocations must still
				// balance against the new amounts, exactly.
				spec, probs := s.currentSpec(ctx, q, id)
				if len(probs) > 0 {
					return Result{}, probs
				}
				spec.Total, spec.Tax, spec.Shipping = cur.Total, cur.Tax, cur.Shipping
				if vs := money.Validate(spec); len(vs) > 0 {
					return Result{}, moneyViolations(vs)
				}
			}

			updated, err := q.UpdatePurchaseHeader(ctx, dbgen.UpdatePurchaseHeaderParams{
				Seller: textPtr(in.Seller), PurchasedOn: datePtr(in.PurchasedOn),
				Currency:   textPtr(in.Currency),
				TotalMinor: numericPtr(in.Total), ShippingMinor: numericPtr(in.Shipping), TaxMinor: numericPtr(in.Tax),
				ID: id, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return Result{}, s.conflictProblem(ctx, q, id, a.WorkspaceID, err)
			}
			if err := s.audit(ctx, q, a, "updatePurchase", "purchase", id, updated.Revision, prow); err != nil {
				return Result{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			// journal re-projection (T18): allocation edits and header
			// date/seller edits both move or relabel the entries, in tx.
			if err := journal.ProjectPurchaseEntries(ctx, q, s.uuid.New, updated); err != nil {
				s.Log.Error("project purchase entries", "err", err)
				return Result{}, []Problem{problem(CodeValidation, "", "journal projection failed")}
			}
			return Result{ID: uuidString(id), Revision: updated.Revision}, nil
		})
}

func headerSpec(p dbgen.Purchase) money.PurchaseSpec {
	t, _ := numericMinor(p.TotalMinor)
	tax, _ := numericMinor(p.TaxMinor)
	sh, _ := numericMinor(p.ShippingMinor)
	return money.PurchaseSpec{Total: t, Tax: tax, Shipping: sh}
}

// numericMinor converts a DB numeric to an int64 minor amount; ok=false
// when the value is not a nonnegative integer that fits.
func numericMinor(n pgtype.Numeric) (int64, bool) {
	if !n.Valid || n.NaN || n.Int == nil || n.InfinityModifier != pgtype.Finite {
		return 0, false
	}
	v := new(big.Int).Set(n.Int)
	switch {
	case n.Exp == 0:
	case n.Exp < 0:
		div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n.Exp)), nil)
		if new(big.Int).Mod(v, div).Sign() != 0 {
			return 0, false // fractional: never a valid minor amount
		}
		v.Div(v, div)
	default:
		mul := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n.Exp)), nil)
		v.Mul(v, mul)
	}
	if v.Sign() < 0 || !v.IsInt64() {
		return 0, false
	}
	return v.Int64(), true
}

func numericPtr(v *int64) *pgtype.Numeric {
	if v == nil {
		return nil
	}
	n := minor(*v)
	return &n
}

func conflictWith(rev int32) []Problem {
	r := rev
	return []Problem{Problem{Code: CodeConflict, Field: "input.expectedRevision",
		Message: "purchase was modified concurrently", CurrentRevision: &r}}
}

// replaceLines validates the replacement set against the current state
// (retention rules + acquisition-link protection), performs explicit
// unlink reverts, then deletes and re-inserts the line tree.
func (s *Service) replaceLines(ctx context.Context, q *dbgen.Queries, a Actor, purchaseID pgtype.UUID,
	prow dbgen.Purchase, in UpdatePurchaseInput,
) []Problem {
	curLines, err := q.ListPurchaseLines(ctx, purchaseID)
	if err != nil {
		s.Log.Error("list purchase lines", "err", err)
		return []Problem{problem(CodeValidation, "", "purchase lines could not be read")}
	}
	curAllocs, err := q.ListAllocationsByPurchase(ctx, purchaseID)
	if err != nil {
		s.Log.Error("list allocations", "err", err)
		return []Problem{problem(CodeValidation, "", "allocations could not be read")}
	}
	linked, err := q.AcquisitionsForPurchase(ctx, dbgen.AcquisitionsForPurchaseParams{
		PurchaseID: purchaseID, WorkspaceID: a.WorkspaceID,
	})
	if err != nil {
		s.Log.Error("linked acquisitions", "err", err)
		return []Problem{problem(CodeValidation, "", "acquisition links could not be read")}
	}

	lineOwner := map[string]string{} // allocation id -> current line id
	allocPlant := map[string]*string{}
	allocByID := map[string]dbgen.PurchaseAllocation{}
	for _, al := range curAllocs {
		lineOwner[uuidString(al.ID)] = uuidString(al.PurchaseLineID)
		allocByID[uuidString(al.ID)] = al
		if al.PlantID.Valid {
			p := uuidString(al.PlantID)
			allocPlant[uuidString(al.ID)] = &p
		} else {
			allocPlant[uuidString(al.ID)] = nil
		}
	}
	// acquisition id -> allocation id (only links into this purchase)
	linkByAlloc := map[string]dbgen.Acquisition{}
	linkedAcqIDs := map[string]bool{}
	for _, acq := range linked {
		if acq.InitialPurchaseAllocationID.Valid {
			linkByAlloc[uuidString(acq.InitialPurchaseAllocationID)] = acq
			linkedAcqIDs[uuidString(acq.ID)] = true
		}
	}

	// Unlink opt-ins must name real linked acquisitions of this purchase.
	unlink := map[string]bool{}
	for _, raw := range in.UnlinkAcquisitionIDs {
		aid, ok := parseUUID(raw)
		if !ok {
			return []Problem{problem(CodeValidation, "input.unlinkAcquisitionIds", "malformed id")}
		}
		key := uuidString(aid)
		if !linkedAcqIDs[key] {
			return []Problem{problem(CodeValidation, "input.unlinkAcquisitionIds",
				"acquisition is not linked to an allocation of this purchase")}
		}
		unlink[key] = true
	}

	// Retention validation + link protection over the replacement set.
	seenLineIDs := map[string]bool{}
	seenAllocIDs := map[string]bool{}
	for i, l := range in.Lines {
		f := fmt.Sprintf("input.lines[%d]", i)
		if l.ID != nil {
			if _, ok := parseUUID(*l.ID); !ok {
				return []Problem{problem(CodeValidation, f+".id", "malformed line id")}
			}
			if seenLineIDs[*l.ID] {
				return []Problem{problem(CodeValidation, f+".id", "duplicate line id in replacement set")}
			}
			found := false
			for _, cl := range curLines {
				if uuidString(cl.ID) == *l.ID {
					found = true
					break
				}
			}
			if !found {
				return []Problem{problem(CodeValidation, f+".id", "line id does not belong to this purchase")}
			}
			seenLineIDs[*l.ID] = true
		}
		for j, al := range l.Allocations {
			af := fmt.Sprintf("%s.allocations[%d]", f, j)
			if al.ID == nil {
				continue
			}
			cur, exists := allocByID[*al.ID]
			if !exists {
				return []Problem{problem(CodeValidation, af+".id", "allocation id does not belong to this purchase")}
			}
			if seenAllocIDs[*al.ID] {
				return []Problem{problem(CodeValidation, af+".id", "duplicate allocation id in replacement set")}
			}
			seenAllocIDs[*al.ID] = true
			owner := ""
			if l.ID != nil {
				owner = *l.ID
			}
			if lineOwner[*al.ID] != owner {
				return []Problem{problem(CodeValidation, af+".id",
					"allocations cannot move between lines; delete and re-create instead")}
			}
			_ = cur
			// Plant reassignment of a linked allocation requires unlink opt-in.
			if acq, linkedAlloc := linkByAlloc[*al.ID]; linkedAlloc {
				oldPlant, newPlant := allocPlant[*al.ID], al.PlantID
				if !samePlant(oldPlant, newPlant) && !unlink[uuidString(acq.ID)] {
					return []Problem{problem(CodeValidation, af+".plantId", fmt.Sprintf(
						"allocation is the acquisition price of %s; reassigning it requires unlinkAcquisitionIds confirmation",
						uuidString(acq.ID)))}
				}
			}
		}
	}
	// Any linked allocation that vanishes from the set needs its
	// acquisition named.
	for allocID, acq := range linkByAlloc {
		if seenAllocIDs[allocID] {
			continue
		}
		if !unlink[uuidString(acq.ID)] {
			return []Problem{problem(CodeValidation, "input.lines", fmt.Sprintf(
				"allocation %s is the acquisition price of %s; removing it requires unlinkAcquisitionIds confirmation",
				allocID, uuidString(acq.ID)))}
		}
	}

	// Explicit unlinks: mark acquisitions UNKNOWN first (stable id order,
	// already ordered by the query), each under its own revision guard.
	for _, acq := range linked {
		if !unlink[uuidString(acq.ID)] {
			continue
		}
		reverted, err := q.RevertAcquisitionUnknown(ctx, dbgen.RevertAcquisitionUnknownParams{
			ID: acq.ID, WorkspaceID: a.WorkspaceID, ExpectedRevision: acq.Revision,
		})
		if err != nil {
			return s.acqConflict(ctx, q, acq.ID, a.WorkspaceID, err)
		}
		if err := s.audit(ctx, q, a, "updatePurchase.unlink", "acquisition", acq.ID, reverted.Revision, acq); err != nil {
			return []Problem{problem(CodeValidation, "", "audit write failed")}
		}
	}

	if _, probs := resolvePlants(ctx, q, a, in.Lines); len(probs) > 0 {
		return probs
	}
	if err := q.DeletePurchaseLines(ctx, purchaseID); err != nil {
		s.Log.Error("delete purchase lines", "err", err)
		return []Problem{problem(CodeValidation, "", "line replacement failed")}
	}
	return insertLines(ctx, s, q, a.WorkspaceID, purchaseID, in.Lines)
}

// currentSpec rebuilds the arithmetic spec from stored lines/allocations
// so header-only edits can be conservation-checked against what exists.
func (s *Service) currentSpec(ctx context.Context, q *dbgen.Queries, purchaseID pgtype.UUID) (money.PurchaseSpec, []Problem) {
	lines, err := q.ListPurchaseLines(ctx, purchaseID)
	if err != nil {
		s.Log.Error("list purchase lines", "err", err)
		return money.PurchaseSpec{}, []Problem{problem(CodeValidation, "", "purchase lines could not be read")}
	}
	allocs, err := q.ListAllocationsByPurchase(ctx, purchaseID)
	if err != nil {
		s.Log.Error("list allocations", "err", err)
		return money.PurchaseSpec{}, []Problem{problem(CodeValidation, "", "allocations could not be read")}
	}
	byLine := map[string][]money.AllocationSpec{}
	var spec money.PurchaseSpec
	for _, al := range allocs {
		ln, ok1 := numericMinor(al.LineMinor)
		tx, ok2 := numericMinor(al.TaxMinor)
		sh, ok3 := numericMinor(al.ShippingMinor)
		if !ok1 || !ok2 || !ok3 {
			return money.PurchaseSpec{}, []Problem{problem(CodeValidation, "", "stored allocation is not a valid minor amount")}
		}
		byLine[uuidString(al.PurchaseLineID)] = append(byLine[uuidString(al.PurchaseLineID)],
			money.AllocationSpec{PlantSet: al.PlantID.Valid, Line: ln, Tax: tx, Shipping: sh})
	}
	for i, l := range lines {
		lt, ok := numericMinor(l.LineTotalMinor)
		if !ok {
			return money.PurchaseSpec{}, []Problem{problem(CodeValidation, "", "stored line total is not a valid minor amount")}
		}
		spec.Lines = append(spec.Lines, money.LineSpec{
			Index: i, Description: l.Description, Total: lt,
			Allocations: byLine[uuidString(l.ID)],
		})
	}
	return spec, nil
}

func samePlant(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (s *Service) acqConflict(ctx context.Context, q *dbgen.Queries, id, ws pgtype.UUID, err error) []Problem {
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.Log.Error("acquisition guarded update", "err", err)
		return []Problem{problem(CodeValidation, "", "acquisition update failed")}
	}
	cur, cerr := q.GetAcquisition(ctx, dbgen.GetAcquisitionParams{ID: id, WorkspaceID: ws})
	if cerr != nil {
		return notFound("input.acquisitionId", "acquisition not found")
	}
	rev := cur.Revision
	return []Problem{Problem{Code: CodeConflict, Field: "input.expectedRevision",
		Message: "acquisition was modified concurrently", CurrentRevision: &rev}}
}
