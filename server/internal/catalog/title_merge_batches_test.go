package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// A large show merges in bounded steps and ends exactly as a one-step merge
// would; the merged-away ids keep leading to the titles that stayed.
func TestShowMergeRunsInBatchesAndRedirectsOldIds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	from := c.Show(tv, "Show", 2001)
	into := c.Show(tv, "The Show", 2001)
	fromOne, fromTwo := c.Season(from, 1), c.Season(from, 2)
	intoOne := c.Season(into, 1)
	var absorbed catalogtest.Item
	for n := 1; n <= 2*showMergeBatch+30; n++ {
		e := c.Episode(from, fromOne, n, fmt.Sprintf("/tv/a/S01E%03d.mkv", n))
		if n == 5 {
			absorbed = e
		}
	}
	for n := 1; n <= 40; n++ {
		c.Episode(from, fromTwo, n, fmt.Sprintf("/tv/a/S02E%03d.mkv", n))
	}
	var kept catalogtest.Item
	for n := 1; n <= 10; n++ {
		e := c.Episode(into, intoOne, n, fmt.Sprintf("/tv/b/S01E%03d.mkv", n))
		if n == 5 {
			kept = e
		}
	}
	c.Drain()
	s := New(c.DB)
	fromID, intoID := c.Public(from.ID), c.Public(into.ID)
	absorbedID, keptID := c.Public(absorbed.ID), c.Public(kept.ID)
	if err := s.MergeTitles(context.Background(), fromID, intoID, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := c.DB.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM catalog_shows`); n != 1 {
		t.Fatalf("shows after merge = %d", n)
	}
	if n := count(`SELECT count(*) FROM catalog_seasons WHERE show_id=?`, into.ID); n != 2 {
		t.Fatalf("seasons after merge = %d", n)
	}
	if n := count(`SELECT count(*) FROM catalog_episodes WHERE show_id=?`, into.ID); n != 2*showMergeBatch+30+40 {
		t.Fatalf("episodes after merge = %d", n)
	}
	if n := count(`SELECT count(*) FROM catalog_episodes e JOIN catalog_seasons s ON s.entity_id=e.season_id WHERE e.show_id=? AND s.show_id<>e.show_id`, into.ID); n != 0 {
		t.Fatalf("%d episodes sit in a season of another show", n)
	}
	for old, want := range map[string]string{fromID: intoID, absorbedID: keptID} {
		id, err := entityid.Resolve(context.Background(), c.DB, old)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Public(id); got != want {
			t.Fatalf("merged-away id %s resolves to %s, want %s", old, got, want)
		}
		if got := s.SurvivingTitleID(context.Background(), old); got != want {
			t.Fatalf("request edge sends %s to %s, want %s", old, got, want)
		}
	}
	if got := s.SurvivingTitleID(context.Background(), intoID); got != intoID {
		t.Fatalf("a live id was rewritten to %s", got)
	}
	// Request bodies and queries resolve every id they carry in one call.
	got := s.SurvivingTitleIDs(context.Background(), []string{fromID, intoID, absorbedID, fromID, "not-an-id"})
	if len(got) != 2 || got[fromID] != intoID || got[absorbedID] != keptID {
		t.Fatalf("request ids resolve to %v", got)
	}
}
