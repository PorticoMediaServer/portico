package catalog

// The selector is a tagged union on the wire, not three independently optional
// fields. Publish that same validation to generated third-party clients.
func (JobSelector) JSONSchema() map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	text := func() map[string]any { return map[string]any{"type": "string", "minLength": 1, "maxLength": 256} }
	variants := []any{
		object(map[string]any{"container": object(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"show", "season", "album", "artist", "book", "collection", "playlist", "library"}}, "id": text()}, "kind", "id")}, "container"),
		object(map[string]any{"items": object(map[string]any{"ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 200, "uniqueItems": true, "items": text()}}, "ids")}, "items"),
		object(map[string]any{"query": object(map[string]any{"selectionBasis": map[string]any{"type": "string", "pattern": SelectionBasisExpression}, "libraryId": text(), "pivot": text(), "filter": map[string]any{"type": "object", "additionalProperties": map[string]any{}}, "sort": map[string]any{"type": "array", "items": object(map[string]any{"field": text(), "direction": map[string]any{"type": "string", "enum": []string{"asc", "desc"}}}, "field", "direction")}}, "libraryId", "pivot")}, "query"),
	}
	properties := map[string]any{}
	for _, variant := range variants {
		for key, value := range variant.(map[string]any)["properties"].(map[string]any) {
			properties[key] = value
		}
	}
	return map[string]any{"type": "object", "properties": properties, "additionalProperties": false, "oneOf": variants}
}
func (JobPersonalArgs) JSONSchema() map[string]any { return jobArgumentsSchema() }

func personalArgumentsSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "minProperties": 1, "properties": map[string]any{
		"watched": map[string]any{"type": "boolean"}, "favorite": map[string]any{"type": "boolean"}, "watchlist": map[string]any{"type": "boolean"},
		"rating": map[string]any{"type": "integer", "nullable": true, "minimum": 0, "maximum": 10},
	}}
}

func jobArgumentsSchema() map[string]any {
	object := func(p map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": p, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 256}
	revision := map[string]any{"type": "integer", "minimum": 1, "maximum": int64(9007199254740991)}
	placement := map[string]any{"oneOf": []any{map[string]any{"type": "string", "enum": []string{"end", "next"}}, object(map[string]any{"after": text}, "after")}}
	personal := personalArgumentsSchema()
	list := object(map[string]any{"add": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "remove": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}})
	field := object(map[string]any{"value": map[string]any{"type": "string"}, "values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "locked": map[string]any{"type": "boolean"}, "useAutomatic": map[string]any{"type": "boolean"}})
	metadata := object(map[string]any{"fields": map[string]any{"type": "object", "additionalProperties": field}, "lists": map[string]any{"type": "object", "additionalProperties": list}, "genres": list, "lockEdited": map[string]any{"type": "boolean"}})
	metadata["minProperties"] = 1
	variants := []any{personal, object(map[string]any{"playlistId": text, "expectedRevision": revision, "placement": placement}, "playlistId", "expectedRevision", "placement"), object(map[string]any{"collectionId": text, "expectedRevision": revision}, "collectionId", "expectedRevision"), metadata, object(map[string]any{})}
	properties := map[string]any{}
	for _, variant := range variants {
		for key, value := range variant.(map[string]any)["properties"].(map[string]any) {
			properties[key] = value
		}
	}
	return map[string]any{"type": "object", "properties": properties, "additionalProperties": false, "oneOf": variants}
}

func (JobRequest) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"operationId", "command", "selector", "args"}, "additionalProperties": false, "properties": map[string]any{
		"operationId": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"},
		"command":     map[string]any{"type": "string", "enum": []string{"personal-state", "playlist-add", "collection-add", "metadata-edit", "refresh"}},
		"selector":    (JobSelector{}).JSONSchema(), "args": jobArgumentsSchema(),
		"expected": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"catalogRevision": map[string]any{"type": "string"}}},
	}}
}
