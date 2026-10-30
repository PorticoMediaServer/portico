package identity

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

type MemberResetRequest struct {
	ExpectedRevision  int64   `json:"expectedRevision"`
	TurnOffTwoStep    bool    `json:"turnOffTwoStep,omitempty"`
	OriginalResetCode *string `json:"originalResetCode,omitempty"`
}

// Only omission requests a fresh reset. A present invalid replacement witness
// must never decode into that branch, including JSON null and duplicate keys.
func (q *MemberResetRequest) UnmarshalJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return ErrDirectInput
	}
	var out MemberResetRequest
	seen := make(map[string]bool, 3)
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return ErrDirectInput
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrDirectInput
		}
		switch key {
		case "expectedRevision":
			if json.Unmarshal(value, &out.ExpectedRevision) != nil {
				return ErrDirectInput
			}
		case "turnOffTwoStep":
			if json.Unmarshal(value, &out.TurnOffTwoStep) != nil {
				return ErrDirectInput
			}
		case "originalResetCode":
			var code string
			if json.Unmarshal(value, &code) != nil || !validMemberResetOriginalCode(code) {
				return ErrDirectInput
			}
			out.OriginalResetCode = &code
		default:
			return ErrDirectInput
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') || !seen["expectedRevision"] {
		return ErrDirectInput
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrDirectInput
	}
	*q = out
	return nil
}

func validMemberResetOriginalCode(code string) bool {
	if len(code) != 43 {
		return false
	}
	for i := range code {
		c := code[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (MemberResetRequest) JSONSchema() map[string]any {
	type wire MemberResetRequest
	shape, err := apikit.Schema(reflect.TypeFor[wire]())
	if err != nil {
		panic(err)
	}
	shape["additionalProperties"] = false
	shape["properties"].(map[string]any)["originalResetCode"] = map[string]any{
		"type": "string", "pattern": `^[A-Za-z0-9_-]{43}$`, "minLength": 43, "maxLength": 43,
	}
	return shape
}
