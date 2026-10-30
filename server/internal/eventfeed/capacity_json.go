package eventfeed

import (
	"encoding/json"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

type ordinaryEvent Event

type capacityEnvelope struct {
	Variant string `json:"variant"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	At      string `json:"at"`
}

// MarshalJSON preserves ordinary event serialization. A capacity hint never
// publishes even empty resource/revision fields or accidentally supplied data.
func (e Event) MarshalJSON() ([]byte, error) {
	if e.Type == apievents.CapacityChangedType {
		return json.Marshal(capacityEnvelope{Variant: "capacity", ID: e.ID, Type: e.Type, At: e.At})
	}
	return json.Marshal(struct {
		Variant string `json:"variant"`
		ordinaryEvent
	}{"change", ordinaryEvent(e)})
}

// JSONSchema keeps the poll and SSE declarations consistent with the closed
// capacity envelope while preserving the ordinary event's existing fields.
func (Event) JSONSchema() map[string]any {
	ordinary, err := apikit.Schema(reflect.TypeFor[ordinaryEvent]())
	if err != nil {
		panic(err)
	}
	// Exclude the one closed type from the open ordinary-type vocabulary. The
	// existing generated response validator supports ECMAScript patterns.
	ordinary["properties"].(map[string]any)["variant"] = map[string]any{"type": "string", "const": "change"}
	ordinary["required"] = append(ordinary["required"].([]string), "variant")
	ordinary["properties"].(map[string]any)["type"].(map[string]any)["pattern"] = `^(?!capacity\.changed$)[\s\S]*$`
	return map[string]any{"oneOf": []any{map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"variant", "id", "type", "at"},
		"properties": map[string]any{
			"variant": map[string]any{"type": "string", "const": "capacity"},
			"id":      map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
			"type":    map[string]any{"type": "string", "const": apievents.CapacityChangedType},
			"at":      map[string]any{"type": "string"},
		},
	}, ordinary}}
}
