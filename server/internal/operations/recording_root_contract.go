package operations

// RecordingRootPathSchema shares the real platform-rooted path domain with the
// route request. Clean-path and UTF-8 byte limits are enforced by the producer.
func RecordingRootPathSchema(allowEmpty bool) map[string]any {
	path := map[string]any{
		"type": "string", "minLength": 1, "maxLength": 1024,
		"pattern": `^(?:/|[A-Za-z]:[\\/]|\\\\)[^\x00-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]*$`,
		"description": "Absolute, already-clean native filesystem path; at most 1024 UTF-8 bytes; no control or bidi override characters.",
	}
	if allowEmpty { return map[string]any{"anyOf": []any{map[string]any{"type": "string", "enum": []string{""}}, path}} }
	return path
}

func (RecordingRootStatus) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"revision", "requested", "effective", "pendingRestart", "operatorControlled"},
		"properties": map[string]any{
			"revision": map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991},
			"requested": RecordingRootPathSchema(true), "effective": RecordingRootPathSchema(true),
			"pendingRestart": map[string]any{"type": "boolean"}, "operatorControlled": map[string]any{"type": "boolean"},
		},
	}
}
