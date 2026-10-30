package apikit

import (
	"reflect"
	"strings"
	"testing"
)

type kindTaggedReply struct{}

func (kindTaggedReply) JSONSchema() map[string]any {
	tag := func(value string) map[string]any { return map[string]any{"type": "string", "const": value} }
	return map[string]any{"anyOf": []any{
		map[string]any{"type": "object", "properties": map[string]any{"kind": tag("a"), "id": map[string]any{"type": "string"}}, "required": []string{"kind", "id"}},
		map[string]any{"type": "object", "properties": map[string]any{"kind": tag("b"), "wait": map[string]any{"type": "integer"}}, "required": []string{"kind", "wait"}},
	}}
}

// R-09b: response unions carry one tag name, variant. Any other tag fails
// generation unless that exact union node is grandfathered with that tag.
func TestResponseUnionsAreTaggedByVariantOnly(t *testing.T) {
	definition := Definition{Metadata: Metadata{Audience: Clients, ID: "kind_reply", Method: "GET", Path: "/v1/kind-reply", Summary: "Kind-tagged reply", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200},
		Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[kindTaggedReply]()}
	if _, err := Generate([]Definition{definition}); err == nil || !strings.Contains(err.Error(), `tagged by "variant"`) {
		t.Fatalf("a kind-tagged response union was accepted: %v", err)
	}
	generated, err := GenerateWith([]Definition{definition}, Options{GrandfatheredUnionTags: map[string]string{"kind_reply $": "kind"}})
	if err != nil || !strings.Contains(string(generated.Schemas), `"x-discriminator": "kind"`) {
		t.Fatalf("a grandfathered domain tag was refused: %v", err)
	}
	if _, err = GenerateWith([]Definition{definition}, Options{GrandfatheredUnionTags: map[string]string{"kind_reply $": "type"}}); err == nil {
		t.Fatal("a grandfathered entry naming another tag was accepted")
	}
}
