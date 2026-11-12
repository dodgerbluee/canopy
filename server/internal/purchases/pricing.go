package purchases

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/money"
)

// NewPricedSpec mirrors NewPricedPurchaseInput with parsed amounts.
type NewPricedSpec struct {
	Currency    string
	Total       int64
	Tax         int64
	Shipping    int64
	Seller      *string
	PurchasedOn *time.Time
	LineDesc    string
}

// PriceAcquisitionInput carries exactly one of the three pricing modes.
type PriceAcquisitionInput struct {
	AcquisitionID        string
	ExpectedRevision     int32
	PurchaseAllocationID *string
	NewPurchase          *NewPricedSpec
	RevertToUnknown      bool
}

// PriceResult names the affected acquisition and, when a purchase was
// created or used, that purchase too.
type PriceResult struct {
	AcquisitionID string `json:"acquisitionId"`
	Revision      int32  `json:"revision"`
	PurchaseID    string `json:"purchaseId,omitempty"`
}

// PriceAcquisition moves an acquisition between UNKNOWN and KNOWN.
// Lock order follows 03: purchase first, then acquisition, so a
// concurrent updatePurchase on the same purchase serializes cleanly.
func (s *Service) PriceAcquisition(ctx context.Context, a Actor, in PriceAcquisitionInput, cmid *string) (PriceResult, []Problem, error) {
	aid, ok := parseUUID(in.AcquisitionID)
	if !ok {
		return PriceResult{}, []Problem{problem(CodeValidation, "input.acquisitionId", "malformed id")}, nil
	}
	modes := 0
	if in.PurchaseAllocationID != nil {
		modes++
	}
	if in.NewPurchase != nil {
		modes++
	}
	if in.RevertToUnknown {
		modes++
	}
	if modes != 1 {
		return PriceResult{}, []Problem{problem(CodeValidation, "input",
			"exactly one of purchaseAllocationId, newPurchase or revertToUnknown is required")}, nil
	}
	wire := map[string]any{"id": in.AcquisitionID, "rev": in.ExpectedRevision, "modes": modes,
		"alloc": in.PurchaseAllocationID, "revert": in.RevertToUnknown}
	return mutate[PriceResult](s, ctx, a, "priceAcquisition", cmid, wire,
		func(ctx context.Context, q *dbgen.Queries) (PriceResult, []Problem) {
			if _, err := q.GetAcquisition(ctx, dbgen.GetAcquisitionParams{ID: aid, WorkspaceID: a.WorkspaceID}); err != nil {
				return PriceResult{}, notFound("input.acquisitionId", "acquisition not found")
			}
			switch {
			case in.PurchaseAllocationID != nil:
				return s.linkAllocation(ctx, q, a, aid, *in.PurchaseAllocationID, in.ExpectedRevision)
			case in.NewPurchase != nil:
				return s.createPriced(ctx, q, a, aid, *in.NewPurchase, in.ExpectedRevision)
			default:
				return s.revertUnknown(ctx, q, a, aid, in.ExpectedRevision)
			}
		})
}

