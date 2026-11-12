package agentapi

import (
	"canopy/server/internal/agentaccess"
	"context"
)

// Related journal/calendar/suggestion summaries can contain synthesis cost
// evidence. Filter those references as well as the direct analysis-status tool.
func analysisReference(m map[string]any) string {
	switch m["__typename"] {
	case "Suggestion", "InsightEntry":
		return str(m["runId"])
	case "CalendarEvent":
		if m["kind"] == "INSIGHT" {
			return str(m["entityId"])
		}
	case "AnalysisRun":
		return str(m["id"])
	}
	return ""
}
func (a *API) filterAnalysis(ctx context.Context, p agentaccess.Principal, value any) (any, error) {
	ids := map[string]bool{}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if id := analysisReference(x); agentaccess.ID(id).Valid {
				ids[id] = true
			}
			for _, child := range x {
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(value)
	if len(ids) == 0 {
		return value, nil
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	rows, err := a.Access.Pool.Query(ctx, `SELECT id::text,kind::text FROM analysis_runs WHERE workspace_id=$1 AND id=ANY($2::uuid[])`, p.Grant.WorkspaceID, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return nil, err
		}
		allowed[id] = analysisKindAllowed(p.Grant, kind)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var filter func(any) any
	filter = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			if id := analysisReference(x); id != "" && !allowed[id] {
				return nil
			}
			out := map[string]any{}
			for k, child := range x {
				out[k] = filter(child)
			}
			return out
		case []any:
			out := []any{}
			for _, child := range x {
				if kept := filter(child); kept != nil {
					out = append(out, kept)
				}
			}
			return out
		default:
			return v
		}
	}
	return filter(value), nil
}
