package playbackv1

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

func (Selector) JSONSchema() map[string]any {
	type wire Selector
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	properties := shape["properties"].(map[string]any)
	variants := make([]any, 0, 5)
	for _, name := range []string{"container", "query", "saved", "items", "journey"} {
		variants = append(variants, map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{name: properties[name]},
			"required":   []string{name},
		})
	}
	return map[string]any{"oneOf": variants}
}

func (JourneySelector) JSONSchema() map[string]any {
	type wire JourneySelector
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	properties := shape["properties"].(map[string]any)
	for _, name := range []string{"from", "to"} {
		field := properties[name].(map[string]any)
		field["minLength"], field["maxLength"], field["pattern"] = 1, 128, itemIDPattern.String()
	}
	length := properties["length"].(map[string]any)
	length["minimum"], length["maximum"] = 2, 128
	return shape
}
