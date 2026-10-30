package catalog

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

func (SimpleFacetPage) JSONSchema() map[string]any {
	type reflected SimpleFacetPage
	schema, err := apikit.Schema(reflect.TypeFor[reflected]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	properties["kind"] = map[string]any{"type": "string", "const": "simple-keyset"}
	properties["total"] = map[string]any{"type": "integer", "minimum": 0, "maximum": int64(9007199254740991)}
	properties["totalIsLowerBound"] = map[string]any{"type": "boolean", "const": false}
	properties["values"].(map[string]any)["maxItems"] = 200
	return schema
}

func (SimpleFacetWindow) JSONSchema() map[string]any {
	type reflected SimpleFacetWindow
	schema, err := apikit.Schema(reflect.TypeFor[reflected]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	for _, name := range []string{"queryId", "next", "previous", "reload"} {
		properties[name] = map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "pattern": "^[A-Za-z0-9_-]+$"}
	}
	properties["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 200}
	properties["returned"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 200}
	return schema
}

func (SimpleFacetPreparing) JSONSchema() map[string]any {
	type reflected SimpleFacetPreparing
	schema, err := apikit.Schema(reflect.TypeFor[reflected]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	properties["kind"] = map[string]any{"type": "string", "const": "facet-preparing"}
	properties["state"] = map[string]any{"type": "string", "const": "preparing"}
	properties["queryId"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "pattern": "^[A-Za-z0-9_-]+$"}
	properties["retryAfterMs"] = map[string]any{"type": "integer", "minimum": 1, "maximum": int64(9007199254740991)}
	return schema
}

func (SimpleFacetFallbackPage) JSONSchema() map[string]any {
	type reflected SimpleFacetFallbackPage
	schema, err := apikit.Schema(reflect.TypeFor[reflected]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	properties["kind"] = map[string]any{"type": "string", "const": "facet-fallback"}
	properties["total"] = map[string]any{"type": "integer", "minimum": 0, "maximum": int64(9007199254740991)}
	properties["totalIsLowerBound"] = map[string]any{"type": "boolean", "const": true}
	values := properties["values"].(map[string]any)
	values["maxItems"] = 200
	row := values["items"].(map[string]any)["properties"].(map[string]any)
	row["countIsLowerBound"] = map[string]any{"type": "boolean", "const": true}
	row["count"] = map[string]any{"type": "integer", "minimum": 0, "maximum": int64(9007199254740991)}
	return schema
}
