package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
)

// R-09b: a response union tags every alternative with `variant`, in the bytes
// as well as in the schema.
func TestVariantBodyLeadsWithItsTag(t *testing.T) {
	raw, err := json.Marshal(variantBody{"ready", struct {
		A int `json:"a"`
	}{1}})
	if err != nil || string(raw) != `{"variant":"ready","a":1}` {
		t.Fatalf("%s %v", raw, err)
	}
	raw, err = json.Marshal(variantBody{"x", struct{}{}})
	if err != nil || string(raw) != `{"variant":"x"}` {
		t.Fatalf("%s %v", raw, err)
	}
	if _, err = json.Marshal(variantBody{"x", []int{1}}); err == nil {
		t.Fatal("a non-object body was tagged")
	}
}

func TestAnalysisControlResponseUnionIsTagged(t *testing.T) {
	shape := (AnalysisControlResponse{}).JSONSchema()
	encoded, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"variant":"acknowledgement","status":"pause"}`, `{"variant":"job","id":"j1","status":"queued","processed":0,"analyzed":0,"warnings":0}`} {
		if problems := (apispec.Document{}).ValidateJSON(shape, []byte(body)); len(problems) != 0 {
			t.Errorf("%s: %v", body, problems)
		}
	}
	if problems := (apispec.Document{}).ValidateJSON(shape, []byte(`{"status":"pause"}`)); len(problems) == 0 {
		t.Error("an untagged acknowledgement was accepted")
	}
}

func TestGenreAndFacetUnionsAreTagged(t *testing.T) {
	for name, shape := range map[string]map[string]any{
		"genres": (genreReadResponse{}).JSONSchema(), "facets": (facetReadResponse{}).JSONSchema(),
		"facet query": (facetQueryResponse{}).JSONSchema(), "reading facets": (ReadingFacetQueryResponse{}).JSONSchema(),
	} {
		seen := map[string]bool{}
		for _, alternative := range shape["anyOf"].([]any) {
			properties := alternative.(map[string]any)["properties"].(map[string]any)
			tag, ok := properties["variant"].(map[string]any)
			if !ok || tag["const"] == nil {
				t.Errorf("%s: an alternative has no variant const", name)
				continue
			}
			value := tag["const"].(string)
			if seen[value] {
				t.Errorf("%s: variant %q repeats", name, value)
			}
			seen[value] = true
		}
	}
}
