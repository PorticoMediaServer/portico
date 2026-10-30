package assets

import "testing"

func TestDiscImageClassification(t *testing.T) {
	for path, want := range map[string]bool{
		"/media/Film.ISO": true,
		"/media/Film.iso": true,
		// ISO and IMG are the formats the bounded disc reader opens as one
		// title; .image is not one of them and must not be classified as media.
		"/media/disc.image":   false,
		"/media/disc.img":     true,
		"/media/Film.mkv":     false,
		"/media/Film.iso.txt": false,
		"/media/movie":        false,
	} {
		if got := DiscImage(path); got != want {
			t.Errorf("%s: got %t want %t", path, got, want)
		}
	}
}
