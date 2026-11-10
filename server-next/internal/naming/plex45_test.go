package naming

import "testing"

// These are generated/blank-tree naming vectors, never real films or evidence
// that a codec or media source was probed or played.
func TestPlex45DateEpisodesKeepCanonicalAndDayFirstSeparators(t *testing.T) {
	for _, date := range []string{"2011-11-15", "2011.11.15", "2011 11 15", "15-11-2011", "15.11.2011", "15 11 2011"} {
		t.Run(date, func(t *testing.T) {
			path := "Show (2011)/Season 01/Show - " + date + " - info.mkv"
			r := Read(path, "tv")
			if len(r.AirDates) != 1 || r.AirDates[0].Value != "2011-11-15" || r.AirDates[0].Source != FileName || r.AirDates[0].Path != path || len(r.Diagnostics) != 0 {
				t.Fatalf("date evidence %+v", r)
			}
		})
	}
	for _, date := range []string{"31-11-2011", "29.02.2011", "00 11 2011"} {
		r := Read("Show (2011)/Show - "+date+".mkv", "tv")
		if len(r.AirDates) != 0 || len(r.Diagnostics) == 0 || r.Diagnostics[0] != "invalid_air_date" {
			t.Fatalf("invalid date became an episode %+v", r)
		}
	}
}

func TestPlex45SplitMovieTokensIncludeDVDWithoutChangingTitleEvidence(t *testing.T) {
	for _, token := range []string{"pt", "part", "cd", "disc", "disk", "dvd"} {
		for _, part := range []int{1, 2, 8} {
			suffix := string(rune('0' + part))
			path := "The Dark Knight (2008)/The Dark Knight (2008) - " + token + suffix + ".mkv"
			r := Read(path, "movie")
			title, year := r.PreferredTitle()
			if title != "The Dark Knight" || year != 2008 || len(r.Parts) == 0 || r.Parts[0].Value != part || r.Parts[0].Source != FileName {
				t.Fatalf("split evidence %q %+v", path, r)
			}
		}
	}
}

func TestPlex45MovieExamplesRetainIDsEditionsVersionsAndExtras(t *testing.T) {
	cases := []struct {
		path, title string
		year        int
	}{
		{"Avatar (2009)/Avatar (2009).mkv", "Avatar", 2009},
		{"Avatar (2009).mkv", "Avatar", 2009},
		{"Pulp Fiction (1994)/Pulp Fiction (1994) - 1080p.mkv", "Pulp Fiction", 1994},
		{"Pulp Fiction (1994)/Pulp Fiction (1994) - SD.mkv", "Pulp Fiction", 1994},
		{"Batman Begins (2005) {imdb-tt0372784}/Batman Begins (2005).mkv", "Batman Begins", 2005},
		{"Blade Runner (1982) {edition-Director's Cut}/Blade Runner (1982) - 1080p.mkv", "Blade Runner", 1982},
	}
	for _, test := range cases {
		t.Run(test.path, func(t *testing.T) {
			r := Read(test.path, "movie")
			title, year := r.PreferredTitle()
			if title != test.title || year != test.year {
				t.Fatalf("movie evidence %+v", r)
			}
		})
	}
	r := Read(cases[4].path, "movie")
	found := false
	for _, id := range r.ProviderIDs {
		found = found || id.Value.Provider == "imdb" && id.Value.ID == "tt0372784"
	}
	if !found {
		t.Fatal("folder IMDb hint lost", r)
	}
	r = Read(cases[5].path, "movie")
	if len(r.Editions) == 0 || r.Editions[0].Value != "Director's Cut" {
		t.Fatal("edition lost", r)
	}
	for _, path := range []string{"Avatar (2009)/Avatar (2009)-trailer.mkv", "Avatar (2009)/Behind The Scenes/Interview.mkv", "Game of Thrones (2011)/Trailers/Trailer.mkv"} {
		kind := "movie"
		if path[0] == 'G' {
			kind = "tv"
		}
		r := Read(path, kind)
		if len(r.Extras) == 0 {
			t.Fatalf("existing extra lost %q %+v", path, r)
		}
	}
}
