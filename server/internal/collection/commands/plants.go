// Package commands holds collection command values without service dependencies.
package commands

import (
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

// ContextInput mirrors PlantContextInput from the contract.
type ContextInput struct {
	EffectiveFrom   *time.Time
	LocationID      *string
	Placement       string // CONTAINER | GROUND
	PotVolumeLiters *pgtype.Numeric
	MediumNote      *string
	LightNote       *string
}

// AcquisitionInput mirrors AcquisitionInput from the contract.
type AcquisitionInput struct {
	Seller       *string
	AcquiredOn   *time.Time
	PricingState string // UNKNOWN | KNOWN
}

// InitialPurchaseInput mirrors InitialPurchaseInput with amounts already
// parsed to minor ints by the transport. When set, createPlant atomically
// creates a single-line purchase fully allocated to the new plant and
// prices the acquisition KNOWN against it.
type InitialPurchaseInput struct {
	Currency    string
	Total       int64
	Tax         int64
	Shipping    int64
	Seller      *string
	PurchasedOn *time.Time
}

// CreatePlantInput mirrors CreatePlantInput from the contract.
type CreatePlantInput struct {
	Name              string
	ScientificName    *string
	Cultivar          *string
	Rootstock         *string
	Type              string
	Count             *int32
	Category          string
	PlantedOn         *time.Time
	EstimatedAgeYears *pgtype.Numeric
	TagIDs            []string
	InitialContext    *ContextInput
	Acquisition       *AcquisitionInput
	InitialPurchase   *InitialPurchaseInput
}
