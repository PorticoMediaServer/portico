package playback

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

func (ClientVideoCodec) JSONSchema() map[string]any {
	type codec ClientVideoCodec
	schema, err := apikit.Schema(reflect.TypeFor[codec]())
	if err != nil {
		panic(err)
	}
	schema["properties"].(map[string]any)["dolbyVisionProfile8Compatibility"] = map[string]any{
		"type": "array", "maxItems": 3, "uniqueItems": true,
		"items": map[string]any{"type": "integer", "enum": []int{1, 2, 4}},
	}
	return schema
}
