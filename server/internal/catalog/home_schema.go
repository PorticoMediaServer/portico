package catalog

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

// The destination is one tagged choice. Query uses the same bounded browse
// expression vocabulary as saved views; its recursive contents are validated
// by the browse parser, rather than expanding an unbounded reflected schema.
func (HomeSeeAll) JSONSchema() map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	text := func() map[string]any { return map[string]any{"type": "string", "minLength": 1, "maxLength": 256} }
	tag := func(value string) map[string]any { return map[string]any{"type": "string", "enum": []string{value}} }
	sort := map[string]any{"type": "array", "minItems": 1, "maxItems": BrowseMaximumSorts, "items": object(map[string]any{"field": text(), "direction": map[string]any{"type": "string", "enum": []string{"asc", "desc"}}}, "field", "direction")}
	variants := []any{
		object(map[string]any{"type": tag("guide"), "group": text(), "sourceId": text()}, "type"),
		object(map[string]any{"type": tag("library"), "libraryId": text(), "pivot": text(), "query": map[string]any{"type": "object", "additionalProperties": map[string]any{}}, "sort": sort}, "type", "libraryId", "pivot", "sort"),
		object(map[string]any{"type": tag("row"), "rowId": text()}, "type", "rowId"),
		object(map[string]any{"type": tag("collection"), "resourceId": text(), "libraryId": text()}, "type", "resourceId", "libraryId"),
		object(map[string]any{"type": tag("saved_view"), "resourceId": text()}, "type", "resourceId"),
	}
	properties := map[string]any{"type": map[string]any{"type": "string", "enum": []string{"library", "row", "saved_view", "collection", "guide"}}}
	for _, variant := range variants {
		for key, value := range variant.(map[string]any)["properties"].(map[string]any) {
			if key != "type" {
				properties[key] = value
			}
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": []string{"type"}, "additionalProperties": false, "oneOf": variants}
}

func (ContentSeeAll) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"pivot", "sort"}, "properties": map[string]any{
		"pivot": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
		"query": map[string]any{"type": "object", "additionalProperties": map[string]any{}},
		"sort":  map[string]any{"type": "array", "maxItems": BrowseMaximumSorts, "items": map[string]any{"type": "object", "required": []string{"field", "direction"}, "properties": map[string]any{"field": map[string]any{"type": "string"}, "direction": map[string]any{"type": "string", "enum": []string{"asc", "desc"}}}}},
	}}
}

// A personal family expands once. Its children are ordinary row previews,
// never another family, so the wire contract has a finite depth and row bound.
func (HomeRow) JSONSchema() map[string]any {
	fields := []reflect.StructField{}
	t := reflect.TypeFor[HomeRow]()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.IsExported() && field.Name != "Children" {
			fields = append(fields, field)
		}
	}
	child, err := apikit.Schema(reflect.StructOf(fields))
	if err != nil {
		panic(err)
	}
	childProperties := child["properties"].(map[string]any)
	childProperties["entries"].(map[string]any)["maxItems"] = 200
	childProperties["whyTitles"].(map[string]any)["maxItems"] = 3
	childProperties["displayTargets"].(map[string]any)["maxItems"] = HomeRowMaxLimit
	childProperties["deferred"] = map[string]any{"type": "boolean", "enum": []bool{false}}
	properties := map[string]any{}
	for key, value := range childProperties {
		properties[key] = value
	}
	properties["deferred"] = map[string]any{"type": "boolean"}
	properties["children"] = map[string]any{"type": "array", "maxItems": recPersonalRows, "items": child}
	out := map[string]any{}
	for key, value := range child {
		out[key] = value
	}
	out["properties"] = properties
	return out
}

// These are wire bounds enforced by Home composition, not renderer guesses.
func (HomeLive) JSONSchema() map[string]any {
	type wire HomeLive
	schema, err := apikit.Schema(reflect.TypeFor[wire]())
	if err != nil {
		panic(err)
	}
	p := schema["properties"].(map[string]any)
	p["sort"] = map[string]any{"type": "string", "enum": []string{"favorites"}}
	p["channels"].(map[string]any)["maxItems"] = 50
	p["groups"].(map[string]any)["maxItems"] = 2000
	return schema
}
func (HomeCollection) JSONSchema() map[string]any {
	type wire HomeCollection
	schema, err := apikit.Schema(reflect.TypeFor[wire]())
	if err != nil {
		panic(err)
	}
	schema["properties"].(map[string]any)["members"].(map[string]any)["maxItems"] = 4
	return schema
}
