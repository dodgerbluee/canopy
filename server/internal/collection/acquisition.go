package collection

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/money"
	"canopy/server/internal/purchases"
)

// collectionProblem re-exports a purchases problem in this package's
// Problem type (same frozen code vocabulary, different struct).
func collectionProblem(p purchases.Problem) Problem {
	return Problem{Code: p.Code, Field: p.Field, Message: p.Message, CurrentRevision: p.CurrentRevision}
}

// AcquisitionResult identifies the affected acquisition aggregate.
type AcquisitionResult struct {
	ID         string `json:"id"`
	PlantID    string `json:"plantId"`
	Revision   int32  `json:"revision"`
	PurchaseID string `json:"purchaseId,omitempty"` // set when a purchase was atomically created
}

// UpdateAcquisitionInput mirrors UpdateAcquisitionInput.
type UpdateAcquisitionInput struct {
	ID               string
	ExpectedRevision int32
	Seller           *string
	AcquiredOn       *time.Time
}

// AcquisitionPricing carries the KNOWN-state inputs for
// recordAcquisition: exactly one of an existing allocation id or an
// atomic new purchase.
type AcquisitionPricing struct {
	AllocationID *string
	NewPurchase  *purchases.NewPricedSpec
}

// RecordAcquisition records provenance for a plant. UNKNOWN stays
// unpriced; KNOWN requires exactly one pricing source and links the
// acquisition atomically (allocation validated, or purchase + line +
// allocation created in the same transaction).
func (s *Service) RecordAcquisition(ctx context.Context, a Actor, in AcquisitionInput, plantID string, pricing *AcquisitionPricing, cmid *string) (AcquisitionResult, []Problem, error) {
	wire := map[string]any{"plantId": plantID, "seller": in.Seller, "pricingState": in.PricingState}
	if pricing != nil {
		wire["allocationId"] = pricing.AllocationID
		wire["newPurchase"] = pricing.NewPurchase != nil
	}
	if in.AcquiredOn != nil {
		wire["acquiredOn"] = in.AcquiredOn.UTC().Format("2006-01-02")
	}
	return mutate[AcquisitionResult](s, ctx, a, "recordAcquisition", cmid, wire,
		func(ctx context.Context, q *dbgen.Queries) (AcquisitionResult, []Problem) {
			pid, ok := parseUUID(plantID)
			if !ok {
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "input.plantId", "malformed id")}
			}
			plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: pid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return AcquisitionResult{}, notFound("input.plantId", "plant not found")
			}
			if _, err := q.GetAcquisitionByPlant(ctx, dbgen.GetAcquisitionByPlantParams{
				PlantID: pid, WorkspaceID: a.WorkspaceID,
			}); err == nil {
				return AcquisitionResult{}, []Problem{problem(CodeConflict, "input.plantId",
					"acquisition already recorded for this plant; use updateAcquisition")}
			} else if !notFoundErr(err) {
				s.Log.Error("acquisition lookup", "err", err)
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "", "acquisition lookup failed")}
			}
			// Pricing transitions (03): UNKNOWN needs no source; KNOWN
			// requires exactly one (existing allocation or new purchase).
			hasSource := pricing != nil && (pricing.AllocationID != nil || pricing.NewPurchase != nil)
			if in.PricingState != "UNKNOWN" && in.PricingState != "KNOWN" {
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "input.pricingState",
					"pricingState must be UNKNOWN or KNOWN")}
			}
			if in.PricingState == "UNKNOWN" && hasSource {
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "input.pricingState",
					"an UNKNOWN acquisition cannot carry a price source")}
			}
			if in.PricingState == "KNOWN" {
				if !hasSource || (pricing.AllocationID != nil && pricing.NewPurchase != nil) {
					return AcquisitionResult{}, []Problem{problem(CodeValidation, "input",
						"KNOWN pricing requires exactly one of purchaseAllocationId or newPurchase")}
				}
			}
			acquiredOn := pgtype.Date{}
			if in.AcquiredOn != nil {
				acquiredOn = pgtype.Date{Time: *in.AcquiredOn, Valid: true}
			}
			state := dbgen.PricingStateUNKNOWN
			var allocID pgtype.UUID
			var newPurchID string
			if in.PricingState == "KNOWN" {
				if pricing.AllocationID != nil {
					alloc, p := purchases.ResolveAllocationForPlant(ctx, q, a.WorkspaceID, *pricing.AllocationID, pid)
					if p != nil {
						return AcquisitionResult{}, []Problem{collectionProblem(*p)}
					}
					allocID = alloc.ID
				} else {
					np := pricing.NewPurchase
					if !money.ValidCurrency(np.Currency) {
						return AcquisitionResult{}, []Problem{problem(CodeValidation, "input.newPurchase.currency",
							"currency must be an ISO-4217 three-letter code")}
					}
					if np.Tax+np.Shipping > np.Total {
						return AcquisitionResult{}, []Problem{problem(CodeValidation, "input.newPurchase.taxMinor",
							"tax + shipping exceeds total")}
					}
					prow, _, alloc, err := purchases.CreateAtomic(ctx, q, a.WorkspaceID, purchases.AtomicSpec{
						Currency: np.Currency, Total: np.Total, Tax: np.Tax, Shipping: np.Shipping,
						Seller: np.Seller, PurchasedOn: np.PurchasedOn, LineDesc: plant.Name, PlantID: pid,
					})
					if err != nil {
						s.Log.Error("recordAcquisition newPurchase", "err", err)
						return AcquisitionResult{}, []Problem{problem(CodeValidation, "input.newPurchase",
							"purchase could not be stored")}
					}
					allocID = alloc.ID
					newPurchID = uuidString(prow.ID)
					if err := s.audit(ctx, q, a, "recordAcquisition.newPurchase", "purchase", prow.ID, prow.Revision, nil); err != nil {
						return AcquisitionResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
					}
				}
				state = dbgen.PricingStateKNOWN
			}
			row, err := q.InsertAcquisition(ctx, dbgen.InsertAcquisitionParams{
				ID:                          s.uuid.New(),
				WorkspaceID:                 a.WorkspaceID,
				PlantID:                     pid,
				Seller:                      textPtr(in.Seller),
				AcquiredOn:                  acquiredOn,
				PricingState:                state,
				InitialPurchaseAllocationID: allocID,
			})
			if err != nil {
				if isUniqueViolation(err) {
					return AcquisitionResult{}, []Problem{problem(CodeConflict, "input.plantId",
						"acquisition already recorded for this plant; use updateAcquisition")}
				}
				s.Log.Error("recordAcquisition insert", "err", err)
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "input", "acquisition could not be stored")}
			}
			// Projection: plants.acquired_on mirrors the acquisition, never
			// an independently editable plant field.
			if acquiredOn.Valid {
				if err := q.SetPlantAcquiredOnProjection(ctx, dbgen.SetPlantAcquiredOnProjectionParams{
					ID: pid, AcquiredOn: acquiredOn, WorkspaceID: a.WorkspaceID,
				}); err != nil {
					s.Log.Error("acquired_on projection", "err", err)
					return AcquisitionResult{}, []Problem{problem(CodeValidation, "", "projection failed")}
				}
			}
			if err := s.audit(ctx, q, a, "recordAcquisition", "acquisition", row.ID, row.Revision, nil); err != nil {
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return AcquisitionResult{ID: uuidString(row.ID), PlantID: uuidString(pid), Revision: row.Revision,
				PurchaseID: newPurchID}, nil
		})
}

