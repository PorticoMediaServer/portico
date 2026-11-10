package naming

import "testing"

// Release tags describe an encode and belong after the title. A film whose own
// title contains one of those words keeps its title and its year.
func TestFilmTitlesContainingReleaseWords(t *testing.T) {
	for _, row := range []struct {
		path, title string
		year        int
	}{
		{"8-Bit Christmas (2021)/8-Bit Christmas (2021).mkv", "8-Bit Christmas", 2021},
		{"Multi-Facial (1995)/Multi-Facial (1995).mkv", "Multi-Facial", 1995},
	} {
		t.Run(row.path, func(t *testing.T) {
			r := Read(row.path, "movie")
			title, year := r.PreferredTitle()
			if title != row.title || year != row.year {
				t.Fatalf("title/year = %q %d; want %q %d; reading %+v", title, year, row.title, row.year, r)
			}
			if len(r.Versions) != 0 {
				t.Fatalf("versions = %+v; want none: the title is not a release label", r.Versions)
			}
		})
	}
	// A real release tag after the title still becomes the version label.
	r := Read("Film (2020)/Film (2020) - 1080p.mkv", "movie")
	title, year := r.PreferredTitle()
	if title != "Film" || year != 2020 || len(r.Versions) != 1 || r.Versions[0].Value != "1080p" {
		t.Fatalf("a release tag after the title = %q %d %+v; want Film 2020 1080p", title, year, r.Versions)
	}
}
