package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
)

// variantBody writes Body's JSON object with a leading `variant` tag: the
// required discriminator of a response union (R-09b). The tag is not part of
// Body's own type, because the same value is also served untagged by routes
// that are not unions.
type variantBody struct {
	Variant string
	Body    any
}

func (t variantBody) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(t.Body)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '{' {
		return nil, errors.New("tagged response body is not an object")
	}
	tag, err := json.Marshal(t.Variant)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)+len(tag)+len(`{"variant":,`))
	out = append(out, `{"variant":`...)
	out = append(out, tag...)
	if len(raw) > 2 {
		out = append(out, ',')
	}
	return append(out, raw[1:]...), nil
}

// withVariantTag returns schema with a required const `variant` property.
func withVariantTag(schema map[string]any, variant string) map[string]any {
	properties := schema["properties"].(map[string]any)
	properties["variant"] = map[string]any{"type": "string", "const": variant}
	required, _ := schema["required"].([]string)
	for _, name := range required {
		if name == "variant" {
			return schema
		}
	}
	schema["required"] = append(required, "variant")
	return schema
}
