package localmetadata

import (
	"fmt"
	"strings"
	"testing"
)

func TestVideoNFOTypedRootsAndEdition(t *testing.T) {
	for _, tc := range []struct {
		raw, kind, provider, typ string
		anime                    bool
	}{
		{`<movie><title>Film</title><year>2020</year><edition>Extended</edition><uniqueid type="tmdb">42</uniqueid></movie>`, "movie", "tmdb", "movie", false},
		{`<tvshow><title>Show</title><uniqueid type="tvdb">7</uniqueid></tvshow>`, "show", "tvdb", "show", false},
		{`<episodedetails><title>Episode</title><season>2</season><episode>1</episode><uniqueid type="tmdb">9</uniqueid></episodedetails>`, "episode", "tmdb", "episode", false},
		{`<tvshow><title>Anime</title><uniqueid type="anilist">1</uniqueid></tvshow>`, "show", "anilist", "anime", true},
	} {
		v, e := ParseVideoNFO([]byte(tc.raw), tc.anime)
		if e != nil || len(v) != 1 || v[0].Kind != tc.kind || len(v[0].IDs) != 1 || v[0].IDs[0].Type != tc.typ || v[0].IDs[0].Provider != tc.provider {
			t.Fatal(v, e)
		}
	}
}
func TestVideoNFONeverResolvesExternalEntitiesAndKeepsStructuralBudgets(t *testing.T) {
	for _, raw := range []string{`<!DOCTYPE movie [<!ENTITY x SYSTEM "file:///etc/passwd">]><movie><plot>&x;</plot></movie>`, strings.Repeat("x", 256*1024+1)} {
		if _, e := ParseVideoNFO([]byte(raw), true); e == nil {
			t.Fatal("accepted invalid NFO")
		}
	}
}
func TestVideoNFOMultiEpisodeCoordinatesRemainDistinct(t *testing.T) {
	v, e := ParseVideoNFO([]byte(`<episodedetails><title>One</title><season>1</season><episode>1</episode></episodedetails><episodedetails><title>Two</title><season>2</season><episode>1</episode></episodedetails>`), false)
	if e != nil || len(v) != 2 || *v[0].Season == *v[1].Season || v[0].Digest != v[1].Digest {
		t.Fatal(v, e)
	}
}

func TestNFOOptionalFailuresPreserveUsefulFields(t *testing.T) {
	raw := `<movie><title>Film & Friends</title><year>unknown</year><runtime>148 min</runtime><director>A Director</director><credits>A Writer</credits><tag>Space</tag><country>Canada</country><rating>8.5</rating><votes>1,234</votes><uniqueid type="tmdb">42</uniqueid></movie>https://example.invalid/scraper`
	v, e := ParseVideoNFO([]byte(raw), false)
	if e != nil || len(v) != 1 {
		t.Fatal(v, e)
	}
	n := v[0]
	if n.Title != "Film & Friends" || n.Year != 0 || n.AdvisoryRuntimeMinutes == nil || *n.AdvisoryRuntimeMinutes != 148 || len(n.Credits) != 2 || len(n.Tags) != 1 || len(n.Issues) == 0 || n.Rating == nil || n.Rating.Votes != 1234 || len(n.IDs) != 1 {
		t.Fatal(n)
	}
	raw = `<movie><title>Large cast</title>` + strings.Repeat(`<actor><name>Actor</name></actor>`, 120) + `</movie>`
	v, e = ParseVideoNFO([]byte(raw), false)
	if e != nil || len(v[0].Credits) != 100 || len(v[0].Issues) == 0 {
		t.Fatal(v, e)
	}
}

func TestNFOInvalidIdentifiersAreIsolatedAndConflictsRemainEvidence(t *testing.T) {
	v, e := ParseVideoNFO([]byte(`<movie><title>Film</title><uniqueid type="tmdb">bad</uniqueid><imdbid>tt0133093</imdbid></movie>`), false)
	if e != nil || len(v[0].IDs) != 1 || v[0].IDs[0].Provider != "imdb" || len(v[0].Issues) == 0 {
		t.Fatal(v, e)
	}
	v, e = ParseVideoNFO([]byte(`<movie><title>Film</title><uniqueid type="tmdb">1</uniqueid><uniqueid type="tmdb">2</uniqueid></movie>`), false)
	if e != nil || len(v[0].IDs) != 2 || len(v[0].Issues) == 0 {
		t.Fatal(v, e)
	}
}

func TestNFOURLOnlyAndXMLScraperHintsStayTyped(t *testing.T) {
	for _, raw := range []string{`https://www.themoviedb.org/movie/603-the-matrix`, `https://www.imdb.com/title/tt0133093/`, `<movie><title>The Matrix</title></movie>https://www.imdb.com/title/tt0133093/`} {
		v, e := ParseVideoNFO([]byte(raw), false)
		if e != nil || len(v) != 1 || len(v[0].IDs) != 1 {
			t.Fatal(v, e)
		}
	}
	if _, e := ParseVideoNFO([]byte(`https://example.invalid/private`), false); e == nil {
		t.Fatal("arbitrary scraper URL became metadata")
	}
}

func TestConflictingNFOIdentifiersHaveABoundedEvidenceBudget(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("<movie><title>Film</title>")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&raw, `<uniqueid type="tmdb">%d</uniqueid>`, i)
	}
	raw.WriteString("</movie>")
	v, e := ParseVideoNFO([]byte(raw.String()), false)
	if e != nil || len(v[0].IDs) != 16 || len(v[0].Issues) == 0 {
		t.Fatal(v, e)
	}
}
