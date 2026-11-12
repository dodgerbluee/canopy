package insights

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// Snapshot bounds (03-contracts: "bounded context: latest 12 relevant
// activities, at most 4 image derivatives, retrieved approved guidance,
// weather freshness"). The bounds are hard: the queries themselves cap
// the rows, so an unbounded workspace can never produce an oversized
// prompt.
const (
	SnapMaxActivities   = 12
	SnapMaxMeasurements = 12
	SnapMaxPhotos       = 4
	SnapMaxGuidance     = 8
	SnapMaxWeather      = 5
	SnapMaxCosts        = 12
	SnapSchema          = "canopy-snap/3" // v3: SYNTHESIS carries typed cost evidence (T33); v2: photo sha256+takenAt (T31)
)

// Snapshot is the frozen input record stored on the run row
// (input_snapshot jsonb). It doubles as the evidence manifest: what was
// actually sent. Struct-field marshal order is deterministic, so the
// stored bytes and the input hash agree byte-for-byte.
type Snapshot struct {
	Schema       string          `json:"schema"`
	Kind         string          `json:"kind"`
	Plant        SnapPlant       `json:"plant"`
	Workspace    SnapWorkspace   `json:"workspace"`
	Activities   []SnapActivity  `json:"activities"`
	Measurements []SnapMeasure   `json:"measurements"`
	Photos       []SnapPhoto     `json:"photos"`
	Guidance     []SnapGuidance  `json:"guidance"`
	Weather      []SnapWeather   `json:"weather"`
	Costs        []SnapCost      `json:"costs"`
	CostTotals   []SnapCostTotal `json:"costTotals"`
	MissingFacts []string        `json:"missingFacts"`
}

type SnapPlant struct {
	ID             string `json:"id"`
	Revision       int32  `json:"revision"`
	Status         string `json:"status"`
	Name           string `json:"name"`
	ScientificName string `json:"scientificName,omitempty"`
}

type SnapWorkspace struct {
	ID             string `json:"id"`
	Revision       int32  `json:"revision"`
	Timezone       string `json:"timezone"`
	HasCoordinates bool   `json:"hasCoordinates"`
}

type SnapActivity struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurredAt"`
	Note       string `json:"note,omitempty"`
	Quantity   string `json:"quantity,omitempty"`
	Unit       string `json:"unit,omitempty"`
}

type SnapMeasure struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	Unit       string `json:"unit"`
	ObservedAt string `json:"observedAt"`
}

type SnapPhoto struct {
	ID      string `json:"id"`
	AssetID string `json:"assetId"`
	// T31 evidence provenance: the exact bytes the provider's evidence
	// refers to, and when the photo was taken (growth ordering).
	Sha256  string `json:"sha256"`
	TakenAt string `json:"takenAt,omitempty"`
}

type SnapGuidance struct {
	SourceVersionID string `json:"sourceVersionId"`
	SourceID        string `json:"sourceId"`
	Version         int32  `json:"version"`
}

type SnapWeather struct {
	ID         string `json:"id"`
	LocationID string `json:"locationId"`
	Provider   string `json:"provider"`
	ValidFrom  string `json:"validFrom"`
	ValidUntil string `json:"validUntil"`
	HighC      string `json:"highC,omitempty"`
	LowC       string `json:"lowC,omitempty"`
	RainMm     string `json:"rainMm,omitempty"`
	Et0Mm      string `json:"et0Mm,omitempty"`
}

// SnapCost is one purchase's exact plant-allocated amount (T33): the
// figures come from the money ledger read path, never from the model, so
// "costs match purchases" holds by construction. PurchasedOn is "" when
// the purchase date is unknown (03: unknown stays unknown).
type SnapCost struct {
	ID          string `json:"id"`
	Currency    string `json:"currency"`
	PlantMinor  string `json:"plantMinor"`
	PurchasedOn string `json:"purchasedOn,omitempty"`
}

// SnapCostTotal is the authoritative per-currency aggregate over the
// same rows (identical arithmetic to PlantLifetimeSpendingByCurrency).
type SnapCostTotal struct {
	Currency      string `json:"currency"`
	TotalMinor    string `json:"totalMinor"`
	PurchaseCount int64  `json:"purchaseCount"`
}

