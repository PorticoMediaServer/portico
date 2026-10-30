package metadata

// The ready contract distinguishes a complete small cardinality from an
// overflow witness. The latter never publishes a guessed exact distinctCount.
func (BulkPreviewField) JSONSchema() map[string]any {
	count := func(minimum int) map[string]any { return map[string]any{"type": "integer", "minimum": minimum} }
	value := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"value", "count"}, "properties": map[string]any{"value": map[string]any{"type": "string"}, "count": count(1)}}
	branch := func(state string) map[string]any {
		properties := map[string]any{
			"state":   map[string]any{"type": "string", "enum": []string{state}},
			"values":  map[string]any{"type": "array", "maxItems": bulkPreviewValues, "items": value},
			"entries": map[string]any{"type": "array", "maxItems": bulkPreviewValues, "items": value},
			"locked":  count(0), "truncated": map[string]any{"type": "boolean"},
		}
		required := []string{"state", "values", "entries", "locked", "truncated"}
		if state == "many" {
			properties["atLeast"] = map[string]any{"type": "integer", "enum": []int{bulkPreviewValues + 1}}
			properties["values"] = map[string]any{"type": "array", "maxItems": 0, "items": value}
			properties["truncated"] = map[string]any{"type": "boolean", "enum": []bool{true}}
			required = append(required, "atLeast")
		} else {
			lo := 2
			if state == "shared" {
				lo = 1
				properties["value"] = map[string]any{"type": "string"}
				required = append(required, "value")
			}
			hi := bulkPreviewValues
			if state == "shared" {
				hi = 1
			}
			properties["distinctCount"] = map[string]any{"type": "integer", "minimum": lo, "maximum": hi}
			properties["values"] = map[string]any{"type": "array", "minItems": lo, "maxItems": hi, "items": value}
			required = append(required, "distinctCount")
		}
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}
	}
	return map[string]any{"oneOf": []any{branch("shared"), branch("mixed"), branch("many")}}
}
