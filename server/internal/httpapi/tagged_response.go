package httpapi

import (
	"encoding/json"
	"errors"
)

// taggedBody writes a response body with its required const union tag first
// (R-09b). Server and clients both require the tag; the shared schema helper
// below declares it on the matching alternative.
type taggedBody struct {
	key, value string
	body       any
}

func (t taggedBody) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(t.body)
	if err != nil {
		return nil, err
	}
	return spliceTag(raw, t.key, t.value)
}

func spliceTag(raw []byte, key, value string) ([]byte, error) {
	if len(raw) < 2 || raw[0] != '{' {
		return nil, errors.New("tagged response body is not an object")
	}
	name, err := json.Marshal(key)
	if err != nil {
		return nil, err
	}
	tag, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := append(append(append([]byte{'{'}, name...), ':'), tag...)
	if string(raw) != "{}" {
		out = append(out, ',')
	}
	return append(out, raw[1:]...), nil
}

// withTag adds the required const union tag to an object schema.
func withTag(schema map[string]any, key, value string) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		panic("tagged schema has no properties")
	}
	properties[key] = map[string]any{"type": "string", "const": value}
	switch required := schema["required"].(type) {
	case []string:
		schema["required"] = append(required, key)
	case []any:
		schema["required"] = append(required, key)
	default:
		schema["required"] = []string{key}
	}
	return schema
}
