package agentapi

import (
	"canopy/server/internal/agentapi/catalog"
	"encoding/json"
	"fmt"
	"github.com/vektah/gqlparser/v2/ast"
	"regexp"
	"strings"
)

var word = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func snake(s string) string { return strings.ToLower(word.ReplaceAllString(s, "${1}_${2}")) }
func schemaFor(s *ast.Schema, t *ast.Type, depth int) map[string]any {
	if !t.NonNull {
		copy := *t
		copy.NonNull = true
		return map[string]any{"anyOf": []any{schemaFor(s, &copy, depth), map[string]any{"type": "null"}}}
	}
	if depth > 12 {
		return map[string]any{"type": "object", "additionalProperties": false}
	}
	if t.Elem != nil {
		return map[string]any{"type": "array", "items": schemaFor(s, t.Elem, depth+1), "maxItems": 100}
	}
	d := s.Types[t.NamedType]
	if d == nil {
		return map[string]any{"type": "string"}
	}
	if d.Kind == ast.Enum {
		values := []string{}
		for _, v := range d.EnumValues {
			values = append(values, v.Name)
		}
		return map[string]any{"type": "string", "enum": values}
	}
	if d.Kind == ast.InputObject {
		p := map[string]any{}
		required := []string{}
		for _, f := range d.Fields {
			p[snake(f.Name)] = schemaFor(s, f.Type, depth+1)
			if f.Type.NonNull && f.DefaultValue == nil {
				required = append(required, snake(f.Name))
			}
		}
		return map[string]any{"type": "object", "properties": p, "required": required, "additionalProperties": false}
	}
	switch t.NamedType {
	case "Int":
		return map[string]any{"type": "integer"}
	case "Float":
		return map[string]any{"type": "number"}
	case "Boolean":
		return map[string]any{"type": "boolean"}
	default:
		return map[string]any{"type": "string", "maxLength": 20000}
	}
}
func inputSchema(s *ast.Schema, t catalog.Tool) map[string]any {
	properties := map[string]any{}
	required := []string{}
	if strings.HasPrefix(t.Root, "@") {
		switch t.Root {
		case "@photo":
			properties["photo_id"] = map[string]any{"type": "string"}
			properties["include_image"] = map[string]any{"type": "boolean"}
			required = append(required, "photo_id")
		case "@receipt":
			properties["receipt_id"] = map[string]any{"type": "string"}
			required = append(required, "receipt_id")
		case "@upload":
			properties["asset_id"] = map[string]any{"type": "string"}
			required = append(required, "asset_id")
		case "@photos":
			properties["plant_id"] = map[string]any{"type": "string"}
			properties["first"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
			properties["after"] = map[string]any{"type": "string"}
			required = append(required, "plant_id")
		case "@operation":
			properties["operation_id"] = map[string]any{"type": "string"}
			required = append(required, "operation_id")
		case "@proposal":
			properties["id"] = map[string]any{"type": "string"}
			required = append(required, "id")
		}
	} else {
		field := rootField(s, t)
		for _, a := range field.Arguments {
			if a.Name == "clientMutationId" {
				continue
			}
			prop := schemaFor(s, a.Type, 0)
			if a.Name == "first" {
				prop["minimum"] = 1
				prop["maximum"] = 100
			}
			properties[snake(a.Name)] = prop
			if a.Type.NonNull && a.DefaultValue == nil {
				required = append(required, snake(a.Name))
			}
		}
	}
	if t.Name == "photo_attach" {
		input := properties["input"].(map[string]any)
		required := []string{}
		for _, key := range input["required"].([]string) {
			if key != "date_source" && key != "viewpoint" {
				required = append(required, key)
			}
		}
		input["required"] = required
	}
	if t.Name == "log_watered" {
		input := properties["input"].(map[string]any)
		delete(input["properties"].(map[string]any), "kind")
		a := []string{}
		for _, k := range input["required"].([]string) {
			if k != "kind" {
				a = append(a, k)
			}
		}
		input["required"] = a
	}
	if t.Root == "gardenKnowledge" || t.Root == "calendarEvents" {
		properties["plant_id"] = map[string]any{"type": "string"}
	}
	if t.Mode != "read" {
		properties["operation_id"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 160}
		required = append(required, "operation_id")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func rootField(s *ast.Schema, t catalog.Tool) *ast.FieldDefinition {
	if t.Root == "previewCarePlan" || t.Mode == "read" {
		return s.Query.Fields.ForName(t.Root)
	}
	return s.Mutation.Fields.ForName(t.Root)
}
func toDomain(s *ast.Schema, t *ast.Type, v any) any {
	if v == nil {
		return nil
	}
	if t.Elem != nil {
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		out := []any{}
		for _, x := range arr {
			out = append(out, toDomain(s, t.Elem, x))
		}
		return out
	}
	d := s.Types[t.NamedType]
	if d == nil || d.Kind != ast.InputObject {
		return v
	}
	object, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for _, f := range d.Fields {
		if value, yes := object[snake(f.Name)]; yes {
			out[f.Name] = toDomain(s, f.Type, value)
		}
	}
	return out
}

var forbidden = map[string]bool{"latitude": true, "longitude": true, "passwordHash": true, "apiKey": true, "credentialCiphertext": true, "storageKey": true, "token": true, "csrfToken": true}

func selection(s *ast.Schema, t *ast.Type, depth int) string {
	d := s.Types[t.Name()]
	if d == nil || d.Kind == ast.Scalar || d.Kind == ast.Enum {
		return ""
	}
	if depth > 4 {
		if d.Fields.ForName("id") != nil {
			return "{id}"
		}
		return "{__typename}"
	}
	if d.Kind == ast.Union || d.Kind == ast.Interface {
		parts := []string{"__typename"}
		for _, m := range s.GetPossibleTypes(d) {
			parts = append(parts, "... on "+m.Name+" "+selection(s, ast.NamedType(m.Name, nil), depth+1))
		}
		return "{" + strings.Join(parts, " ") + "}"
	}
	names := []string{"__typename"}
	for _, f := range d.Fields {
		if strings.HasPrefix(f.Name, "__") || forbidden[f.Name] {
			continue
		}
		if d.Name == "Plant" && (f.Name == "acquisition" || f.Name == "carePlan") {
			continue
		}
		if d.Name == "Workspace" && (f.Name != "id" && f.Name != "displayName" && f.Name != "timezone" && f.Name != "defaultCurrency") {
			continue
		}
		if (d.Name == "Location" && f.Name == "parent") || f.Name == "contexts" {
			continue
		}
		skip := false
		for _, a := range f.Arguments {
			if a.Type.NonNull && a.DefaultValue == nil {
				skip = true
			}
		}
		if skip {
			continue
		}
		name := f.Name
		if f.Name == "kind" && d.Name == "InsightEntry" {
			name = "analysisKind:kind"
		}
		if f.Name == "kind" && d.Name == "CareEntry" {
			name = "ruleKind:kind"
		}
		names = append(names, name+selection(s, f.Type, depth+1))
	}
	if len(names) == 0 {
		names = append(names, "__typename")
	}
	return "{" + strings.Join(names, " ") + "}"
}
func document(s *ast.Schema, t catalog.Tool) (string, error) {
	f := rootField(s, t)
	if f == nil {
		return "", fmt.Errorf("tool %s has no implemented root %s", t.Name, t.Root)
	}
	variables := []string{}
	args := []string{}
	for _, a := range f.Arguments {
		variables = append(variables, "$"+a.Name+":"+a.Type.String())
		args = append(args, a.Name+":$"+a.Name)
	}
	kind := "query"
	if t.Mode != "read" {
		kind = "mutation"
	}
	decl := ""
	if len(variables) > 0 {
		decl = "(" + strings.Join(variables, ",") + ")"
	}
	arguments := ""
	if len(args) > 0 {
		arguments = "(" + strings.Join(args, ",") + ")"
	}
	return kind + " AgentTool" + decl + " {" + t.Root + arguments + selection(s, f.Type, 0) + "}", nil
}
func publicOutput(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			if !forbidden[k] {
				out[snake(k)] = publicOutput(x)
			}
		}
		return out
	case []any:
		out := []any{}
		for _, x := range v {
			out = append(out, publicOutput(x))
		}
		return out
	default:
		return value
	}
}
func canonical(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
