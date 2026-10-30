package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

// Bounds belong to this projection, so registration does not compete with the
// Home catalogue's shared schema file. The response and apigen use one type.
func (ActivitySummary) JSONSchema() map[string]any {
	type wire ActivitySummary
	shape, err := apikit.Schema(reflect.TypeFor[wire]())
	if err != nil {
		panic(err)
	}
	props := shape["properties"].(map[string]any)
	for key, cap := range map[string]int{"streams": 3, "tasks": 3} {
		section := props[key].(map[string]any)["properties"].(map[string]any)
		section["count"].(map[string]any)["minimum"] = 0
		section["items"].(map[string]any)["maxItems"] = cap
	}
	streams := props["streams"].(map[string]any)["properties"].(map[string]any)
	streams["convertingCount"].(map[string]any)["minimum"] = 0
	overflow := streams["overflow"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"count", "convertingCount"} {
		overflow[key].(map[string]any)["minimum"] = 0
	}
	return shape
}
