package assets

import (
	"strings"
	"testing"
)

func TestEmbeddedVideoNamesKeepUsableFieldsWithinBounds(t *testing.T) {
	e, skipped := EmbeddedVideoName(map[string]string{"title": "Heat", "year": "1995", "edition": "Director's Cut", "tmdb": "949", "season_number": "2", "episode_sort": "0", "tv_show": "Show", "bad": strings.Repeat("x", 2000)})
	if !skipped || e.Title != "Heat" || e.Year != 1995 || e.Edition != "Director's Cut" || len(e.IDs) != 1 || e.IDs[0].ID != "949" || e.Number == nil || *e.Number != 0 {
		t.Fatalf("%+v skipped=%v", e, skipped)
	}
	e, skipped = EmbeddedVideoName(map[string]string{"title": "Still valid", "year": "wrong", "imdb": "file:///etc/passwd", "episode_sort": "100000"})
	if !skipped || e.Title != "Still valid" || e.Year != 0 || len(e.IDs) != 0 || e.Number != nil {
		t.Fatal(e, skipped)
	}
}
