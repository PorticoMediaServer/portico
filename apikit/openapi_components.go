package apikit

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// referenceOpenAPIComponents factors only the detached OpenAPI document. SDK
// schemas, types and decoders retain their expanded shapes. Comparing the whole
// schema also preserves status narrowing, nullability and per-use constraints,
// including schemas supplied by providers rather than reflection.
func referenceOpenAPIComponents(document map[string]any, named map[string]reflect.Type) error {
	if len(named) == 0 {
		return nil
	}
	components := document["components"].(map[string]any)["schemas"].(map[string]any)
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	sort.Strings(names)
	refs := map[string]string{}
	for _, name := range names {
		raw, err := json.Marshal(components[name])
		if err != nil {
			return err
		}
		// Equal named shapes use a stable name, independent of map iteration.
		if _, exists := refs[string(raw)]; !exists {
			refs[string(raw)] = "#/components/schemas/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
		}
	}
	var schema func(any, bool) any
	schema = func(value any, root bool) any {
		switch node := value.(type) {
		case map[string]any:
			if !root {
				raw, _ := json.Marshal(node)
				if ref, ok := refs[string(raw)]; ok {
					return map[string]any{"$ref": ref}
				}
			}
			for _, key := range []string{"items", "additionalProperties", "not"} {
				if child, ok := node[key]; ok {
					node[key] = schema(child, false)
				}
			}
			for _, key := range []string{"anyOf", "oneOf", "allOf"} {
				if children, ok := node[key].([]any); ok {
					for i, child := range children {
						children[i] = schema(child, false)
					}
				}
			}
			if properties, ok := node["properties"].(map[string]any); ok {
				for key, child := range properties {
					properties[key] = schema(child, false)
				}
			}
		}
		return value
	}
	for name, value := range components {
		_, isNamed := named[name]
		// Keep named definitions expanded at the root, avoiding self references
		// and cycles between aliases of the same shape.
		components[name] = schema(value, isNamed)
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "schema" || key == "x-event-schema" {
					node[key] = schema(child, false)
				} else {
					walk(child)
				}
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(document["paths"])
	return nil
}
