package subtitles

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"
	"unicode/utf16"
)

func TestCanonicalUnicodeAndLiteralEntities(t *testing.T) {
	text := "WEBVTT\n\n00:00.000 --> 00:02.000\n2 &lt; 3 &amp; café 🙂\n"
	units := utf16.Encode([]rune(text))
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		b := make([]byte, 2+len(units)*2)
		order.PutUint16(b, 0xfeff)
		for i, u := range units {
			order.PutUint16(b[2+i*2:], u)
		}
		raw, e := Canonical(b, "vtt", 60000000)
		if e != nil {
			t.Fatal(e)
		}
		var d Document
		if e = json.Unmarshal(raw, &d); e != nil || len(d.Cues) != 1 || d.Cues[0].Text != "2 < 3 & café 🙂" || d.Cues[0].EndUS != "2000000" {
			t.Fatal(d, e)
		}
	}
	for _, raw := range [][]byte{{0xff, 0xfe, 0, 0xd8}, {0xfe, 0xff, 0xdc, 0}, {0xff, 0xfe, 1}, {0xff}} {
		if _, e := DecodeText(raw); !errors.Is(e, ErrText) {
			t.Fatalf("invalid encoding accepted: %x %v", raw, e)
		}
	}
}
func TestCanonicalDurationAndOffsetBounds(t *testing.T) {
	if _, e := Canonical([]byte("1\n03:00:00,000 --> 03:00:01,000\nLong movie\n"), "srt", 4*60*60*1000000); e != nil {
		t.Fatal(e)
	}
	// A cue that runs past the end is cut off at it; a file whose every cue lies
	// beyond the end belongs to a different cut of the film and is refused.
	raw, e := Canonical([]byte("1\n00:00:01,000 --> 00:00:09,000\nRuns over\n"), "srt", 2000000)
	var clamped Document
	if e != nil || json.Unmarshal(raw, &clamped) != nil || len(clamped.Cues) != 1 || clamped.Cues[0].EndUS != "4000000" {
		t.Fatal(clamped, e)
	}
	if _, e := Canonical([]byte("1\n00:10:01,000 --> 00:10:05,000\nPast end\n"), "srt", 2000000); !errors.Is(e, ErrText) {
		t.Fatal(e)
	}
	for _, s := range []string{"0", "600000000", "-600000000", "500000"} {
		if _, e := Offset(s); e != nil {
			t.Fatal(s, e)
		}
	}
	for _, s := range []string{"-0", "01", "+1", "1.2", "600000001", "-600000001"} {
		if _, e := Offset(s); !errors.Is(e, ErrInput) {
			t.Fatal(s, e)
		}
	}
	if l, e := Language("fr-ca"); e != nil || l != "fr-CA" {
		t.Fatal(l, e)
	}
}
