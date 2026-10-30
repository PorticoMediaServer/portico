package apikit

import (
	"encoding/json"
	"reflect"
	"testing"
)

type opaqueValue json.RawMessage

func (opaqueValue) JSONSchema() map[string]any { return map[string]any{} }

type withOpaque struct {
	Type string      `json:"type"`
	Data opaqueValue `json:"data,omitempty"`
}

func TestSchemaProviderDeclaresAnOpaqueValue(t *testing.T) {
	s, err := Schema(reflect.TypeFor[withOpaque]())
	if err != nil {
		t.Fatal(err)
	}
	data := s["properties"].(map[string]any)["data"].(map[string]any)
	if len(data) != 0 {
		t.Fatalf("opaque schema %v", data)
	}
	if got := tsType(s); got != `{ "data"?: unknown; "type": string }` {
		t.Fatal(got)
	}
}

type nullableLabel string

func (nullableLabel) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}}
}

func TestSchemaProviderUnionKeepsItsTypes(t *testing.T) {
	s, err := Schema(reflect.TypeFor[struct {
		Label nullableLabel `json:"label"`
	}]())
	if err != nil {
		t.Fatal(err)
	}
	if got := tsType(s); got != `{ "label": string | null }` {
		t.Fatalf("union type erased: %s", got)
	}
}
