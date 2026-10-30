package catalog

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

func (SearchGenreArt) JSONSchema() map[string]any {
	type reflected SearchGenreArt
	schema, err := apikit.Schema(reflect.TypeFor[reflected]())
	if err != nil {
		panic(err)
	}
	schema["properties"].(map[string]any)["kind"] = map[string]any{"type": "string", "enum": []string{"item", "show"}}
	return schema
}

func (SearchGenre) JSONSchema() map[string]any {
	type reflected SearchGenre
	schema, err := apikit.Schema(reflect.TypeFor[reflected]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	properties["count"].(map[string]any)["minimum"] = 1
	properties["covers"].(map[string]any)["maxItems"] = 2
	return schema
}
