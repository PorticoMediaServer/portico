package apikit

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestNamedComponentsReferenceOnlyExactOpenAPIShapes(t *testing.T) {
	type entry struct {
		ID    string `json:"id"`
		Title string `json:"title,omitempty"`
	}
	type page struct {
		Entries []entry          `json:"entries"`
		Maybe   *entry           `json:"maybe"`
		Lookup  map[string]entry `json:"lookup"`
	}
	definitions := []Definition{
		{Metadata: Metadata{Audience: Clients, ID: "entries", Method: "POST", Path: "/v1/entries", Summary: "Entries", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200, BodyLimit: 4096}, Request: reflect.TypeFor[entry](), Response: reflect.TypeFor[page]()},
		{Metadata: Metadata{Audience: Clients, ID: "status", Method: "GET", Path: "/v1/status", Summary: "Status", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 201, SuccessStatuses: []int{202}}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[statusResponse]()},
	}
	wire, err := Schema(reflect.TypeFor[entry]())
	if err != nil {
		t.Fatal(err)
	}
	wire["properties"].(map[string]any)["id"].(map[string]any)["maxLength"] = 20
	options := Options{WireSchemas: map[reflect.Type]map[string]any{reflect.TypeFor[entry](): wire}}
	plain, err := GenerateWith(definitions, options)
	if err != nil {
		t.Fatal(err)
	}
	options.Components = map[string]reflect.Type{"Entry": reflect.TypeFor[entry](), "EntryAlias": reflect.TypeFor[entry](), "Status": reflect.TypeFor[statusResponse]()}
	with, err := GenerateWith(definitions, options)
	if err != nil {
		t.Fatal(err)
	}
	a, b := plain, with
	a.OpenAPI, b.OpenAPI = nil, nil
	if !reflect.DeepEqual(a, b) {
		t.Fatal("naming OpenAPI components changed SDK artifacts")
	}
	if !bytes.Contains(with.OpenAPI, []byte(`"$ref": "#/components/schemas/Entry"`)) {
		t.Fatal("named entry is still inlined")
	}
	var baseline, doc map[string]any
	if err = json.Unmarshal(plain.OpenAPI, &baseline); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(with.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	components := doc["components"].(map[string]any)["schemas"].(map[string]any)
	var expand func(any, map[string]bool) any
	expand = func(value any, stack map[string]bool) any {
		switch v := value.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				const prefix = "#/components/schemas/"
				name := ref[len(prefix):]
				if stack[name] {
					t.Fatalf("component reference cycle: %s", ref)
				}
				next := map[string]bool{}
				for key, val := range stack {
					next[key] = val
				}
				next[name] = true
				return expand(components[name], next)
			}
			out := map[string]any{}
			for key, child := range v {
				out[key] = expand(child, stack)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, child := range v {
				out[i] = expand(child, stack)
			}
			return out
		}
		return value
	}
	if !reflect.DeepEqual(expand(doc["paths"], nil), baseline["paths"]) {
		t.Fatal("references changed request, nullable, collection or status-specific response shapes")
	}
	for name, shape := range baseline["components"].(map[string]any)["schemas"].(map[string]any) {
		if !reflect.DeepEqual(expand(components[name], nil), shape) {
			t.Fatalf("component %s changed shape", name)
		}
	}
	// A constrained use is not interchangeable with its unconstrained component.
	constrained := map[string]any{"type": "string", "maxLength": float64(10)}
	input := map[string]any{"components": map[string]any{"schemas": map[string]any{"Label": map[string]any{"type": "string"}, "Use": constrained}}, "paths": map[string]any{}}
	if err = referenceOpenAPIComponents(input, map[string]reflect.Type{"Label": reflect.TypeFor[string]()}); err != nil {
		t.Fatal(err)
	}
	if constrained["maxLength"] != float64(10) || constrained["$ref"] != nil {
		t.Fatal("per-use bounds were erased")
	}
}
