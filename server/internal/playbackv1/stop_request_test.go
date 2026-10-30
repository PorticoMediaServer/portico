package playbackv1

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
)

func TestStopRequestGenerationPresenceNeverFallsBack(t *testing.T) {
	for _, raw := range []string{`{"ExpectedGeneration":0}`, `{"expectedGeneration":1,"ExpectedGeneration":2}`, `{"expectedGeneration":null}`, `{"expectedGeneration":0}`, `{"expectedGeneration":-1}`, `{"expectedGeneration":1.5}`, `{"expectedGeneration":"1"}`, `{"expectedGeneration":9007199254740992}`, `{"expectedGeneration":999999999999999999999999}`, `{"expectedGeneration":1,"expectedGeneration":2}`, `{"expectedGeneration":1,"positionMs":5}`, `{"expectedGeneration":1,"positionMs":null}`} {
		var in StopRequest
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatalf("accepted invalid conditional stop %s", raw)
		}
	}
	var ordinary StopRequest
	if err := json.Unmarshal([]byte(`{"positionMs":5}`), &ordinary); err != nil || ordinary.ExpectedGeneration != nil || ordinary.PositionMs == nil {
		t.Fatalf("ordinary stop: %+v %v", ordinary, err)
	}
	var guarded StopRequest
	if err := json.Unmarshal([]byte(`{"expectedGeneration":2}`), &guarded); err != nil || guarded.ExpectedGeneration == nil || *guarded.ExpectedGeneration != 2 || guarded.PositionMs != nil {
		t.Fatalf("conditional stop: %+v %v", guarded, err)
	}
}

// Exercise the actual schema-provider entry used by apigen, not a second
// handwritten validator. Generated JS request-decoder qualification belongs to
// the official containing generation cohort; no generated output lives here.
func TestStopRequestSchemaProducerBoundary(t *testing.T) {
	schema, err := apikit.Schema(reflect.TypeFor[StopRequest]())
	if err != nil {
		t.Fatal(err)
	}
	validator := apispec.Document{}
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"empty ordinary", `{}`, true},
		{"ordinary position", `{"positionMs":5}`, true},
		{"ordinary null position", `{"positionMs":null}`, true},
		{"ordinary zero", `{"positionMs":0}`, true},
		{"ordinary negative", `{"positionMs":-1}`, true},
		{"ordinary safe minimum", `{"positionMs":-9007199254740991}`, true},
		{"ordinary safe maximum", `{"positionMs":9007199254740991}`, true},
		{"captured generation and position", `{"generation":2,"positionMs":5}`, true},
		{"captured generation only", `{"generation":2}`, true},
		{"captured null generation", `{"generation":null}`, false},
		{"captured zero generation", `{"generation":0}`, false},
		{"two generation contracts", `{"generation":2,"expectedGeneration":2}`, false},
		{"conditional minimum", `{"expectedGeneration":1}`, true},
		{"conditional maximum", `{"expectedGeneration":9007199254740991}`, true},
		{"both fields", `{"expectedGeneration":1,"positionMs":5}`, false},
		{"both fields reversed", `{"positionMs":5,"expectedGeneration":1}`, false},
		{"conditional null position", `{"expectedGeneration":1,"positionMs":null}`, false},
		{"conditional null position reversed", `{"positionMs":null,"expectedGeneration":1}`, false},
		{"null generation", `{"expectedGeneration":null}`, false},
		{"zero generation", `{"expectedGeneration":0}`, false},
		{"negative generation", `{"expectedGeneration":-1}`, false},
		{"fraction generation", `{"expectedGeneration":1.5}`, false},
		{"unsafe generation", `{"expectedGeneration":9007199254740992}`, false},
		{"overflow generation", `{"expectedGeneration":999999999999999999999999}`, false},
		{"string generation", `{"expectedGeneration":"1"}`, false},
		{"boolean generation", `{"expectedGeneration":true}`, false},
		{"object generation", `{"expectedGeneration":{}}`, false},
		{"array generation", `{"expectedGeneration":[]}`, false},
		{"ordinary unknown", `{"positionMs":5,"extra":true}`, false},
		{"conditional unknown", `{"expectedGeneration":1,"extra":true}`, false},
		{"unknown only", `{"extra":true}`, false},
		{"wrong case", `{"ExpectedGeneration":1}`, false},
		{"fraction position", `{"positionMs":1.5}`, false},
		{"string position", `{"positionMs":"5"}`, false},
		{"null body", `null`, false},
		{"array body", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			issues := validator.ValidateJSON(schema, raw)
			if got := len(issues) == 0; got != tc.valid {
				t.Fatalf("schema accepted=%t, want %t: %v", got, tc.valid, issues)
			}
			var in StopRequest
			if err := json.Unmarshal(raw, &in); (err == nil) != tc.valid {
				t.Fatalf("producer accepted=%t, want %t: %v", err == nil, tc.valid, err)
			}
			if tc.valid {
				// A producer round trip must keep its branch, including null
				// ordinary progress becoming absent rather than conditional.
				out, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				if issues := validator.ValidateJSON(schema, out); len(issues) != 0 {
					t.Fatalf("producer round trip rejected: %s: %v", out, issues)
				}
			}
		})
	}
}

func TestStopRequestDuplicateAndTrailingFieldsRemainProducerErrors(t *testing.T) {
	// A parsed JSON schema cannot detect duplicate wire keys. The unchanged
	// presence-aware producer must continue refusing these before authority IO.
	for _, raw := range []string{
		`{"positionMs":5,"positionMs":6}`,
		`{"generation":1,"generation":2}`,
		`{"positionMs":null,"positionMs":5}`,
		`{"expectedGeneration":1,"expectedGeneration":2}`,
		`{"expectedGeneration":1,"expectedGeneration":null}`,
		`{"positionMs":5} {}`,
		`{"expectedGeneration":1} {}`,
	} {
		var in StopRequest
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatalf("producer accepted invalid wire body: %s", raw)
		}
	}
}
