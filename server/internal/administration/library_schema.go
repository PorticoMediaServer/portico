package administration

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

// LibrarySettings publishes the same closed vocabulary its write validator
// accepts. Generated clients cannot silently turn unknown policy into a default.
func (LibrarySettings) JSONSchema() map[string]any {
	type wire LibrarySettings
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	props := shape["properties"].(map[string]any)
	for key, values := range map[string][]string{
		"scanDepth": {"file_list_only", "basic", "complete", "custom"},
		"localTags": {"prefer", "supplement", "off"}, "lyricsSource": {"files", "lrclib"},
		"titleSource": {"tags", "names"}, "episodeNumbering": {"season", "absolute"},
		"refreshMode": {"replace_unlocked", "fill_missing"}, "segmentDetection": {"server", "off", "standard", "thorough"},
	} {
		props[key].(map[string]any)["enum"] = values
	}
	analysis := props["analysis"].(map[string]any)
	analysis["uniqueItems"] = true
	analysis["maxItems"] = len(analysisRuntime)
	ids := []string{}
	for _, op := range AnalysisOperations() {
		ids = append(ids, op.ID)
	}
	analysis["items"].(map[string]any)["enum"] = ids
	return shape
}
