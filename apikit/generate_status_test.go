package apikit

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type statusResponse struct{}

func (statusResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{
		map[string]any{"type": "object", "properties": map[string]any{"variant": map[string]any{"type": "string", "const": "ready"}, "id": map[string]any{"type": "string"}}, "required": []string{"variant", "id"}},
		map[string]any{"type": "object", "properties": map[string]any{"variant": map[string]any{"type": "string", "const": "waiting"}, "wait": map[string]any{"type": "integer"}}, "required": []string{"variant", "wait"}},
	}}
}

func (v statusResponse) JSONSchemaForStatus(status int) map[string]any {
	variants := v.JSONSchema()["anyOf"].([]any)
	switch status {
	case 201:
		return variants[0].(map[string]any)
	case 202:
		return variants[1].(map[string]any)
	default:
		return nil
	}
}

func TestGenerateKeepsStatusShapesAndSharedResponseUnion(t *testing.T) {
	definition := Definition{
		Metadata: Metadata{Audience: Clients, ID: "status_reply", Method: "POST", Path: "/v1/status-reply", Summary: "Status response", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 201, SuccessStatuses: []int{202}},
		Request:  reflect.TypeFor[struct{}](), Response: reflect.TypeFor[statusResponse](),
	}
	generated, err := Generate([]Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err = json.Unmarshal(generated.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	responses := spec.Paths["/v1/status-reply"]["post"].Responses
	for status, field := range map[string]string{"201": "id", "202": "wait"} {
		shape := responses[status].Content["application/json"].Schema
		if required, ok := shape["required"].([]any); !ok || len(required) != 2 || required[0] != "variant" || required[1] != field {
			t.Fatalf("status %s lost its distinct shape: %v", status, shape)
		}
	}
	if !strings.Contains(string(generated.Schemas), `"x-discriminator": "variant"`) || !strings.Contains(string(generated.Typescript), `{ readonly unknownVariant: true; readonly "variant": string }`) {
		t.Fatal("a tagged response union lost its discriminator or its unknown variant")
	}
	if !strings.Contains(string(generated.Typescript), `"id": string`) || !strings.Contains(string(generated.Typescript), `"wait": number`) || !strings.Contains(string(generated.Typescript), " | ") {
		t.Fatal("SDK response lost a declared status variant")
	}
	if _, ok := responses["200"]; ok {
		t.Fatal("a schema provider declared an undeclared status")
	}
}
