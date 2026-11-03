package naming

import "testing"

// A film's own title may contain the words an edition label uses. An edition is
// evidence from the release tail or a {edition-…} tag, never a word cut out of
// the title itself.
func TestFilmTitlesContainingEditionWords(t *testing.T) {
	for _, row := range []struct {
		path, title string
		year        int
	}{
		{"The Final Cut (2004)/The Final Cut (2004).mkv", "The Final Cut", 2004},
		{"Final Cut (2022)/Final Cut (2022).mkv", "Final Cut", 2022},
		{"Extended Family (2018)/Extended Family (2018).mkv", "Extended Family", 2018},
	} {
		t.Run(row.path, func(t *testing.T) {
			r := Read(row.path, "movie")
			title, year := r.PreferredTitle()
			if title != row.title || year != row.year {
				t.Fatalf("title/year = %q %d; want %q %d (the title's own words are not an edition); reading %+v", title, year, row.title, row.year, r)
			}
			if len(r.Editions) != 0 {
				t.Fatalf("editions = %+v; want none: the title is not an edition", r.Editions)
			}
		})
	}
	// The real thing still reads as an edition.
	r := Read("Film (2020)/Film (2020) - Director's Cut.mkv", "movie")
	title, year := r.PreferredTitle()
	if title != "Film" || year != 2020 || len(r.Editions) != 1 || r.Editions[0].Value != "Director's Cut" {
		t.Fatalf("an edition label after the title = %q %d %+v; want Film 2020 Director's Cut", title, year, r.Editions)
	}
}

// The same words after the title are still an edition.
func TestEditionWordsInTheReleaseTail(t *testing.T) {
	for _, row := range []struct{ path, title, edition string }{
		{"Aliens.1986.Extended.1080p.BluRay.mkv", "Aliens", "Extended"},
		{"Payback (1999) Final Cut.mkv", "Payback", "Final Cut"},
		{"Blade Runner 2049 Directors Cut (2017)/Blade Runner 2049 Directors Cut (2017).mkv", "Blade Runner 2049", "Directors Cut"},
	} {
		t.Run(row.path, func(t *testing.T) {
			r := Read(row.path, "movie")
			title, _ := r.PreferredTitle()
			found := false
			for _, e := range r.Editions {
				found = found || e.Value == row.edition
			}
			if title != row.title || !found {
				t.Fatalf("title %q, editions %+v; want %q with edition %q", title, r.Editions, row.title, row.edition)
			}
		})
	}
}
