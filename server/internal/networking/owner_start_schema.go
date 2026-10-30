package networking

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

// The schema matches the existing strict decoder, including canonical 32-byte
// RawURL values and the installed device's unchanged identifier grammar.
func (ClaimOwnerStartRequest) JSONSchema() map[string]any {
	random32 := map[string]any{"type": "string", "minLength": 43, "maxLength": 43, "pattern": "^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$"}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"state", "codeChallenge", "installationId"},
		"properties": map[string]any{
			"state": random32, "codeChallenge": random32,
			"installationId": map[string]any{"type": "string", "minLength": 32, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"},
		},
	}
}

func (ClaimActionRequest) JSONSchema() map[string]any {
	expected, err := apikit.Schema(reflect.TypeFor[ClaimExpected]())
	if err != nil {
		panic(err)
	}
	expected["additionalProperties"] = false
	ordinary := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"expected"},
		"properties": map[string]any{
			"expected": expected, "approvalEnvelope": map[string]any{"type": "string"},
			"ownerStart": (ClaimOwnerStartRequest{}).JSONSchema(),
		},
	}
	setup := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"expected", "setupStart"}, "properties": map[string]any{"expected": expected, "setupStart": (ClaimSetupStartRequest{}).JSONSchema()}}
	return map[string]any{"oneOf": []any{ordinary, setup}}
}
