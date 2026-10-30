package metadata

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestScreenFilenameEvidenceAndEdition(t *testing.T) {
	for _, tc := range []struct {
		raw, title string
		year       int
	}{
		{"[Group] A.Movie.2020.1080p.BluRay.x265.mkv", "A Movie", 2020},
		{"A_Show.S02E03.1080p.mkv", "A Show", 0},
		{"[Group] Anime - 014v2 [1080p].mkv", "Anime", 0},
	} {
		title, year, _ := screenFilename(tc.raw)
		if title != tc.title || year != tc.year {
			t.Fatal(tc.raw, title, year)
		}
	}
	title, year, edition := screenFilename("Film (2020) {edition-Extended Cut} 2160p.mkv")
	if title != "Film" || year != 2020 || edition != "Extended Cut" {
		t.Fatal(title, year, edition)
	}
	if normalizedScreenName("Amélie") != normalizedScreenName("Amelie") {
		t.Fatal("accent normalization")
	}
	for _, q := range []string{"/media/private/Film.mkv", "https://private.example/movie", "C:\\media\\Film", "a\nquery"} {
		if screenQuerySafe(q) {
			t.Fatal("path/query disclosure", q)
		}
	}
}
func TestScreenConfidenceRequiresVerifiedDetailsSignalsAndMargin(t *testing.T) {
	record := metadataprovider.ScreenRecord{Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "movie", ID: "1"}, Title: "Film", Year: 2020}
	top := scoreScreen(record, []string{"Film"}, 2020, false)
	if i, _ := screenWinner([]screenCandidate{top}); i != -1 {
		t.Fatal("search-only preview accepted")
	}
	top.DetailsVerified = true
	if i, _ := screenWinner([]screenCandidate{top}); i != 0 {
		t.Fatal("strong verified match rejected")
	}
	other := top
	other.Record.Identity.ID = "2"
	if i, _ := screenWinner([]screenCandidate{top, other}); i != -1 {
		t.Fatal("ambiguous remake accepted")
	}
	missing := scoreScreen(record, []string{"Film"}, 0, false)
	missing.DetailsVerified = true
	if i, _ := screenWinner([]screenCandidate{missing}); i != 0 {
		t.Fatal("yearless exact match rejected")
	}
	conflict := scoreScreen(record, []string{"Film"}, 2021, false)
	conflict.DetailsVerified = true
	if i, _ := screenWinner([]screenCandidate{conflict}); i != 0 {
		t.Fatal("one-year release difference rejected")
	}
	exact := scoreScreen(record, nil, 0, true)
	if i, _ := screenWinner([]screenCandidate{exact}); i != 0 {
		t.Fatal("validated exact ID rejected")
	}
}
func TestScreenLocalYearsAndNFOEpisodeCoordinates(t *testing.T) {
	if !screenLocalYearConflict(screenBase{Year: 2020, NFO: []assets.VideoNFO{{Year: 2021}}}) {
		t.Fatal("conflicting local facts hidden")
	}
	s, n := 2, 3
	b := screenBase{Numbering: "seasonal", Season: 2, Number: 3}
	if !screenNFOCoordinates(assets.VideoNFO{Season: &s, Episode: &n}, b) {
		t.Fatal("exact coordinate rejected")
	}
	s = 1
	if screenNFOCoordinates(assets.VideoNFO{Season: &s, Episode: &n}, b) {
		t.Fatal("different local season matched")
	}
}

