package downloads

func (ContainerRequest) JSONSchema() map[string]any {
	id := map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,160}$"}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operationId", "target", "deviceId", "policy"}, "properties": map[string]any{
		"operationId": id, "deviceId": id, "quality": RequestQuality("").JSONSchema(),
		"target": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "id"}, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"show", "season", "album", "book", "playlist"}}, "id": id}},
		"policy": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"episodes"}, "properties": map[string]any{"episodes": map[string]any{"type": "string", "enum": []string{"all", "unwatched", "next"}}, "keepNext": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}}},
	}}
}
