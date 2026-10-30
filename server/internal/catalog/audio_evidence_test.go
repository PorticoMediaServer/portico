package catalog

import (
	"encoding/json"
	"testing"
)

func TestLocalBookProjectionHasBoundedNonNullCollections(t *testing.T) {
	for _, tags := range []map[string]string{{}, {"series_list": "null", "identifiers": "null"}, {"series_list": `[{"name":"Bad","position":"\u0000"}]`, "identifiers": `[{"scheme":"isbn","value":"\u0000"}]`}} {
		v := bookLocalMetadata(tags, nil)
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		for _, key := range []string{"authors", "narrators", "series", "identifiers"} {
			if _, ok := out[key].([]any); !ok {
				t.Fatal("invalid client collection", key, string(raw))
			}
		}
	}
}
func TestLocalBookArtistFallbackAndIndependentEditionMetadata(t *testing.T) {
	v := bookLocalMetadata(map[string]string{"artist": "Author", "narrators": `["Reader One","Reader Two"]`, "edition": "Unabridged", "series_list": `[{"name":"Series","position":"2.5"}]`, "isbn": "9780000000002"}, map[string]string{"artist": "embedded", "narrators": "opf"})
	if len(v.Authors) != 1 || v.Authors[0] != "Author" || len(v.Narrators) != 2 || v.Edition != "Unabridged" || len(v.Series) != 1 || v.Series[0].Position != "2.5" || len(v.Identifiers) != 1 || v.Sources["authors"] != "embedded" {
		t.Fatal(v)
	}
}

func TestStructuredGenreNamesKeepPunctuation(t *testing.T) {
	got := normalizeAudioGenres(`["Singer/Songwriter","Rock","Rock"]`)
	if len(got) != 2 || got[0] != "Singer/Songwriter" {
		t.Fatal(got)
	}
}
