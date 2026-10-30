package apiwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

// MarshalDurations converts the engine's seconds into integer milliseconds at
// the boundary, preserving null and omitted values. Every converted field is
// named with Ms so consumers cannot silently confuse the units.
func MarshalDurations(value any, fields map[string]string) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	for old, next := range fields {
		raw, ok := object[old]
		if !ok {
			continue
		}
		delete(object, old)
		if string(raw) == "null" {
			object[next] = raw
			continue
		}
		var seconds float64
		if err = json.Unmarshal(raw, &seconds); err != nil {
			return nil, err
		}
		ms := math.Round(seconds * 1000)
		if math.IsNaN(ms) || math.IsInf(ms, 0) || ms > 9007199254740991 || ms < 0 {
			return nil, fmt.Errorf("invalid duration")
		}
		object[next], err = json.Marshal(int64(ms))
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(object)
}
func UnmarshalDurations(raw []byte, value any, fields map[string]string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	for old, next := range fields {
		if _, ok := object[old]; ok {
			return fmt.Errorf("use %s in milliseconds", next)
		}
		raw, ok := object[next]
		if !ok {
			continue
		}
		delete(object, next)
		if string(raw) == "null" {
			object[old] = raw
			continue
		}
		var ms int64
		if err := json.Unmarshal(raw, &ms); err != nil {
			return err
		}
		if ms < 0 || ms > 9007199254740991 {
			return fmt.Errorf("invalid duration")
		}
		object[old], _ = json.Marshal(float64(ms) / 1000)
	}
	converted, err := json.Marshal(object)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(converted))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
func DurationSchema(kind reflect.Type, fields map[string]string) map[string]any {
	schema, err := apikit.Schema(kind)
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	for old, next := range fields {
		property, ok := properties[old]
		if !ok {
			continue
		}
		delete(properties, old)
		p := property.(map[string]any)
		nextProperty := map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740991}
		if nullable, ok := p["nullable"].(bool); ok && nullable {
			nextProperty["nullable"] = true
		}
		properties[next] = nextProperty
	}
	if required, ok := schema["required"].([]string); ok {
		for i, key := range required {
			if next, ok := fields[key]; ok {
				required[i] = next
			}
		}
	}
	return schema
}
