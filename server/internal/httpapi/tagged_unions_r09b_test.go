package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// The get_file_info_query union mixes the Get/Detail documents, whose kind is
// domain data, with the pages, so every alternative carries a distinct const
// variant (R-09b).
func TestFileInfoQueryUnionIsTaggedByVariant(t *testing.T) {
	alternatives := fileInfoQueryResponse{}.JSONSchemaForStatus(200)["anyOf"].([]any)
	alternatives = append(alternatives, fileInfoQueryResponse{}.JSONSchemaForStatus(202))
	seen := map[string]bool{}
	for _, alternative := range alternatives {
		shape := alternative.(map[string]any)
		tag := shape["properties"].(map[string]any)["variant"].(map[string]any)["const"].(string)
		if seen[tag] || !requiredName(shape, "variant") {
			t.Fatalf("variant %q is repeated or not required", tag)
		}
		seen[tag] = true
	}
	if len(seen) != len(alternatives) || !seen["detail"] || !seen["item"] || !seen["files"] || !seen["preparing"] {
		t.Fatalf("variants: %v", seen)
	}
}

func requiredName(shape map[string]any, name string) bool {
	for _, key := range shape["required"].([]string) {
		if key == name {
			return true
		}
	}
	return false
}

func TestFileInfoBodiesCarryTheirVariant(t *testing.T) {
	raw, err := json.Marshal(fileInfoKindVariant{catalog.FileInfoFilesPage{Kind: "file-info-files"}})
	if err != nil {
		t.Fatal(err)
	}
	var page struct{ Variant, Kind string }
	if err = json.Unmarshal(raw, &page); err != nil || page.Variant != "files" || page.Kind != "file-info-files" {
		t.Fatalf("page %s %v", raw, err)
	}
	for _, c := range []struct {
		body any
		want string
	}{
		{catalog.Item{ID: "i", Duration: 1}, "item"},
		{catalog.Item{ID: "i", Duration: 1e300}, "item"},
	} {
		raw, err = json.Marshal(fileInfoPolledBody{variant: "item", body: c.body})
		var tag struct{ Variant string }
		if err != nil || json.Unmarshal(raw, &tag) != nil || tag.Variant != c.want {
			t.Fatalf("item %s %v", raw, err)
		}
	}
}

// A tag the client does not know is not guessed at; the generated runtime
// reads it as an unknown variant (apikit tests). Here: the schema the runtime
// is generated from names the tag on every response union this lane converted.
func TestConvertedUnionsAreTagged(t *testing.T) {
	for name, schema := range map[string]map[string]any{
		"invitation accept": InvitationAcceptanceResponse{}.JSONSchema(),
		"owner reset":       ownerResetResponse{}.JSONSchema(),
	} {
		var alternatives []any
		for _, key := range []string{"anyOf", "oneOf"} {
			if list, ok := schema[key].([]any); ok {
				alternatives = list
			}
		}
		seen := map[string]bool{}
		for _, a := range alternatives {
			shape := a.(map[string]any)
			tag := shape["properties"].(map[string]any)["variant"].(map[string]any)["const"].(string)
			if seen[tag] || !requiredName(shape, "variant") {
				t.Fatalf("%s: variant %q repeated or not required", name, tag)
			}
			seen[tag] = true
		}
		if len(seen) != len(alternatives) || len(seen) < 2 {
			t.Fatalf("%s: %v", name, seen)
		}
	}
}
