package httpapi

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCompatibilityNestedSourcesTraverseEveryPage(t *testing.T) {
	calls := 0
	sources := compatibilitySourcePages{first: []any{map[string]any{"Id": "first"}}, cursor: "next", fetch: func(cursor string) (map[string]any, error) {
		calls++
		if cursor != "next" {
			t.Fatalf("unexpected cursor %q", cursor)
		}
		return map[string]any{"mediaSources": []any{map[string]any{"id": "second", "durationSeconds": float64(2), "audioCodec": "aac", "video": map[string]any{"codec": "h264", "height": float64(1080)}}}}, nil
	}}
	var w bytes.Buffer
	if err := writeCompatibilityJSON(&w, map[string]any{"Items": []any{map[string]any{"Id": "item", "MediaSources": sources}}}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Items []struct {
			MediaSources []struct {
				Id                                                            string
				RunTimeTicks                                                  int64
				SupportsDirectPlay, SupportsDirectStream, SupportsTranscoding bool
				MediaStreams                                                  []struct {
					Type, Codec string
					Height      int
				}
			}
		}
	}
	if err := json.Unmarshal(w.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Items) != 1 || len(doc.Items[0].MediaSources) != 2 || calls != 1 {
		t.Fatalf("incomplete media facts: %s", &w)
	}
	second := doc.Items[0].MediaSources[1]
	if second.Id != "second" || second.RunTimeTicks != 20000000 || len(second.MediaStreams) != 2 {
		t.Fatalf("incorrect stored technical projection: %+v", second)
	}
	if second.SupportsDirectPlay || second.SupportsDirectStream || second.SupportsTranscoding {
		t.Fatal("read-only media facts advertised a player capability")
	}
}
