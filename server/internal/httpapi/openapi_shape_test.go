package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func TestGeneratedOpenAPIReadBodiesRequiredListsAndRecommendationQueries(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	components, wire, err := PublishedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	generated, err := apikit.GenerateWith(registry.Definitions(), apikit.Options{Errors: registry.ErrorDefinitions(), Components: components, WireSchemas: wire, GrandfatheredUnionTags: GrandfatheredUnionTags})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(generated.OpenAPI, &document); err != nil {
		t.Fatal(err)
	}
	// GenerateWith already refused an invalid document (apikit.CheckOpenAPI);
	// what follows pins the shapes F15 fixed.
	paths, ok := document["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("generated document has no paths")
	}
	for path, raw := range paths {
		operations := raw.(map[string]any)
		for _, method := range []string{"get", "head"} {
			if operation, ok := operations[method].(map[string]any); ok {
				if _, present := operation["requestBody"]; present {
					t.Fatalf("%s %s declares a request body", method, path)
				}
			}
		}
	}
	for _, path := range []string{"/v1/suggestions", "/v1/items/{id}/recommendations"} {
		operation := paths[path].(map[string]any)["get"].(map[string]any)
		parameters, _ := operation["parameters"].([]any)
		var limit map[string]any
		for _, raw := range parameters {
			parameter := raw.(map[string]any)
			if parameter["in"] == "query" && parameter["name"] == "limit" {
				limit, _ = parameter["schema"].(map[string]any)
			}
		}
		// homeQueryLimit: absent means 12, below 1 is refused, above 60 is 60.
		if limit == nil || len(limit["anyOf"].([]any)) != 2 || limit["anyOf"].([]any)[0].(map[string]any)["type"] != "integer" || limit["x-empty-is-default"] != true || limit["minimum"] != float64(1) || limit["default"] != float64(catalog.HomeRowDefaultLimit) || limit["x-clamp-maximum"] != float64(catalog.HomeRowMaxLimit) || limit["maximum"] != nil {
			t.Fatalf("%s limit schema: %#v", path, limit)
		}
	}
}
