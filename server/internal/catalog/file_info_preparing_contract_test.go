package catalog

import (
	"encoding/json"
	"math"
	"testing"
)

func TestFileInfoPreparingEchoesDetailOnlyAfterOpening(t *testing.T) {
	original := FileInfoDetailCapture{Scope: DetailScope{"server", "library", "item", "original"}, Revision: ContentRevision{Catalog: 41, Viewer: 7}}
	for _, capture := range []*FileInfoDetailCapture{nil, &original} {
		raw, e := json.Marshal(FileInfoPreparing{Kind: "file-info-preparing", State: "preparing", QueryID: "private", RetryAfterMs: 1000, Detail: capture})
		if e != nil {
			t.Fatal(e)
		}
		var wire map[string]json.RawMessage
		if e = json.Unmarshal(raw, &wire); e != nil {
			t.Fatal(e)
		}
		detail, exists := wire["detail"]
		if capture == nil {
			if exists {
				t.Fatal("opening preparation invented accepted Detail", string(raw))
			}
			continue
		}
		if !exists {
			t.Fatal("lane preparation lost original Detail")
		}
		var got FileInfoDetailCapture
		if e = json.Unmarshal(detail, &got); e != nil || got != original {
			t.Fatal(got, e)
		}
	}
}

func TestFileInfoItemDurationUnrepresentableIsExplicit(t *testing.T) {
	for _, duration := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), 1e300} {
		raw, e := json.Marshal(Item{ID: "item", Duration: duration})
		if e != nil {
			t.Fatal(e)
		}
		var wire map[string]json.RawMessage
		if e = json.Unmarshal(raw, &wire); e != nil {
			t.Fatal(e)
		}
		if _, exists := wire["durationMs"]; exists || string(wire["durationState"]) != `"unrepresentable"` {
			t.Fatal(string(raw))
		}
	}
	raw, e := json.Marshal(Item{ID: "item", Duration: 1.2345})
	if e != nil {
		t.Fatal(e)
	}
	var wire map[string]json.RawMessage
	json.Unmarshal(raw, &wire)
	if string(wire["durationMs"]) != "1235" || wire["durationState"] != nil {
		t.Fatal(string(raw))
	}
}