// UpdateAcquisition edits provenance under a revision guard and keeps the
// plant projection in step.
func (s *Service) UpdateAcquisition(ctx context.Context, a Actor, in UpdateAcquisitionInput, cmid *string) (AcquisitionResult, []Problem, error) {
	return mutate[AcquisitionResult](s, ctx, a, "updateAcquisition", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (AcquisitionResult, []Problem) {
			aid, ok := parseUUID(in.ID)
			if !ok {
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}
			}
			cur, err := q.GetAcquisition(ctx, dbgen.GetAcquisitionParams{ID: aid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				return AcquisitionResult{}, notFound("input.id", "acquisition not found")
			}
			acquiredOn := cur.AcquiredOn
			if in.AcquiredOn != nil {
				acquiredOn = pgtype.Date{Time: *in.AcquiredOn, Valid: true}
			}
			updated, err := q.UpdateAcquisitionProvenance(ctx, dbgen.UpdateAcquisitionProvenanceParams{
				Seller: textPtr(in.Seller), AcquiredOn: acquiredOn,
				ID: aid, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return AcquisitionResult{}, s.conflictProblem(ctx, q, "acquisitions", aid, a.WorkspaceID, err)
			}
			if acquiredOn.Valid {
				if err := q.SetPlantAcquiredOnProjection(ctx, dbgen.SetPlantAcquiredOnProjectionParams{
					ID: cur.PlantID, AcquiredOn: acquiredOn, WorkspaceID: a.WorkspaceID,
				}); err != nil {
					s.Log.Error("acquired_on projection", "err", err)
					return AcquisitionResult{}, []Problem{problem(CodeValidation, "", "projection failed")}
				}
			}
			if err := s.audit(ctx, q, a, "updateAcquisition", "acquisition", aid, updated.Revision, cur); err != nil {
				return AcquisitionResult{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			return AcquisitionResult{ID: uuidString(aid), PlantID: uuidString(cur.PlantID), Revision: updated.Revision}, nil
		})
}

var _ = pgx.ErrNoRows
