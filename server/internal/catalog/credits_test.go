package catalog

import (
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A title's credits are never cut off: the detail carries the cast's first
// page and the key crew (the director included, though billed last), and the
// credits read pages each group completely, in billing order.
func TestItemCreditsAreCompleteByCursor(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	film := c.Movie(films, "/m/f.mkv", "Film", 2020)
	credits := []compactcatalog.Credit{}
	for i := 0; i < 30; i++ {
		credits = append(credits, personCredit(fmt.Sprintf("tmdb:c%d", i), fmt.Sprintf("c%d", i), fmt.Sprintf("Actor %02d", i), "Character", "Acting", i))
	}
	for i := 0; i < 19; i++ {
		credits = append(credits, personCredit(fmt.Sprintf("tmdb:g%d", i), fmt.Sprintf("g%d", i), fmt.Sprintf("Grip %02d", i), "Grip", "Crew", 30+i))
	}
	credits = append(credits, personCredit("tmdb:d", "d", "The Director", "Director", "Directing", 49))
	compactCredits(c, film.ID, credits...)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	detail, err := s.Detail(viewer, "server", film.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Metadata.CreditTotals != (CreditTotals{Cast: 30, Crew: 20}) {
		t.Fatalf("totals %+v", detail.Metadata.CreditTotals)
	}
	cast, director := 0, false
	for _, credit := range detail.Metadata.Credits {
		if credit.Department == "Acting" {
			cast++
		}
		director = director || credit.Name == "The Director"
	}
	if cast != creditFirstCast || !director {
		t.Fatalf("detail credits: %d cast, director %v", cast, director)
	}
	for group, want := range map[string]int{"cast": 30, "crew": 20} {
		names, cursor := []string{}, ""
		for page := 0; page < 10; page++ {
			out, err := s.ItemCredits(viewer, film.Public, group, cursor, 7)
			if err != nil || out.Total != want {
				t.Fatalf("%s page %d: total %d %v", group, page, out.Total, err)
			}
			for _, credit := range out.Credits {
				names = append(names, credit.Name)
			}
			if cursor = out.NextCursor; cursor == "" {
				break
			}
		}
		if len(names) != want || group == "cast" && (names[0] != "Actor 00" || names[29] != "Actor 29") || group == "crew" && names[19] != "The Director" {
			t.Fatalf("%s pages: %d names %v", group, len(names), names)
		}
	}
	if _, err = s.ItemCredits(viewer, film.Public, "everyone", "", 0); err == nil {
		t.Fatal("an unknown group was accepted")
	}
}