// BuildSnapshot gathers the bounded context for one analysis target.
// Every read is workspace-scoped; the returned JSON is what the provider
// will see, and the evidence IDs are what the validator later requires
// result references to be a subset of. includeCosts attaches the typed
// cost evidence (T33) — only SYNTHESIS runs may see purchase figures;
// every other kind's snapshot stays cost-free so money data never leaks
// to a provider that has no need for it.
func BuildSnapshot(ctx context.Context, q *dbgen.Queries, ws dbgen.Workspace, plant dbgen.Plant,
	includeCosts bool) (*Snapshot, error) {
	snap := &Snapshot{
		Schema: SnapSchema,
		Plant: SnapPlant{
			ID:             uuidString(plant.ID),
			Revision:       plant.Revision,
			Status:         string(plant.Status),
			Name:           plant.Name,
			ScientificName: pgText(plant.ScientificName),
		},
		Workspace: SnapWorkspace{
			ID:             uuidString(ws.ID),
			Revision:       ws.SettingsRevision,
			Timezone:       ws.Timezone,
			HasCoordinates: ws.Latitude != nil && ws.Latitude.Valid && ws.Longitude != nil && ws.Longitude.Valid,
		},
		Activities:   []SnapActivity{},
		Measurements: []SnapMeasure{},
		Photos:       []SnapPhoto{},
		Guidance:     []SnapGuidance{},
		Weather:      []SnapWeather{},
		Costs:        []SnapCost{},
		CostTotals:   []SnapCostTotal{},
		MissingFacts: []string{},
	}

	acts, err := q.SnapshotActivities(ctx, dbgen.SnapshotActivitiesParams{WorkspaceID: ws.ID, PlantID: plant.ID})
	if err != nil {
		return nil, err
	}
	for _, a := range acts {
		snap.Activities = append(snap.Activities, SnapActivity{
			ID: uuidString(a.ID), Kind: string(a.Kind), OccurredAt: tsText(a.OccurredAt),
			Note: pgText(a.Note), Quantity: numText(a.Quantity), Unit: nullUnitText(a.Unit),
		})
	}

	mes, err := q.SnapshotMeasurements(ctx, dbgen.SnapshotMeasurementsParams{WorkspaceID: ws.ID, PlantID: plant.ID})
	if err != nil {
		return nil, err
	}
	for _, m := range mes {
		snap.Measurements = append(snap.Measurements, SnapMeasure{
			ID: uuidString(m.ID), Kind: string(m.QuantityKind), Value: numText(&m.Value),
			Unit: string(m.Unit), ObservedAt: tsText(m.ObservedAt),
		})
	}

	photos, err := q.SnapshotPhotos(ctx, dbgen.SnapshotPhotosParams{WorkspaceID: ws.ID, PlantID: plant.ID})
	if err != nil {
		return nil, err
	}
	for _, p := range photos {
		snap.Photos = append(snap.Photos, SnapPhoto{
			ID: uuidString(p.ID), AssetID: uuidString(p.AssetID),
			Sha256: p.Sha256, TakenAt: tsText(p.TakenAt),
		})
	}

	guid, err := q.SnapshotGuidance(ctx, ws.ID)
	if err != nil {
		return nil, err
	}
	for _, g := range guid {
		snap.Guidance = append(snap.Guidance, SnapGuidance{
			SourceVersionID: uuidString(g.ID), SourceID: uuidString(g.ProfileID), Version: g.Version,
		})
	}

	wx, err := q.SnapshotWeather(ctx, ws.ID)
	if err != nil {
		return nil, err
	}
	for _, w := range wx {
		snap.Weather = append(snap.Weather, SnapWeather{
			ID: uuidString(w.ID), LocationID: uuidString(w.LocationID), Provider: w.Provider,
			ValidFrom: tsText(w.ValidFrom), ValidUntil: tsText(w.ValidUntil),
			HighC: numText(w.HighTempC), LowC: numText(w.LowTempC),
			RainMm: numText(w.RainfallMm), Et0Mm: numText(w.Et0Mm),
		})
	}

	// T33 typed cost evidence (SYNTHESIS only): exact ledger figures, so a
	// model can explain spending per currency without ever inventing a
	// number, and the validator can close purchase references.
	if includeCosts {
		costs, err := q.SnapshotPurchaseEvidence(ctx, dbgen.SnapshotPurchaseEvidenceParams{
			WorkspaceID: ws.ID, PlantID: plant.ID,
		})
		if err != nil {
			return nil, err
		}
		for _, c := range costs {
			snap.Costs = append(snap.Costs, SnapCost{
				ID: uuidString(c.ID), Currency: c.Currency,
				PlantMinor: numericText(c.PlantMinor), PurchasedOn: dateText(c.PurchasedOn),
			})
		}
		totals, err := q.SnapshotCostTotals(ctx, dbgen.SnapshotCostTotalsParams{
			WorkspaceID: ws.ID, PlantID: plant.ID,
		})
		if err != nil {
			return nil, err
		}
		for _, tt := range totals {
			snap.CostTotals = append(snap.CostTotals, SnapCostTotal{
				Currency: tt.Currency, TotalMinor: numericText(tt.TotalMinor),
				PurchaseCount: tt.PurchaseCount,
			})
		}
		if len(snap.CostTotals) == 0 {
			snap.MissingFacts = append(snap.MissingFacts, "no purchases with costs allocated to this plant are recorded")
		}
	}

	// Honest gaps (03: "records missing facts instead of guessing").
	if len(snap.Guidance) == 0 {
		snap.MissingFacts = append(snap.MissingFacts, "no approved care guidance is published for this workspace")
	}
	if len(snap.Weather) == 0 {
		snap.MissingFacts = append(snap.MissingFacts, "no fresh weather snapshot exists for this location")
	}
	if !snap.Workspace.HasCoordinates {
		snap.MissingFacts = append(snap.MissingFacts, "workspace has no location coordinates")
	}
	if len(snap.Measurements) == 0 {
		snap.MissingFacts = append(snap.MissingFacts, "no measurements recorded for this plant")
	}
	if len(snap.Activities) == 0 {
		snap.MissingFacts = append(snap.MissingFacts, "no care activities recorded for this plant")
	}
	return snap, nil
}

