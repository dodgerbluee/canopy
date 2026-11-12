package agentapi

import (
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentapi/catalog"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"strings"
)

func pgText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
func (a *API) AssetAllowed(ctx context.Context, p agentaccess.Principal, id string) bool {
	if !agentaccess.ID(id).Valid {
		return false
	}
	var yes bool
	err := a.Access.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_operations WHERE grant_id=$1 AND workspace_id=$2 AND state='APPLIED' AND (resource->>'id'=$3 OR resource->>'ID'=$3))`, p.Grant.ID, p.Grant.WorkspaceID, id).Scan(&yes)
	return err == nil && yes
}
func (a *API) authorize(ctx context.Context, p agentaccess.Principal, t catalog.Tool, args map[string]any) error {
	input, _ := args["input"].(map[string]any)
	if input == nil {
		input = args
	}
	plant := str(input["plant_id"])
	if plant == "" {
		plant = str(args["plant_id"])
	}
	if t.Root == "plant" || strings.HasSuffix(t.Root, "Plant") {
		if id := str(input["id"]); id != "" {
			plant = id
		}
	}
	if plant != "" && !p.Grant.Plant(plant) {
		return agentaccess.ErrDenied
	}
	if t.Root == "exportJob" {
		var allowed bool
		err := a.Access.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM export_jobs WHERE id=$1 AND workspace_id=$2 AND agent_grant_id=$3 AND agent_credential_id=$4)`, str(args["id"]), p.Grant.WorkspaceID, p.Grant.ID, p.CredentialID).Scan(&allowed)
		if err != nil || !allowed {
			return agentaccess.ErrDenied
		}
	}
	asset := str(input["asset_id"])
	if t.Root == "@upload" {
		asset = str(args["asset_id"])
	}
	if asset != "" && !a.AssetAllowed(ctx, p, asset) {
		return agentaccess.ErrDenied
	}
	if t.Root == "reserveUpload" && input["purpose"] == "RECEIPT" && (!p.Grant.Has("purchases:write") || len(p.Grant.PlantIDs) > 0) {
		return agentaccess.ErrDenied
	}
	if t.Root == "createPlant" && input["initial_purchase"] != nil && !p.Grant.Has("purchases:write") {
		return agentaccess.ErrDenied
	}
	if t.Root == "requestAnalysis" {
		kind := str(input["kind"])
		if t.Name == "receipt_extract_request" {
			kind = "RECEIPT_EXTRACTION"
		}
		if !analysisKindAllowed(p.Grant, kind) {
			return agentaccess.ErrDenied
		}
		if kind == "RECEIPT_EXTRACTION" {
			if !p.Grant.Has("receipts:read") || !p.Grant.Has("purchases:read") || len(p.Grant.PlantIDs) > 0 {
				return agentaccess.ErrDenied
			}
		} else if (kind == "VISION" || kind == "IDENTIFICATION") && !p.Grant.Has("photos:read") {
			return agentaccess.ErrDenied
		}
	}
	id := str(input["id"])
	for _, key := range []string{"run_id", "suggestion_id", "occurrence_id", "plan_id", "photo_id"} {
		if id == "" {
			id = str(input[key])
		}
	}
	table := ""
	switch t.Root {
	case "@photo":
		id = str(args["photo_id"])
		table = "plant_photos"
	case "updatePhoto", "deletePhoto":
		table = "plant_photos"
	case "analysisRun", "cancelAnalysis":
		table = "analysis_runs"
	case "updateActivity", "voidActivity":
		table = "activities"
	case "updateMeasurement", "deleteMeasurement":
		table = "measurements"
	case "completeOccurrence", "skipOccurrence", "snoozeOccurrence", "correctOccurrenceCompletion":
		table = "care_occurrences"
	case "updateCarePlan":
		table = "care_plans"
	case "acceptSuggestion", "dismissSuggestion":
		table = "suggestions"
	}
	if table != "" {
		if !agentaccess.ID(id).Valid {
			return agentaccess.ErrDenied
		}
		var target *string
		err := a.Access.Pool.QueryRow(ctx, "SELECT plant_id::text FROM "+table+" WHERE id=$1 AND workspace_id=$2", id, p.Grant.WorkspaceID).Scan(&target)
		if err != nil {
			return agentaccess.ErrDenied
		}
		if table == "analysis_runs" {
			var kind string
			if err := a.Access.Pool.QueryRow(ctx, `SELECT kind::text FROM analysis_runs WHERE id=$1 AND workspace_id=$2`, id, p.Grant.WorkspaceID).Scan(&kind); err != nil || !analysisKindAllowed(p.Grant, kind) {
				return agentaccess.ErrDenied
			}
		}
		if table == "suggestions" {
			var kind *string
			if err := a.Access.Pool.QueryRow(ctx, `SELECT r.kind::text FROM suggestions s LEFT JOIN analysis_runs r ON r.id=s.run_id AND r.workspace_id=s.workspace_id WHERE s.id=$1 AND s.workspace_id=$2`, id, p.Grant.WorkspaceID).Scan(&kind); err != nil || (kind != nil && !analysisKindAllowed(p.Grant, *kind)) {
				return agentaccess.ErrDenied
			}
		}
		if target != nil && !p.Grant.Plant(*target) {
			return agentaccess.ErrDenied
		}
		if target == nil && (len(p.Grant.PlantIDs) > 0 || !p.Grant.Has("receipts:read")) {
			return agentaccess.ErrDenied
		}
	}
	if len(p.Grant.PlantIDs) > 0 && t.Mode != "read" && plant == "" && table == "" {
		switch t.Root {
		case "reserveUpload", "finalizeUpload", "recordRain", "voidRain":
		default:
			return errors.New("restricted action needs an authorized plant")
		}
	}
	return nil
}

