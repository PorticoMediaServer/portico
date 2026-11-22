package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A recording or daily show placed by its air date is sent with no season or episode number
// (apps label it by date, not "S2026 E326"); a numbered episode keeps its numbers.
func TestADatedEpisodeHasNoMadeUpNumbers(t *testing.T) {
	n := func(v int) *int { return &v }
	dated := db.Item{Kind: db.KindEpisode, ReleaseDate: "2026-11-22", ParentIndex: n(2026), IndexNumber: n(326)}
	if shaped := itemShape(dated); shaped.Index != nil || shaped.ParentIndex != nil {
		t.Fatalf("a recording of 22 Nov 2026 is S%v E%v", *shaped.ParentIndex, *shaped.Index)
	}
	numbered := db.Item{Kind: db.KindEpisode, ReleaseDate: "2026-11-22", ParentIndex: n(1), IndexNumber: n(3)}
	if shaped := itemShape(numbered); shaped.Index == nil || *shaped.Index != 3 || shaped.ParentIndex == nil || *shaped.ParentIndex != 1 {
		t.Fatalf("a numbered episode lost its numbers: %+v", shaped)
	}
	season := db.Item{Kind: db.KindSeason, ReleaseDate: "2026-11-22", IndexNumber: n(2026)}
	if shaped := itemShape(season); shaped.Index == nil || *shaped.Index != 2026 {
		t.Fatal("a year's season lost its number")
	}
}
