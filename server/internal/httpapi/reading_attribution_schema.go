package httpapi

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type ReadingAttributionSource string

func (ReadingAttributionSource) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{
		string(compactcatalog.ReadingOwner), string(compactcatalog.ReadingFileMetadata),
		string(compactcatalog.ReadingFileName), string(compactcatalog.ReadingFolder),
	}}
}

type ReadingAuthor struct {
	Value  string                   `json:"value"`
	Source ReadingAttributionSource `json:"source"`
}

// Source describes the series name. Number provenance stays in the private
// accepted-value/editor model; the opaque ID remains scoped to its library.
type ReadingSeries struct {
	ID     string                   `json:"id"`
	Name   string                   `json:"name"`
	Number *float64                 `json:"number,omitempty"`
	Source ReadingAttributionSource `json:"source"`
}

func (ReadingAuthor) JSONSchema() map[string]any {
	type wire ReadingAuthor
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	value := shape["properties"].(map[string]any)["value"].(map[string]any)
	value["minLength"], value["maxLength"] = 1, 4096
	return shape
}

func (ReadingSeries) JSONSchema() map[string]any {
	type wire ReadingSeries
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	properties := shape["properties"].(map[string]any)
	properties["id"].(map[string]any)["minLength"] = 1
	name := properties["name"].(map[string]any)
	name["minLength"], name["maxLength"] = 1, 4096
	properties["number"].(map[string]any)["minimum"] = 0
	return shape
}