func (s *Service) linkAllocation(ctx context.Context, q *dbgen.Queries, a Actor, aid pgtype.UUID,
	allocRaw string, expectedRevision int32,
) (PriceResult, []Problem) {
	allocID, ok := parseUUID(allocRaw)
	if !ok {
		return PriceResult{}, []Problem{problem(CodeValidation, "input.purchaseAllocationId", "malformed id")}
	}
	alloc, err := q.GetPurchaseAllocation(ctx, dbgen.GetPurchaseAllocationParams{ID: allocID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return PriceResult{}, notFound("input.purchaseAllocationId", "allocation not found")
	}
	// Purchase first, then acquisition (stable order).
	prow, err := q.LockPurchase(ctx, dbgen.LockPurchaseParams{ID: alloc.PurchaseID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return PriceResult{}, notFound("input.purchaseAllocationId", "allocation not found")
	}
	if prow.VoidedAt.Valid {
		return PriceResult{}, []Problem{problem(CodeConflict, "input.purchaseAllocationId",
			"cannot price from a voided purchase")}
	}
	acq, err := q.LockAcquisition(ctx, dbgen.LockAcquisitionParams{ID: aid, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return PriceResult{}, notFound("input.acquisitionId", "acquisition not found")
	}
	if acq.PricingState == dbgen.PricingStateKNOWN {
		return PriceResult{}, []Problem{problem(CodeConflict, "input.acquisitionId",
			"acquisition already has a price; revert to unknown first")}
	}
	if !alloc.PlantID.Valid || uuidString(alloc.PlantID) != uuidString(acq.PlantID) {
		return PriceResult{}, []Problem{problem(CodeValidation, "input.purchaseAllocationId",
			"allocation must belong to this acquisition's plant")}
	}
	linked, err := q.PriceAcquisitionLink(ctx, dbgen.PriceAcquisitionLinkParams{
		AllocationID: allocID, ID: aid, WorkspaceID: a.WorkspaceID, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return PriceResult{}, s.acqConflict(ctx, q, aid, a.WorkspaceID, err)
	}
	if err := s.audit(ctx, q, a, "priceAcquisition.link", "acquisition", aid, linked.Revision, acq); err != nil {
		return PriceResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
	}
	return PriceResult{AcquisitionID: uuidString(aid), Revision: linked.Revision,
		PurchaseID: uuidString(alloc.PurchaseID)}, nil
}

func (s *Service) createPriced(ctx context.Context, q *dbgen.Queries, a Actor, aid pgtype.UUID,
	np NewPricedSpec, expectedRevision int32,
) (PriceResult, []Problem) {
	var probs []Problem
	if !money.ValidCurrency(np.Currency) {
		probs = append(probs, problem(CodeValidation, "input.newPurchase.currency", "currency must be an ISO-4217 three-letter code"))
	}
	if np.Tax+np.Shipping > np.Total {
		probs = append(probs, problem(CodeValidation, "input.newPurchase.taxMinor",
			"tax + shipping exceeds total"))
	}
	if len(np.LineDesc) < 1 || len(np.LineDesc) > 200 {
		probs = append(probs, problem(CodeValidation, "input.newPurchase.lineDescription",
			"description must be 1..200 characters"))
	}
	if len(probs) > 0 {
		return PriceResult{}, probs
	}
	acq, err := q.LockAcquisition(ctx, dbgen.LockAcquisitionParams{ID: aid, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return PriceResult{}, notFound("input.acquisitionId", "acquisition not found")
	}
	if acq.PricingState == dbgen.PricingStateKNOWN {
		return PriceResult{}, []Problem{problem(CodeConflict, "input.acquisitionId",
			"acquisition already has a price; revert to unknown first")}
	}
	if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: acq.PlantID, WorkspaceID: a.WorkspaceID}); err != nil {
		return PriceResult{}, notFound("input.acquisitionId", "plant not found")
	}
	prow, _, alloc, err := CreateAtomic(ctx, q, a.WorkspaceID, AtomicSpec{
		Currency: np.Currency, Total: np.Total, Tax: np.Tax, Shipping: np.Shipping,
		Seller: np.Seller, PurchasedOn: np.PurchasedOn, LineDesc: np.LineDesc, PlantID: acq.PlantID,
	})
	if err != nil {
		s.Log.Error("atomic priced purchase", "err", err)
		return PriceResult{}, []Problem{problem(CodeValidation, "input.newPurchase", "purchase could not be stored")}
	}
	if err := s.audit(ctx, q, a, "priceAcquisition.createPurchase", "purchase", prow.ID, prow.Revision, nil); err != nil {
		return PriceResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
	}
	linked, err := q.PriceAcquisitionLink(ctx, dbgen.PriceAcquisitionLinkParams{
		AllocationID: alloc.ID, ID: aid, WorkspaceID: a.WorkspaceID, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return PriceResult{}, s.acqConflict(ctx, q, aid, a.WorkspaceID, err)
	}
	if err := s.audit(ctx, q, a, "priceAcquisition.link", "acquisition", aid, linked.Revision, acq); err != nil {
		return PriceResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
	}
	return PriceResult{AcquisitionID: uuidString(aid), Revision: linked.Revision,
		PurchaseID: uuidString(prow.ID)}, nil
}

func (s *Service) revertUnknown(ctx context.Context, q *dbgen.Queries, a Actor, aid pgtype.UUID,
	expectedRevision int32,
) (PriceResult, []Problem) {
	acq, err := q.LockAcquisition(ctx, dbgen.LockAcquisitionParams{ID: aid, WorkspaceID: a.WorkspaceID})
	if err != nil {
		return PriceResult{}, notFound("input.acquisitionId", "acquisition not found")
	}
	if acq.PricingState == dbgen.PricingStateUNKNOWN {
		return PriceResult{}, []Problem{problem(CodeConflict, "input.acquisitionId",
			"acquisition price is already unknown")}
	}
	reverted, err := q.RevertAcquisitionUnknown(ctx, dbgen.RevertAcquisitionUnknownParams{
		ID: aid, WorkspaceID: a.WorkspaceID, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return PriceResult{}, s.acqConflict(ctx, q, aid, a.WorkspaceID, err)
	}
	if err := s.audit(ctx, q, a, "priceAcquisition.revert", "acquisition", aid, reverted.Revision, acq); err != nil {
		return PriceResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
	}
	return PriceResult{AcquisitionID: uuidString(aid), Revision: reverted.Revision}, nil
}

// ResolveAllocationForPlant validates an existing allocation for a
// KNOWN acquisition at recordAcquisition time (collection calls this
// inside its own transaction).
func ResolveAllocationForPlant(ctx context.Context, q *dbgen.Queries, ws pgtype.UUID,
	allocRaw string, plantID pgtype.UUID,
) (dbgen.GetPurchaseAllocationRow, *Problem) {
	allocID, ok := parseUUID(allocRaw)
	if !ok {
		return dbgen.GetPurchaseAllocationRow{}, ptrProblem(problem(CodeValidation, "input.purchaseAllocationId", "malformed id"))
	}
	alloc, err := q.GetPurchaseAllocation(ctx, dbgen.GetPurchaseAllocationParams{ID: allocID, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetPurchaseAllocationRow{}, ptrProblem(problem(CodeNotFound, "input.purchaseAllocationId", "allocation not found"))
	}
	if err != nil {
		return dbgen.GetPurchaseAllocationRow{}, ptrProblem(problem(CodeValidation, "input.purchaseAllocationId", "allocation lookup failed"))
	}
	prow, err := q.GetPurchase(ctx, dbgen.GetPurchaseParams{ID: alloc.PurchaseID, WorkspaceID: ws})
	if err != nil || prow.VoidedAt.Valid {
		return dbgen.GetPurchaseAllocationRow{}, ptrProblem(problem(CodeConflict, "input.purchaseAllocationId",
			"cannot price from a voided purchase"))
	}
	if !alloc.PlantID.Valid || uuidString(alloc.PlantID) != uuidString(plantID) {
		return dbgen.GetPurchaseAllocationRow{}, ptrProblem(problem(CodeValidation, "input.purchaseAllocationId",
			"allocation must belong to this plant"))
	}
	return alloc, nil
}

func ptrProblem(p Problem) *Problem { return &p }
