package mediaanalysis

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

// Preserve the real owner's projection while closing its existing coded states.
func (SonicStatusView) JSONSchema() map[string]any {
	type reflected SonicStatusView
	schema, err := apikit.Schema(reflect.TypeFor[reflected]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	properties["state"] = map[string]any{"type": "string", "enum": []string{"disabled", "unavailable", "complete", "paused", "analyzing"}}
	properties["reasonCode"] = map[string]any{"type": "string", "enum": []string{"inference_failed", "inference_not_verified", "runtime_or_model_missing", "scan_policy_disallows"}}
	return schema
}
