package catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// PERF-13: a page of episodes must cost a constant number of statements,
// not one Get (three statements) per row.
func TestEpisodesPageIssuesConstantStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "episodes.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	c := catalogtest.New(t, db)
	tv := c.Library("tv", "Shows", "tv", "/tv")
	show := c.Show(tv, "Harbour", 2001)
	season := c.Season(show, 1)
	const total = 100
	for i := 1; i <= total; i++ {
		c.Episode(show, season, i, fmt.Sprintf("/x%03d.mkv", i))
	}
	c.Drain()

	viewer := Viewer{Libraries: []string{"tv"}}
	rows, next, err := s.Episodes(viewer, "", season.Public, "", total)
	if err != nil {
		t.Fatalf("episodes: %v", err)
	}
	if len(rows) != total {
		t.Fatalf("got %d of %d episodes (next %q)", len(rows), total, next)
	}
	for i, row := range rows {
		if row.Episode == nil || row.Episode.Number != i+1 {
			t.Fatalf("row %d out of order or missing detail: %+v", i, row.Episode)
		}
		if len(row.Sources) != 1 {
			t.Fatalf("row %d: got %d sources, want 1", i, len(row.Sources))
		}
	}

	ctx, cost := dbwork.Measure(context.Background())
	if _, _, err = s.WithContext(ctx).Episodes(viewer, "", season.Public, "", total); err != nil {
		t.Fatal(err)
	}
	statements := cost().Statements
	t.Logf("a 100-episode season page cost %d statements", statements)
	if statements > 12 {
		t.Fatalf("a 100-episode season page cost %d statements; the per-row reads are not batched", statements)
	}

	// The cost must not grow with the page: a 10-episode page costs the same
	// shape, so the 100-episode page is constant, not linear.
	ctx10, cost10 := dbwork.Measure(context.Background())
	if _, _, err = s.WithContext(ctx10).Episodes(viewer, "", season.Public, "", 10); err != nil {
		t.Fatal(err)
	}
	small := cost10().Statements
	t.Logf("a 10-episode season page cost %d statements", small)
	if statements > small+4 {
		t.Fatalf("100 episodes cost %d statements vs %d for 10; cost grows with the page", statements, small)
	}
}
