package scan

import "testing"

// A release's file name or a tool's leftovers in the title tag are not a title.
func TestWhatCountsAsATitleInsideAFile(t *testing.T) {
	for tag, want := range map[string]string{
		"Léon: The Professional":          "Léon: The Professional",
		"  Heat  ":                        "Heat",
		"Heat.1995.1080p.BluRay.x264-GRP": "",
		"heat_1995_remux":                 "",
		"":                                "",
		"Heat":                            "Heat",
	} {
		if got := embeddedTitle(tag); got != want {
			t.Errorf("embeddedTitle(%q) = %q; want %q", tag, got, want)
		}
	}
}
