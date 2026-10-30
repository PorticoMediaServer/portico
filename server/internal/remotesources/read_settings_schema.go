package remotesources

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

func readSettingsWireSchema[T any]() map[string]any {
	schema, err := apikit.Schema(reflect.TypeFor[T]())
	if err != nil {
		panic(err)
	}
	schema["additionalProperties"] = false
	return schema
}

func (SourceReadValues) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"scanDepth", "wholeFileReads", "connections", "requestsPerSecond", "bytesPerSecond"},
		"properties": map[string]any{
			"scanDepth":                  map[string]any{"type": "string", "enum": []string{"file_list_only", "basic", "complete"}},
			"wholeFileReads":             map[string]any{"type": "boolean"},
			"allowAdvancedMediaAnalysis": map[string]any{"type": "boolean"},
			"connections":                map[string]any{"type": "integer", "minimum": 1, "maximum": 32},
			"requestsPerSecond":          map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			"bytesPerSecond": map[string]any{"anyOf": []any{
				map[string]any{"type": "integer", "const": 0},
				map[string]any{"type": "integer", "minimum": 1_000_000, "maximum": 1_000_000_000, "multipleOf": 1_000_000},
			}},
		},
	}
}

func (SourceReadSettingsChange) JSONSchema() map[string]any {
	type wire SourceReadSettingsChange
	schema := readSettingsWireSchema[wire]()
	properties := schema["properties"].(map[string]any)
	properties["operationId"].(map[string]any)["pattern"] = opPattern.String()
	properties["expectedGeneration"].(map[string]any)["minLength"] = 1
	properties["expectedRevision"].(map[string]any)["minimum"] = 0
	return schema
}
