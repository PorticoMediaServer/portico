package naming

import (
	"strings"
	"testing"
)

func TestBuiltinIgnoreRules(t *testing.T) {
	for _, p := range []string{"._Film.mkv", ".Film.mkv", ".hidden/Film.mkv", "@eaDir/thumbnail.mp4", "#recycle/Film.mkv", "$RECYCLE.BIN/Film.mkv", "lost+found/Film.mkv", "Film/.grab/Film.mkv", "Film/Samples/sample.mkv", "Film/Sample/sample.mkv", "Film/Film-sample.mkv", "Film/Film.sample-GROUP.mkv", "Film/Film - sample.mkv", "Film/Film.mkv.grab", "Film/Film.sample.mp4", "Film/sample-encode.mkv", "Film/.plexignore", "Film/.ignore"} {
		if !IgnorePath(p, false) {
			t.Errorf("not ignored: %s", p)
		}
	}
	for _, p := range []string{"A Sample Movie (2020).mkv", "Sample (2020)/Sample (2020).mkv", "Sample.mkvx/Film.mkv", "Samples of Life/Film.mkv", "Film/Film.mkv", "Trailers/Film.mkv", "Extras/Season 1/Extras S01E01.mkv"} {
		if IgnorePath(p, false) {
			t.Errorf("title ignored: %s", p)
		}
	}
	if !IgnoreName(".grab", true) || !IgnoreName("Samples", true) {
		t.Fatal("directory rules")
	}
}
func TestPlexIgnorePatternCorpus(t *testing.T) {
	rows := []struct {
		scope, contents, p string
		root, dir, match   bool
	}{
		{"", "# comment\n\n*trailer*\n*.cr2\n*Modified/*\n*thumbnails/*.tif", "Film/Film-trailer.mkv", true, false, true},
		{"", "*.cr2", "nested/photo.cr2", true, false, true},
		{"", "*Modified/*", "Film/Modified", true, true, true},
		{"", "*Modified/*", "Film/Modified/file.mkv", true, false, true},
		{"", "*thumbnails/*.tif", "Film/thumbnails/image.tif", true, false, true},
		{"", "*.mkv", "Film/file.mp4", true, false, false},
		{"Film", "*.mkv", "Film/file.mkv", false, false, true},
		{"Film", "*.mkv", "Film/nested/file.mkv", false, false, false},
		{"Film", "nested/*.mkv", "Film/nested/deeper/file.mkv", false, false, true},
		{"Film", "nested/*", "Film/nested", false, true, true},
		{"Film", "nested/", "Film/nested/file.mkv", false, false, true},
		{"Film", "nested/", "Film/other/file.mkv", false, false, false},
		{"Film", "*.mkv", "Film2/file.mkv", false, false, false},
		{"", "episode[0-9]?.mkv", "episode12.mkv", true, false, true},
		{"", "episode[!0-9].mkv", "episodeA.mkv", true, false, true},
		{"", "episode[!0-9].mkv", "episode1.mkv", true, false, false},
		{"", "!keep.mkv", "keep.mkv", true, false, false},
		{"", "!keep.mkv", "!keep.mkv", true, false, true},
		{"", "\ufeff*.mkv\r\n#ignored\r\n", "Film/file.mkv", true, false, true},
	}
	for _, row := range rows {
		r, d := ParseIgnore(row.scope, row.contents, row.root)
		if len(d) > 0 {
			t.Fatal(d)
		}
		if r.Match(row.p, row.dir) != row.match {
			t.Errorf("%q %q %q dir=%v", row.scope, row.contents, row.p, row.dir)
		}
	}
}
func TestIgnoreBudgetsKeepValidLines(t *testing.T) {
	r, d := ParseIgnore("", "*.mp4\n[broken\n"+strings.Repeat("x", MaxIgnorePatternBytes+1)+"\n*.mkv", true)
	if len(d) != 2 || d[0].Line != 2 || d[1].Line != 3 || !r.Match("Film/film.mp4", false) || !r.Match("film.mkv", false) {
		t.Fatal(d, r)
	}
	r, d = ParseIgnore("", "*.mkv\n"+strings.Repeat("x", MaxIgnoreBytes), true)
	if len(d) == 0 || !r.Match("film.mkv", false) {
		t.Fatal(d)
	}
	_, d = ParseIgnore("", strings.Repeat("a\n", MaxIgnorePatterns+1), true)
	if len(d) != 1 || d[0].Reason != "ignore_pattern_count_exceeded" {
		t.Fatal(d)
	}
}
func FuzzIgnore(f *testing.F) {
	f.Add("*.mkv\n*Modified/*", "Film/file.mkv")
	f.Add("[broken\n*.mp4", "Film/file.mp4")
	f.Fuzz(func(t *testing.T, patterns, p string) { r, _ := ParseIgnore("", patterns, true); r.Match(p, false) })
}
