package agentapi

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentapi/catalog"
	"canopy/server/internal/agentcontext"
	platformmedia "canopy/server/internal/platform/media"
	gqlx "canopy/server/internal/transport/graphql"
	httpx "canopy/server/internal/transport/http"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type Result struct {
	Status      string  `json:"status"`
	OperationID string  `json:"operation_id,omitempty"`
	Data        any     `json:"data,omitempty"`
	Problems    []Issue `json:"problems"`
	AsOf        string  `json:"as_of"`
	Image       []byte  `json:"-"`
	MIME        string  `json:"-"`
}
type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Definition struct {
	Tool     catalog.Tool
	Schema   map[string]any
	resolved *jsonschema.Resolved
	document string
}
type API struct {
	Access      *agentaccess.Manager
	Engine      *gqlx.AgentExecutor
	Definitions []Definition
	Store       platformmedia.Store
	UploadBase  string
}

func New(access *agentaccess.Manager, engine *gqlx.AgentExecutor, store platformmedia.Store) (*API, error) {
	a := &API{Access: access, Engine: engine, Store: store}
	for _, t := range catalog.Tools() {
		d := Definition{Tool: t, Schema: inputSchema(engine.Schema(), t)}
		raw, _ := json.Marshal(d.Schema)
		var s jsonschema.Schema
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		var err error
		d.resolved, err = s.Resolve(nil)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(t.Root, "@") {
			d.document, err = document(engine.Schema(), t)
			if err != nil {
				return nil, err
			}
		}
		a.Definitions = append(a.Definitions, d)
	}
	access.Apply = a.Apply
	return a, nil
}
func Allowed(p agentaccess.Principal, t catalog.Tool) bool {
	if t.Root == "@operation" || t.Root == "@proposals" || t.Root == "@proposal" {
		return true
	}
	if !p.Grant.Has(t.Scope) {
		return false
	}
	if len(p.Grant.PlantIDs) > 0 {
		switch t.Root {
		case "purchases", "purchase", "spending", "@receipt", "requestExport", "exportJob", "notifications", "markNotificationRead", "createPlant", "createLocation", "updateLocation", "createTag", "updateTag", "archiveTag", "sources", "careProfiles", "careProfile", "createSource", "updateSource", "retireSource", "createCareProfileDraft", "updateCareProfileDraft", "deleteCareProfileDraft", "publishCareProfile", "retireCareProfileVersion":
			return false
		}
	}
	if t.Root == "attachReceipt" || t.Root == "detachReceipt" {
		return p.Grant.Has("purchases:write") && len(p.Grant.PlantIDs) == 0
	}
	return true
}
func (a *API) definition(name string) (Definition, bool) {
	for _, d := range a.Definitions {
		if d.Tool.Name == name {
			return d, true
		}
	}
	return Definition{}, false
}
func failure(code, msg, op string) Result {
	return Result{Status: "failed", OperationID: op, Problems: []Issue{{code, msg}}, AsOf: time.Now().UTC().Format(time.RFC3339)}
}
func str(v any) string { s, _ := v.(string); return s }
func (a *API) Call(ctx context.Context, p agentaccess.Principal, name string, args map[string]any) Result {
	return a.call(ctx, p, name, args, "")
}
func (a *API) Apply(ctx context.Context, p agentaccess.Principal, action agentaccess.Action) (any, error) {
	var args map[string]any
	if json.Unmarshal(action.Input, &args) != nil {
		return nil, errors.New("Invalid proposal input")
	}
	r := a.call(ctx, p, action.Tool, args, action.ID)
	if len(r.Problems) > 0 {
		return nil, errors.New(r.Problems[0].Message)
	}
	return r, nil
}
func (a *API) call(ctx context.Context, p agentaccess.Principal, name string, args map[string]any, approved string) Result {
	d, ok := a.definition(name)
	op := str(args["operation_id"])
	if !ok {
		return failure("NOT_FOUND", "Unknown tool.", op)
	}
	var err error
	p, err = a.Access.Current(ctx, p)
	if err != nil || !Allowed(p, d.Tool) {
		return failure("FORBIDDEN", "This connection cannot use the requested tool.", op)
	}
	if err = d.resolved.Validate(args); err != nil {
		return failure("VALIDATION", "Arguments do not match this tool's schema. Use its documented fields and required values.", op)
	}
	if err = a.authorize(ctx, p, d.Tool, args); err != nil {
		return failure("NOT_FOUND", "The requested record is unavailable to this connection.", op)
	}
	input := canonical(args)
	// Replay public committed responses before charging the mutation limit again.
	if d.Tool.Mode != "read" {
		var hash, tool string
		var cached []byte
		err = a.Access.Pool.QueryRow(ctx, `SELECT input_hash,tool,response FROM agent_operations WHERE grant_id=$1 AND operation_id=$2`, p.Grant.ID, op).Scan(&hash, &tool, &cached)
		if err == nil {
			if hash != agentcontext.Hash(input) || tool != name {
				return failure("IDEMPOTENCY_CONFLICT", "This operation identity was already used for different input.", op)
			}
			if len(cached) > 0 {
				var replay Result
				if json.Unmarshal(cached, &replay) == nil {
					return replay
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return failure("UNAVAILABLE", "Operation status could not be checked.", op)
		}
	}
	if !a.Access.Limit(p, d.Tool.Mode != "read") {
		return failure("RATE_LIMITED", "This connection has reached its call limit. Retry later with the same operation identity.", op)
	}
	if d.Tool.Mode == "review" && approved == "" {
		action, e := a.Access.Propose(ctx, p, name, op, input, a.before(ctx, p, d.Tool, args))
		if e != nil {
			return failure("CONFLICT", "Proposal could not be created. Check the operation identity and reload.", op)
		}
		status := "proposed"
		if action.State == "APPLIED" {
			status = "applied"
		} else if action.State != "PENDING" || !action.ExpiresAt.After(time.Now()) {
			return failure("CONFLICT", "This proposal is no longer pending. Create a new operation after reviewing the record.", op)
		}
		return Result{Status: status, OperationID: op, Data: action, Problems: []Issue{}, AsOf: time.Now().UTC().Format(time.RFC3339)}
	}
	if d.Tool.Mode != "read" {
		ctx = agentcontext.With(ctx, agentcontext.Origin{GrantID: p.Grant.ID, CredentialID: p.CredentialID, WorkspaceID: p.Grant.WorkspaceID, Revision: p.Grant.Revision, RequestID: uuid.NewString(), Tool: name, OperationID: op, Scope: d.Tool.Scope, ActionID: approved, Approved: approved != "", Input: input})
	}
	var value any
	var image []byte
	var mime string
	if strings.HasPrefix(d.Tool.Root, "@") {
		value, image, mime, err = a.custom(ctx, p, d.Tool, args)
	} else {
		value, err = a.execute(ctx, p, d, args)
	}
	if err != nil {
		return failure("UNAVAILABLE", err.Error(), op)
	}
	if value == nil {
		return failure("NOT_FOUND", "The requested record is unavailable to this connection.", op)
	}
	if m, yes := value.(map[string]any); yes {
		if problems, yes := m["problems"].([]any); yes && len(problems) > 0 {
			return failure("CONFLICT", "The application rejected this change: "+problemText(problems), op)
		}
	}
	if d.Tool.Root == "reserveUpload" {
		if data, ok := value.(map[string]any); ok {
			if asset, ok := data["asset"].(map[string]any); ok {
				data["upload_path"] = "/agent/uploads/" + str(asset["id"])
				data["upload_method"] = "PUT"
				data["upload_auth"] = "Use this connection bearer credential in the HTTP Authorization header; never include it in tool arguments."
			}
		}
	}
	if d.Tool.Root == "requestExport" || d.Tool.Root == "exportJob" {
		addExportPath(value)
	}
	value, err = a.filterAnalysis(ctx, p, value)
	if err != nil {
		return failure("UNAVAILABLE", "Related analysis permissions could not be checked.", op)
	}
	value = scopeOutput(value, p.Grant, str(args["plant_id"]))
	result := Result{Status: "ok", OperationID: op, Data: publicOutput(value), Problems: []Issue{}, AsOf: time.Now().UTC().Format(time.RFC3339), Image: image, MIME: mime}
	if d.Tool.Mode != "read" {
		result.Status = "applied"
		if d.Tool.Root == "requestAnalysis" || d.Tool.Root == "refreshGardenKnowledge" || d.Tool.Root == "finalizeUpload" {
			result.Status = "queued"
		}
	}
	raw, _ := json.Marshal(result)
	if len(raw) > 128<<10 {
		return failure("RESULT_TOO_LARGE", "Use a narrower date/plant filter or a smaller page size. No result was truncated.", op)
	}
	if d.Tool.Mode != "read" {
		_, _ = a.Access.Pool.Exec(ctx, `UPDATE agent_operations SET response=$3 WHERE grant_id=$1 AND operation_id=$2 AND state='APPLIED'`, p.Grant.ID, op, raw)
	}
	_ = a.Access.Q.TouchAgentGrant(ctx, dbgen.TouchAgentGrantParams{WorkspaceID: agentaccess.ID(p.Grant.WorkspaceID), ID: agentaccess.ID(p.Grant.ID), LastTool: pgText(name)})
	return result
}
func problemText(problems []any) string {
	out := []string{}
	for _, p := range problems {
		if v, ok := p.(map[string]any); ok {
			out = append(out, str(v["message"]))
		}
	}
	return strings.Join(out, " ")
}
func (a *API) execute(ctx context.Context, p agentaccess.Principal, d Definition, args map[string]any) (any, error) {
	vars := map[string]any{}
	field := rootField(a.Engine.Schema(), d.Tool)
	for _, arg := range field.Arguments {
		if v, ok := args[snake(arg.Name)]; ok {
			vars[arg.Name] = toDomain(a.Engine.Schema(), arg.Type, v)
		} else if arg.DefaultValue != nil {
			value, err := arg.DefaultValue.Value(nil)
			if err != nil {
				return nil, err
			}
			vars[arg.Name] = value
		}
	}
	if field.Arguments.ForName("first") != nil && vars["first"] == nil {
		vars["first"] = 25
	}
	if d.Tool.Mode != "read" {
		vars["clientMutationId"] = "mcp:" + p.Grant.ID + ":" + agentcontext.Hash([]byte(str(args["operation_id"])))
	}
	input, _ := vars["input"].(map[string]any)
	if d.Tool.Name == "log_watered" {
		input["kind"] = "WATER"
	}
	if d.Tool.Name == "receipt_extract_request" {
		if input == nil {
			return nil, errors.New("Receipt extraction input is required.")
		}
		input["kind"] = "RECEIPT_EXTRACTION"
	}
	if d.Tool.Name == "photo_attach" {
		if input["viewpoint"] == nil {
			input["viewpoint"] = "WHOLE"
		}
		if input["takenAt"] != nil {
			input["dateSource"] = "USER"
		}
	}
	if d.Tool.Name == "photo_attach" && input["takenAt"] == nil {
		input["preferCaptureDate"] = true
		input["dateSource"] = "UPLOAD_FALLBACK"
	}
	if d.Tool.Root == "journal" {
		filter, _ := vars["filter"].(map[string]any)
		if filter == nil {
			filter = map[string]any{}
		}
		if !p.Grant.Has("purchases:read") {
			if types, yes := filter["entryTypes"].([]any); yes {
				for _, t := range types {
					if t == "PURCHASE" {
						return nil, errors.New("Purchase history requires purchases:read.")
					}
				}
			} else {
				filter["entryTypes"] = []string{"ACTIVITY", "PHOTO", "MEASUREMENT", "CARE", "INSIGHT"}
			}
		}
		vars["filter"] = filter
	}
	principal := httpx.Principal{WorkspaceID: agentaccess.ID(p.Grant.WorkspaceID), OwnerID: agentaccess.ID(p.OwnerID)}
	out, err := a.Engine.Execute(ctx, principal, d.document, vars)
	if err != nil {
		return nil, err
	}
	return out[d.Tool.Root], nil
}
func scopeOutput(value any, g agentaccess.Grant, plant string) any {
	switch v := value.(type) {
	case []any:
		out := []any{}
		for _, x := range v {
			if y := scopeOutput(x, g, plant); y != nil {
				out = append(out, y)
			}
		}
		return out
	case map[string]any:
		if id := str(v["plantId"]); id != "" && (!g.Plant(id) || (plant != "" && id != plant)) {
			return nil
		}
		if v["__typename"] == "Plant" {
			if !g.Plant(str(v["id"])) || (plant != "" && str(v["id"]) != plant) {
				return nil
			}
		}
		if (v["__typename"] == "CalendarEvent" && v["kind"] == "PURCHASE" || v["__typename"] == "PurchaseEntry") && !g.Has("purchases:read") {
			return nil
		}
		if (v["__typename"] == "PhotoEntry" || v["__typename"] == "PlantPhoto" || v["__typename"] == "CalendarEvent" && v["kind"] == "PHOTO") && !g.Has("photos:read") {
			return nil
		}
		out := map[string]any{}
		for k, x := range v {
			out[k] = scopeOutput(x, g, plant)
		}
		return out
	default:
		return value
	}
}

func addExportPath(value any) {
	m, ok := value.(map[string]any)
	if !ok {
		return
	}
	if job, ok := m["exportJob"].(map[string]any); ok {
		addExportPath(job)
		return
	}
	if id := str(m["id"]); id != "" {
		m["downloadUrl"] = "/agent/exports/" + id + "/download"
	}
}
