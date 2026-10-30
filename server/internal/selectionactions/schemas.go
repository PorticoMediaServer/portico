package selectionactions

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

func strictSchema(t reflect.Type) map[string]any {
	shape, err := apikit.Schema(t)
	if err != nil {
		panic(err)
	}
	shape["additionalProperties"] = false
	return shape
}
func enum(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
func wireID(max int) map[string]any {
	return map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]+$", "minLength": 1, "maxLength": max}
}
func (Pending) JSONSchema() map[string]any {
	type wire Pending
	shape := strictSchema(reflect.TypeFor[wire]())
	p := shape["properties"].(map[string]any)
	p["kind"] = enum("selection-action-preparing")
	p["preparationId"] = wireID(128)
	p["retryAfterMs"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 60000}
	return shape
}
func (Preview) JSONSchema() map[string]any {
	type wire Preview
	shape := strictSchema(reflect.TypeFor[wire]())
	p := shape["properties"].(map[string]any)
	p["kind"] = enum("selection-action-preview")
	p["action"] = enum("delete", "download")
	p["preparationId"] = wireID(128)
	p["basis"] = map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}
	for _, key := range []string{"total", "files", "bytes"} {
		p[key] = map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740991}
	}
	p["total"].(map[string]any)["minimum"] = 1
	p["confirmationKind"] = enum("title", "count")
	p["expiresAt"] = map[string]any{"type": "string", "format": "date-time"}
	p["sample"].(map[string]any)["maxItems"] = 20
	return shape
}
func (Job) JSONSchema() map[string]any {
	type wire Job
	shape := strictSchema(reflect.TypeFor[wire]())
	p := shape["properties"].(map[string]any)
	p["operationId"] = wireID(160)
	p["action"] = enum("delete", "download")
	p["state"] = enum("capturing", "sealed", "running", "cancelling", "complete", "partial", "failed", "cancelled")
	for _, key := range []string{"total", "admitted", "done", "failed", "bytesDeleted", "filesDeleted"} {
		p[key] = map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740991}
	}
	p["total"].(map[string]any)["minimum"] = 1
	p["retryAfterMs"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 60000}
	return shape
}
func (Confirm) JSONSchema() map[string]any {
	type wire Confirm
	shape := strictSchema(reflect.TypeFor[wire]())
	p := shape["properties"].(map[string]any)
	p["operationId"] = wireID(160)
	p["preparationId"] = wireID(128)
	return shape
}
