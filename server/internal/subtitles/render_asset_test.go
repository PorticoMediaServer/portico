package subtitles

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestRichCanonicalRoutesAndRejectsUnsafeFeatures(t *testing.T) {
	// Marked-up SRT and WebVTT stay text: the words, and whether they were placed
	// at the top. They used to become burn-in assets, which re-encoded the film.
	for _, f := range []struct{ format, content, text string }{
		{"srt", "1\n00:00:01,000 --> 00:00:03,000\n{\\an8}<b>Top</b> &lt; literal\n", "Top < literal"},
		{"vtt", "WEBVTT\n\nSTYLE\n::cue { color: yellow; }\n::cue(.warning) { font-weight: bold; font-style: italic; }\n\n00:01.000 --> 00:03.000 line:10% position:20%,line-left size:60%\n<c.warning>Warning <u>nested</u></c>\n", "Warning nested"},
	} {
		raw, e := CanonicalAsset([]byte(f.content), nil, f.format, 10000000)
		if e != nil || assetRenderer(raw) != "external_text" {
			t.Fatalf("%s: %v %s", f.format, e, raw)
		}
		var d Document
		if json.Unmarshal(raw, &d) != nil || len(d.Cues) != 1 || d.Cues[0].Text != f.text || !d.Cues[0].Top {
			t.Fatalf("%s: %+v", f.format, d)
		}
	}
	for _, f := range []struct{ format, content, contains string }{
		{"ass", assHeader + "Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\pos(100,120)}Styled\n", `\pos(100,120)`},
	} {
		raw, e := CanonicalAsset([]byte(f.content), nil, f.format, 10000000)
		if e != nil {
			t.Fatalf("%s: %v", f.format, e)
		}
		a, e := DecodeRenderAsset(raw)
		if e != nil || !strings.Contains(string(a.Data), f.contains) {
			t.Fatalf("%s: %s %v", f.format, a.Data, e)
		}
	}
	if _, e := validateASSForTest(assHeader + "Dialogue: 0,0:99:00.00,0:00:03.00,Default,,0,0,0,,bad\n"); e == nil {
		t.Fatal("invalid ASS time")
	}
}
func validateASSForTest(text string) ([]byte, error) {
	return CanonicalAsset([]byte(text), nil, "ass", 10000000)
}
func TestPGSAdmissionBoundsWithoutOCR(t *testing.T) {
	raw := make([]byte, 24)
	copy(raw, "PG")
	raw[10] = 0x16
	binary.BigEndian.PutUint16(raw[11:13], 11)
	binary.BigEndian.PutUint16(raw[13:15], 1920)
	binary.BigEndian.PutUint16(raw[15:17], 1080)
	if e := validatePGS(raw); e != nil {
		t.Fatal(e)
	}
	binary.BigEndian.PutUint16(raw[13:15], 65535)
	if e := validatePGS(raw); e == nil {
		t.Fatal("unbounded canvas")
	}
	if _, e := CanonicalAsset([]byte("[Script Info]"), []byte("unexpected"), "ass", 0); e == nil {
		t.Fatal("unpaired companion")
	}
}
