package access

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

const shortInvitationPattern = `^(?:[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{16}|[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{4}(?:-[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{4}){3})$`

// InvitationCapabilityFields checks field presence before decoding values. An
// empty/null alternate capability must never turn a mixed form into a valid one.
func InvitationCapabilityFields(raw []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, invalid("code", "shortCode")
	}
	_, long := fields["code"]
	_, short := fields["shortCode"]
	if long == short {
		return nil, invalid("code", "shortCode")
	}
	var code string
	field := "code"
	if short {
		field = "shortCode"
	}
	if bytes.Equal(bytes.TrimSpace(fields[field]), []byte("null")) || json.Unmarshal(fields[field], &code) != nil {
		return nil, invalid(field)
	}
	return fields, nil
}

func (q *InvitationPreviewRequest) UnmarshalJSON(raw []byte) error {
	fields, err := InvitationCapabilityFields(raw)
	if err != nil {
		return err
	}
	if len(fields) != 1 {
		return invalid("code", "shortCode")
	}
	type wire InvitationPreviewRequest
	var value wire
	if err = json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*q = InvitationPreviewRequest(value)
	return nil
}

// InvitationCapabilityShape applies exactly the runtime grammar to one closed
// wire variant. Neither this helper nor the oneOf accepts a secret resolver.
func InvitationCapabilityShape(typ reflect.Type) map[string]any {
	shape, err := apikit.Schema(typ)
	if err != nil {
		panic(err)
	}
	properties := shape["properties"].(map[string]any)
	if code, ok := properties["code"].(map[string]any); ok {
		code["minLength"] = 32
		code["maxLength"] = 128
	}
	if short, ok := properties["shortCode"].(map[string]any); ok {
		short["pattern"] = shortInvitationPattern
		short["minLength"] = 16
		short["maxLength"] = 19
	}
	return shape
}
func (InvitationPreviewRequest) JSONSchema() map[string]any {
	return map[string]any{"oneOf": []any{
		InvitationCapabilityShape(reflect.TypeFor[struct {
			Code string `json:"code"`
		}]()),
		InvitationCapabilityShape(reflect.TypeFor[struct {
			ShortCode string `json:"shortCode"`
		}]()),
	}}
}
func (Acceptance) JSONSchema() map[string]any {
	return map[string]any{"oneOf": []any{
		InvitationCapabilityShape(reflect.TypeFor[struct {
			Code     string `json:"code"`
			Username string `json:"username"`
			Password string `json:"password"`
			Name     string `json:"name,omitempty"`
		}]()),
		InvitationCapabilityShape(reflect.TypeFor[struct {
			ShortCode string `json:"shortCode"`
			Username  string `json:"username"`
			Password  string `json:"password"`
			Name      string `json:"name,omitempty"`
		}]()),
	}}
}
