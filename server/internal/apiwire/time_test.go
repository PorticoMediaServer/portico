package apiwire

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
	"testing"
)

func TestInstantWireContract(t *testing.T) {
	var value Instant
	if err := json.Unmarshal([]byte(`"2026-10-13T02:00:00.125Z"`), &value); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil || string(raw) != `"2026-10-13T02:00:00.125Z"` {
		t.Fatalf("%s %v", raw, err)
	}
	if err = json.Unmarshal([]byte(`123`), &value); err == nil {
		t.Fatal("numeric timestamp accepted")
	}
	schema, err := apikit.Schema(reflect.TypeFor[Instant]())
	if err != nil || schema["type"] != "string" {
		t.Fatal(schema, err)
	}
}
func TestDurationWireContract(t *testing.T) {
	type sample struct {
		Duration float64 `json:"duration"`
	}
	fields := map[string]string{"duration": "durationMs"}
	raw, err := MarshalDurations(sample{1.2345}, fields)
	if err != nil || string(raw) != `{"durationMs":1235}` {
		t.Fatal(string(raw), err)
	}
	var value sample
	if err = UnmarshalDurations(raw, &value, fields); err != nil || value.Duration != 1.235 {
		t.Fatal(value, err)
	}
	for _, bad := range []string{`{"duration":1}`, `{"durationMs":1.5}`, `{"durationMs":-1}`, `{"durationMs":1,"unknown":true}`} {
		if err = UnmarshalDurations([]byte(bad), &value, fields); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	schema := DurationSchema(reflect.TypeFor[sample](), fields)
	property := schema["properties"].(map[string]any)["durationMs"].(map[string]any)
	if property["type"] != "integer" {
		t.Fatal(schema)
	}
}
