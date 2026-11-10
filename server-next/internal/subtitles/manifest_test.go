package subtitles

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const plainSubtitle = "1\n00:00:00,000 --> 00:00:02,000\nHello\n"

func TestManifestWindowsRetainCrossingCuesOffsetsAndEmptyWindows(t *testing.T) {
	raw, _ := json.Marshal(Document{Version: 1, TimeDomain: "source", Cues: []TextCue{{StartUS: "59000000", EndUS: "61000000", Text: "<word>", Italic: true, Top: true}}})
	for _, window := range []int{0, 1} {
		v, e := ManifestWebVTT(bytes.NewReader(raw), 500000, window)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(v), "00:00:59.500 --> 00:01:01.500 line:10%") || !strings.Contains(string(v), "<i>&lt;word&gt;</i>") {
			t.Fatal(string(v))
		}
	}
	v, e := ManifestWebVTT(bytes.NewReader(raw), 0, 2)
	if e != nil || strings.Contains(string(v), "-->") || !strings.Contains(string(v), "X-TIMESTAMP-MAP") {
		t.Fatal(string(v), e)
	}
}

func TestTolerantTextDocumentReportsSkippedCues(t *testing.T) {
	raw, e := CanonicalAsset([]byte(plainSubtitle+"\n2\nbad --> timing\nBad\n"), nil, "srt", 60000000)
	if e != nil {
		t.Fatal(e)
	}
	var doc Document
	if e = json.Unmarshal(raw, &doc); e != nil || doc.SkippedCues != 1 || len(doc.Cues) != 1 {
		t.Fatal(doc, e)
	}
}
