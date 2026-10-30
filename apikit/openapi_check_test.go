package apikit

import (
	"encoding/json"
	"strings"
	"testing"
)

func checkedDocument(t *testing.T, mutate func(document map[string]any)) error {
	t.Helper()
	document := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Test", "version": "1"},
		"components": map[string]any{"schemas": map[string]any{
			"Thing": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "pattern": `^A(?!x)`}}, "required": []any{"id"}, "additionalProperties": false},
		}},
		"paths": map[string]any{"/v1/things/{id}": map[string]any{"get": map[string]any{
			"operationId": "get_thing",
			"parameters": []any{
				map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
				map[string]any{"name": "limit", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1, "default": 12, "x-clamp-maximum": 60}},
			},
			"responses": map[string]any{"200": map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Thing"}}}}},
		}}},
	}
	if mutate != nil {
		mutate(document)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return CheckOpenAPI(raw)
}

func TestCheckOpenAPIAcceptsAValidDocument(t *testing.T) {
	if err := checkedDocument(t, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckOpenAPIRefusesInvalidDocuments(t *testing.T) {
	operation := func(d map[string]any) map[string]any {
		return d["paths"].(map[string]any)["/v1/things/{id}"].(map[string]any)["get"].(map[string]any)
	}
	thing := func(d map[string]any) map[string]any {
		return d["components"].(map[string]any)["schemas"].(map[string]any)["Thing"].(map[string]any)
	}
	limit := func(d map[string]any) map[string]any {
		return operation(d)["parameters"].([]any)[1].(map[string]any)["schema"].(map[string]any)
	}
	for name, test := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"version":         {func(d map[string]any) { d["openapi"] = "3.0.3" }, "3.1.x"},
		"undeclared path": {func(d map[string]any) { operation(d)["parameters"] = operation(d)["parameters"].([]any)[1:] }, `template parameter "id" is not declared`},
		"optional path":   {func(d map[string]any) { operation(d)["parameters"].([]any)[0].(map[string]any)["required"] = false }, "must be required"},
		"read body": {func(d map[string]any) {
			operation(d)["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": true}}}
		}, "read operation declares a request body"},
		"status key": {func(d map[string]any) {
			operation(d)["responses"].(map[string]any)["2000"] = map[string]any{"description": "x"}
		}, "not a status code"},
		"dangling ref":      {func(d map[string]any) { delete(d["components"].(map[string]any)["schemas"].(map[string]any), "Thing") }, "has no target"},
		"empty required":    {func(d map[string]any) { thing(d)["required"] = []any{} }, "required must be a non-empty array"},
		"undeclared closed": {func(d map[string]any) { thing(d)["required"] = []any{"id", "name"} }, `"name" is not declared on a closed object`},
		"unknown keyword":   {func(d map[string]any) { limit(d)["maximun"] = 60 }, `unknown schema keyword "maximun"`},
		"keyword for type":  {func(d map[string]any) { limit(d)["maxLength"] = 3 }, "maxLength does not apply"},
		"inverted bounds":   {func(d map[string]any) { limit(d)["maximum"] = 0 }, "minimum 1 exceeds maximum 0"},
		"unknown type":      {func(d map[string]any) { limit(d)["type"] = "int" }, `unknown type "int"`},
		"bad pattern": {func(d map[string]any) {
			thing(d)["properties"].(map[string]any)["id"].(map[string]any)["pattern"] = "("
		}, "pattern does not compile"},
		"repeated enum": {func(d map[string]any) {
			thing(d)["properties"].(map[string]any)["id"].(map[string]any)["enum"] = []any{"a", "a"}
		}, "enum repeats"},
		"duplicate operation": {func(d map[string]any) {
			d["paths"].(map[string]any)["/v1/other"] = map[string]any{"get": map[string]any{"operationId": "get_thing", "responses": map[string]any{"204": map[string]any{"description": "x"}}}}
		}, `operationId "get_thing" is also used`},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkedDocument(t, test.mutate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
}

func TestQueryIntegersPublishTheirBounds(t *testing.T) {
	for _, integer := range []QueryInteger{{Name: "limit", Minimum: 1, Default: 12, Maximum: 60, Clamped: true}, {Name: "limit", Minimum: 1, Default: 50, Maximum: 200}} {
		m := Metadata{Audience: Clients, Query: []string{"cursor", "limit"}, QueryIntegers: []QueryInteger{integer}}
		got := querySchema(m, "limit")
		if got["type"] != "integer" || got["minimum"] != 1 || got["default"] != integer.Default {
			t.Fatalf("%+v: %#v", integer, got)
		}
		if integer.Clamped && (got["x-clamp-maximum"] != 60 || got["maximum"] != nil) || !integer.Clamped && got["maximum"] != 200 {
			t.Fatalf("%+v maximum: %#v", integer, got)
		}
		if querySchema(m, "cursor")["type"] != "string" {
			t.Fatal("an undeclared query parameter stays a string")
		}
	}
	base := Metadata{Audience: Clients, ID: "get_things", Method: "GET", Path: "/v1/things", Summary: "Read things", Access: Public, Requires: ScopeRead, Lane: Default, Cost: PageSized, Status: 200, Query: []string{"limit"}}
	for name, integer := range map[string]QueryInteger{
		"undeclared": {Name: "size", Minimum: 1, Default: 1, Maximum: 2},
		"default":    {Name: "limit", Minimum: 5, Default: 1, Maximum: 10},
		"maximum":    {Name: "limit", Minimum: 1, Default: 20, Maximum: 10},
	} {
		m := base
		m.QueryIntegers = []QueryInteger{integer}
		if err := validate(m); err == nil || !strings.Contains(err.Error(), "query integer") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
