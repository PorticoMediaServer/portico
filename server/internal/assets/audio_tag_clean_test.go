package assets

import "testing"

// CD-27: a tab or newline inside a tagged title, album or artist becomes a
// space, so no music page carries a control character; a description keeps
// its lines.
func TestTaggedNamesAreOneLine(t *testing.T) {
	f := Facts{}
	CopyEmbeddedAudioTags(&f, map[string]string{
		"title":       "Side A\tTrack 1",
		"album":       "Live\r\nat the Hall ",
		"artist":      "  The\n\nBand ",
		"description": "Line one\nLine two",
	})
	for field, want := range map[string]string{"title": "Side A Track 1", "album": "Live at the Hall", "artist": "The Band", "description": "Line one\nLine two"} {
		if f.Tags[field] != want {
			t.Fatalf("%s = %q, want %q", field, f.Tags[field], want)
		}
	}
	for _, v := range f.AudioEvidence {
		if v.Field != "description" && v.Value != CleanAudioTag(v.Field, v.Value) {
			t.Fatalf("evidence kept a control character: %+v", v)
		}
	}
}
