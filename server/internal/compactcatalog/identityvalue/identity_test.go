package identityvalue

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

func TestLegacyIdentityValueCorpus(t *testing.T) {
	raw, err := os.ReadFile("../../naming/testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Path, Kind, Title string
		Year, Part        int
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 600 {
		t.Fatal("missing existing filename corpus", len(cases))
	}
	root := filepath.Join(string(filepath.Separator), "library")
	comparisons := 0
	for _, c := range cases {
		// Compare the stored corpus values AND the actual preferred filename
		// output. No normalization of expected/result bytes is permitted.
		title, year := naming.Read(c.Path, c.Kind).PreferredTitle()
		for _, value := range []string{c.Path, c.Title, title} {
			if got, want := IdentityText(value), legacyIdentityText(value); got != want {
				t.Fatalf("IdentityText(%q)=%q, legacy=%q", value, got, want)
			}
			comparisons++
		}
		for _, v := range []struct {
			title string
			year  int
		}{{c.Title, c.Year}, {title, year}} {
			if got, want := MovieKey(v.title, v.year), legacyMovieKey(v.title, v.year); got != want {
				t.Fatalf("MovieKey(%q,%d)=%q, legacy=%q", v.title, v.year, got, want)
			}
			comparisons++
		}
		path := filepath.Join(root, filepath.FromSlash(c.Path))
		if got, want := ItemKey(root, path, c.Part), legacyItemKey(root, path, c.Part); got != want {
			t.Fatalf("ItemKey(%q,%q,%d)=%q, legacy=%q", root, path, c.Part, got, want)
		}
		comparisons++
	}
	t.Logf("corpus=%x rows=%d exact-byte comparisons=%d", sha256.Sum256(raw), len(cases), comparisons)
}

func TestLegacyIdentityValueEdges(t *testing.T) {
	for _, name := range []string{"", "WALL·E", "Wall-E", "Schindler's List", "Schindler’s List", "Amélie", "Ame\u0301lie", "İstanbul", "!!!", "\xff\x00"} {
		if got, want := IdentityText(name), legacyIdentityText(name); got != want {
			t.Fatalf("IdentityText(%q)=%q, legacy=%q", name, got, want)
		}
		if got, want := MovieKey(name, -1), legacyMovieKey(name, -1); got != want {
			t.Fatalf("MovieKey(%q)=%q, legacy=%q", name, got, want)
		}
	}
	for _, c := range []struct {
		root, path string
		part       int
	}{
		{"", "Film.mkv", 0}, {"/media", "/media/Film.mkv", 0}, {"/media", "/media/Film.mkv", 1},
		{"/media", "/other/Film.mkv", 0}, {"/media", "/media/../Film.mkv", -1},
		{"/media", "/media/..name/Film.mkv", 0}, {`C:\Movies`, `C:\Movies\Film.mkv`, 2},
		{"/media", "/media/\xff\x00.mkv", 0},
	} {
		if got, want := ItemKey(c.root, c.path, c.part), legacyItemKey(c.root, c.path, c.part); got != want {
			t.Fatalf("ItemKey(%q,%q,%d)=%q, legacy=%q", c.root, c.path, c.part, got, want)
		}
	}
}
