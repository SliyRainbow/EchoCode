package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type sharedInputSchemaTool struct{ schema map[string]any }

func (sharedInputSchemaTool) Name() string                  { return "shared_schema" }
func (sharedInputSchemaTool) Description() string           { return "test schema export" }
func (t sharedInputSchemaTool) InputSchema() map[string]any { return t.schema }
func (sharedInputSchemaTool) Execute(context.Context, json.RawMessage) (Result, error) {
	return Result{}, nil
}

func TestListDirExportsAnEmptyRequiredArray(t *testing.T) {
	schema := NewRegistry(ListDirTool{}).Schemas()[0].Function.Parameters
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["required"]) != "[]" {
		t.Fatalf("required must be an explicit empty array, got %s", encoded)
	}
	// Empty arguments keep their real meaning: list the workspace root.
	result, err := (ListDirTool{Policy: SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "read-only"}}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || result.IsError {
		t.Fatalf("optional arguments became required: %#v, %v", result, err)
	}
}

func TestExportInputSchemaNormalizesOnlySchemaLocations(t *testing.T) {
	annotation := map[string]any{"type": "object", "required": nil, "properties": nil}
	schema := map[string]any{
		"type": "object", "required": []string{"name"}, "additionalProperties": false,
		"properties": map[string]any{
			"name":     map[string]any{"type": "string"},
			"options":  map[string]any{"type": "object", "required": []string(nil), "properties": map[string]any{"value": map[string]any{"type": []string{"string", "null"}, "default": nil, "enum": []any{nil, "test"}}}},
			"rows":     map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}}},
			"union":    map[string]any{"anyOf": []any{map[string]any{"type": []any{"object", "null"}, "required": nil}, map[string]any{"type": "string"}}},
			"required": map[string]any{"type": "string", "default": "literal property name"},
		},
		"$defs":   map[string]any{"nested": map[string]any{"type": "object", "required": []any(nil)}},
		"default": annotation, "examples": []any{annotation}, "const": nil,
	}
	before, _ := json.Marshal(schema)
	registry := NewRegistry(sharedInputSchemaTool{schema: schema})
	exported := registry.Schemas()[0].Function.Parameters
	after, _ := json.Marshal(schema)
	if string(before) != string(after) {
		t.Fatal("shared input schema was mutated")
	}
	if !reflect.DeepEqual(exported["required"], []string{"name"}) || exported["additionalProperties"] != false {
		t.Fatal("required fields or extra-property policy changed")
	}
	properties := exported["properties"].(map[string]any)
	for _, node := range []map[string]any{
		properties["options"].(map[string]any),
		properties["rows"].(map[string]any)["items"].(map[string]any),
		properties["union"].(map[string]any)["anyOf"].([]any)[0].(map[string]any),
		exported["$defs"].(map[string]any)["nested"].(map[string]any),
	} {
		if !reflect.DeepEqual(node["required"], []string{}) {
			t.Fatalf("optional object required = %#v", node["required"])
		}
	}
	if !reflect.DeepEqual(exported["default"], annotation) || !reflect.DeepEqual(exported["examples"], []any{annotation}) {
		t.Fatal("schema-like literal data was rewritten")
	}
	value := properties["options"].(map[string]any)["properties"].(map[string]any)["value"].(map[string]any)
	if value["default"] != nil || !reflect.DeepEqual(value["enum"], []any{nil, "test"}) || exported["const"] != nil {
		t.Fatal("valid JSON null data was lost")
	}
	first, _ := json.Marshal(exported)
	second, _ := json.Marshal(registry.Schemas()[0].Function.Parameters)
	if string(first) != string(second) {
		t.Fatal("schema export is not deterministic")
	}
	if got := exportInputSchema(map[string]any{"type": "object", "required": "bad"})["required"]; got != "bad" {
		t.Fatal("invalid non-array constraints were silently weakened")
	}
}