// before is an owner-review snapshot of a finite set of domain records. It
// never reads credential/configuration/asset-storage tables.
func (a *API) before(ctx context.Context, p agentaccess.Principal, t catalog.Tool, args map[string]any) []byte {
	input, _ := args["input"].(map[string]any)
	id := str(input["id"])
	if id == "" {
		id = str(input["suggestion_id"])
	}
	if id == "" {
		id = str(input["occurrence_id"])
	}
	table := ""
	switch t.Root {
	case "updatePlant", "archivePlant", "restorePlant":
		table = "plants"
	case "updatePhoto", "deletePhoto":
		table = "plant_photos"
	case "updateActivity", "voidActivity":
		table = "activities"
	case "updateMeasurement", "deleteMeasurement":
		table = "measurements"
	case "updatePurchase", "voidPurchase":
		table = "purchases"
	case "updateCarePlan":
		table = "care_plans"
	case "completeOccurrence", "skipOccurrence", "snoozeOccurrence", "correctOccurrenceCompletion":
		table = "care_occurrences"
	case "acceptSuggestion", "dismissSuggestion":
		table = "suggestions"
	}
	if table == "" || !agentaccess.ID(id).Valid {
		return []byte(`{}`)
	}
	var body []byte
	if err := a.Access.Pool.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+table+" t WHERE id=$1 AND workspace_id=$2", id, p.Grant.WorkspaceID).Scan(&body); err != nil {
		return []byte(`{}`)
	}
	return body
}

func (a *API) AgentManager() *agentaccess.Manager { return a.Access }

func analysisKindAllowed(g agentaccess.Grant, kind string) bool {
	switch kind {
	case "RECEIPT_EXTRACTION":
		return g.Has("receipts:read") && g.Has("purchases:read") && len(g.PlantIDs) == 0
	case "SYNTHESIS":
		return g.Has("garden:read") && g.Has("purchases:read") && len(g.PlantIDs) == 0
	case "VISION", "IDENTIFICATION":
		return g.Has("garden:read") && g.Has("photos:read")
	default:
		return g.Has("garden:read")
	}
}
