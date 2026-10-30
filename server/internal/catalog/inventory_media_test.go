package catalog

import "testing"

// A disc image is one video title; audio and reading libraries keep rejecting
// it (VIDEO §3 VS-4a).
func TestDiscImagesAreVideoMedia(t *testing.T) {
	for _, kind := range []string{"movie", "tv", "anime", ""} {
		if ok, reason := InventoryMedia("/media/Film.iso", kind); !ok || reason != "" {
			t.Errorf("%q: %t %q", kind, ok, reason)
		}
	}
	for _, kind := range []string{"music", "audiobook", "book", "comic"} {
		if ok, reason := InventoryMedia("/media/Film.iso", kind); ok || reason != "" {
			t.Errorf("%q accepted a disc image: %t %q", kind, ok, reason)
		}
	}
	if ok, reason := InventoryMedia("/media/Film.mkv", "movie"); !ok || reason != "" {
		t.Fatal("ordinary video was refused", ok, reason)
	}
}