func TestScreenNamesKeepAYearThatBelongsToTheTitle(t *testing.T) {
	for _, tc := range []struct {
		raw, title string
		year       int
	}{
		{"Blade Runner 2049 (2017).mkv", "Blade Runner 2049", 2017},
		{"1917 (2019).mkv", "1917", 2019},
		{"2001 A Space Odyssey (1968).mkv", "2001 A Space Odyssey", 1968},
		{"2001.A.Space.Odyssey.1968.1080p.BluRay.mkv", "2001 A Space Odyssey", 1968},
		{"Blade.Runner.2049.2017.1080p.BluRay.mkv", "Blade Runner 2049", 2017},
		{"1917.2019.1080p.mkv", "1917", 2019},
		// Bare years that belong to a title are preserved; strong release evidence is separate.
		{"Film.1999.Remastered.2019.mkv", "Film", 1999},
		{"Blade Runner 2049.mkv", "Blade Runner 2049", 0},
		{"1917.mkv", "1917", 0},
		// A bracketed year after release tags is an encode or group date.
		{"Movie.2019.1080p.[2020].mkv", "Movie", 2019},
		{"Movie (2019) 1080p [2020].mkv", "Movie", 2019},
		{"Movie (1999) (2019).mkv", "Movie", 1999},
		{"2001 A Space Odyssey (1968) 1080p [2020].mkv", "2001 A Space Odyssey", 1968},
		{"Blade Runner 2049 [2017] 1080p.mkv", "Blade Runner 2049", 2017},
	} {
		title, year, _ := screenFilename(tc.raw)
		if title != tc.title || year != tc.year {
			t.Errorf("%s: got %q %d, want %q %d", tc.raw, title, year, tc.title, tc.year)
		}
	}
	// The catalogue already holds the release year, so a number in its title is
	// part of the title: it is searched for and is not a second, conflicting year.
	for _, b := range []screenBase{
		{Title: "Blade Runner 2049", Year: 2017, Names: []string{"Blade Runner 2049 (2017).mkv"}},
		{Title: "1917", Year: 2019, Names: []string{"1917 (2019).mkv"}},
		{Title: "2001 A Space Odyssey", Year: 1968, Names: []string{"2001 A Space Odyssey (1968).mkv"}},
	} {
		names, year, _ := screenQueries(b)
		if len(names) != 1 || names[0] != b.Title || year != b.Year {
			t.Errorf("%s: queries %q year %d", b.Title, names, year)
		}
		if screenLocalYearConflict(b) {
			t.Errorf("%s: a number in the title was read as a conflicting release year", b.Title)
		}
	}
	// Without a catalogue year nothing is assumed about a bare number.
	if names, year, _ := screenQueries(screenBase{Title: "1917", Names: []string{"1917.mkv"}}); len(names) != 1 || names[0] != "1917" || year != 0 {
		t.Errorf("bare numeric name changed: %q %d", names, year)
	}
	if !screenLocalYearConflict(screenBase{Title: "Film", Year: 2020, Names: []string{"Film (2021).mkv"}}) {
		t.Error("a real disagreement between file names stopped being a conflict")
	}
}

func TestMatchingUsesNormalisedNamesAndRuntime(t *testing.T) {
	for _, pair := range [][2]string{{"The Office", "Office, The"}, {"Fast & Furious II", "Fast and Furious 2"}, {"Amélie", "Amelie"}, {"Москва", "Moskva"}} {
		if normalizedScreenName(pair[0]) != normalizedScreenName(pair[1]) {
			t.Errorf("normalisation: %q", pair)
		}
	}
	runtime := 120
	r := metadataprovider.ScreenRecord{Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "movie", ID: "1"}, Title: "Interstellar", Year: 2014, AdvisoryRuntimeMinutes: &runtime}
	c := scoreScreenEvidence(r, []string{"Interstllar"}, 0, false, screenBase{ItemKind: "movie", RuntimeMinutes: 121})
	c.DetailsVerified = true
	if winner, _ := screenWinner([]screenCandidate{c}); winner != 0 {
		t.Fatal("minor misspelling did not match", c)
	}
	wrong := scoreScreen(r, []string{"Unrelated"}, 2014, false)
	wrong.DetailsVerified = true
	if winner, _ := screenWinner([]screenCandidate{wrong}); winner >= 0 {
		t.Fatal("unrelated title accepted")
	}
	r.Year = 2000
	wrong = scoreScreen(r, []string{"Interstellar"}, 2014, false)
	wrong.DetailsVerified = true
	if winner, _ := screenWinner([]screenCandidate{wrong}); winner >= 0 {
		t.Fatal("distant release year accepted")
	}
}
