package apispec

import (
	"encoding/json"
	"testing"
)

func TestPrivatePreparationShapeChecksBoundsAndClosedMembers(t *testing.T) {
	shape := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"retryAfterMs": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}},
		"required":   []string{"retryAfterMs"},
	}
	// The checker accepts both an in-memory provider schema and the same
	// schema loaded from the generated document; neither can erase its bounds.
	raw, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, schema := range []map[string]any{shape, decoded} {
		for _, body := range []string{`{"retryAfterMs":1}`, `{"retryAfterMs":10000}`} {
			if issues := (Document{}).ValidateJSON(schema, []byte(body)); len(issues) != 0 {
				t.Fatalf("valid wait refused: %s %v", body, issues)
			}
		}
		for _, body := range []string{`{}`, `{"retryAfterMs":0}`, `{"retryAfterMs":10001}`, `{"retryAfterMs":1.5}`, `{"retryAfterMs":"1"}`, `{"retryAfterMs":1,"id":"fabricated"}`} {
			if issues := (Document{}).ValidateJSON(schema, []byte(body)); len(issues) == 0 {
				t.Fatalf("invalid wait accepted: %s", body)
			}
		}
	}
}

func TestOneOfRequiresExactlyOneResponseBranch(t *testing.T) {
	branch := map[string]any{"type": "integer"}
	for _, test := range []struct {
		kind string
		want bool
	}{{"oneOf", false}, {"anyOf", true}} {
		issues := (Document{}).ValidateJSON(map[string]any{test.kind: []any{branch, branch}}, []byte(`5`))
		if (len(issues) == 0) != test.want {
			t.Fatalf("%s ambiguous branch result: %v", test.kind, issues)
		}
	}
}
