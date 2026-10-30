package catalog

import (
	"encoding/json"
	"math"
	"testing"
)

// R-09b: the item's duration is one object, not a union. A representable length
// is durationMs; one the millisecond contract cannot hold is durationState
// "unrepresentable" with no durationMs.
func TestItemDurationIsOneObject(t *testing.T) {
	schema := Item{}.JSONSchema()
	if _, union := schema["anyOf"]; union {
		t.Fatal("the item schema is still a union")
	}
	required := schema["required"].([]string)
	for _, name := range required {
		if name == "durationMs" || name == "durationState" {
			t.Fatalf("%s must be optional, required: %v", name, required)
		}
	}
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["durationMs"]; !ok {
		t.Fatal("durationMs is not published")
	}
	if properties["durationState"].(map[string]any)["enum"].([]string)[0] != "unrepresentable" {
		t.Fatal("durationState does not publish its one state")
	}
	for _, tc := range []struct {
		duration float64
		state    bool
	}{{90.5, false}, {math.Inf(1), true}} {
		raw, err := json.Marshal(Item{ID: "i", Duration: tc.duration})
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err = json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		_, hasMs := wire["durationMs"]
		_, hasState := wire["durationState"]
		if hasMs == tc.state || hasState != tc.state {
			t.Errorf("duration %v: durationMs=%v durationState=%v", tc.duration, hasMs, hasState)
		}
	}
}