// Marshal canonicalizes the snapshot for storage and hashing.
func (s *Snapshot) Marshal() ([]byte, string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ParseSnapshot re-reads a stored snapshot (worker staleness checks,
// evidence reconstruction). Unknown stored shapes fail closed.
func ParseSnapshot(raw []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("insights: snapshot parse: %w", err)
	}
	if s.Schema != SnapSchema {
		return nil, fmt.Errorf("insights: unknown snapshot schema %q", s.Schema)
	}
	return &s, nil
}

// EvidenceIDSet is the closed set of ids the provider was shown; result
// evidence references must be a subset (validator rule).
func (s *Snapshot) EvidenceIDSet() map[string]bool {
	set := map[string]bool{}
	for _, a := range s.Activities {
		set[a.ID] = true
	}
	for _, m := range s.Measurements {
		set[m.ID] = true
	}
	for _, p := range s.Photos {
		set[p.ID] = true
	}
	for _, g := range s.Guidance {
		set[g.SourceVersionID] = true
	}
	for _, w := range s.Weather {
		set[w.ID] = true
	}
	for _, c := range s.Costs {
		set[c.ID] = true
	}
	return set
}

// GuidanceVersionIDSet is the closed set of source versions the provider
// was given; citations may only reference these (03: citations bind to
// the exact version supplied).
func (s *Snapshot) GuidanceVersionIDSet() map[string]bool {
	set := map[string]bool{}
	for _, g := range s.Guidance {
		set[g.SourceVersionID] = true
	}
	return set
}

func pgText(t pgtype.Text) string {
	if t.Valid {
		return t.String
	}
	return ""
}

func numText(n *pgtype.Numeric) string {
	if n == nil || !n.Valid {
		return ""
	}
	v, err := n.Value()
	if err != nil || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// numericText formats a NOT NULL numeric ledger figure exactly (minor
// units are integers; the text keeps that shape so totals compare
// string-for-string against the money read path).
func numericText(n pgtype.Numeric) string {
	if !n.Valid {
		return ""
	}
	v, err := n.Value()
	if err != nil || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func nullUnitText(u dbgen.NullMeasurementUnit) string {
	if u.Valid {
		return string(u.MeasurementUnit)
	}
	return ""
}

func tsText(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
}

func dateText(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}
