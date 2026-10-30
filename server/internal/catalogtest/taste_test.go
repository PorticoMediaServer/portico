package catalogtest

import (
	"os"
	"testing"
	"time"
)

func TestTaste(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := Open(t)
	taste := BuildTaste(t, c, TasteShape{Movies: 400, Shows: 20, EpisodesPerShow: 3, Anime: 3, Seed: 42})
	c.Drain()
	check := func(query string, args ...any) {
		t.Helper()
		var count int
		if err := c.DB.QueryRow(query, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("expected rows for %s", query)
		}
	}
	check(`SELECT count(*) FROM catalog_rec_facets WHERE entity_id=?`, taste.SciFiDirector[0].ID)
	check(`SELECT count(*) FROM catalog_rec_facets WHERE entity_id=?`, taste.CozyAnime[0].ID)
	check(`SELECT count(*) FROM catalog_collection_members WHERE collection_id=?`, taste.Franchise.ID)
	check(`SELECT count(*) FROM metadata_ratings WHERE item_id=?`, taste.HiddenGem.ID)
	check(`SELECT count(*) FROM catalog_external_ids WHERE entity_id=?`, taste.CozyAnime[0].ID)
	check(`SELECT count(*) FROM catalog_similar WHERE entity_id=?`, taste.SciFiDirector[0].ID)
	check(`SELECT count(*) FROM catalog_item_availability WHERE entity_id=? AND available=1`, taste.HiddenGem.ID)
	var available int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_item_availability a JOIN catalog_entities e ON e.id=a.entity_id WHERE a.available=1 AND e.kind IN(1,4)`).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 460 {
		t.Fatalf("available playable titles: got %d, want 460", available)
	}
	taste.Watch("taste-episode-check", taste.CozyAnime[1])
	check(`SELECT count(*) FROM rec_profile_jobs WHERE profile_id='taste-episode-check' AND work_id=?`, taste.CozyAnime[0].ID)
}

func TestTasteScale(t *testing.T) {
	if testing.Short() || os.Getenv("PORTICO_TASTE_SCALE") == "" {
		t.Skip("set PORTICO_TASTE_SCALE=1 without -short")
	}
	c := Open(t)
	start := time.Now()
	BuildTaste(t, c, TasteShape{Movies: 99500, Shows: 100, EpisodesPerShow: 5, Anime: 10, Seed: 7})
	t.Logf("built 100000 playable titles in %s", time.Since(start))
}

func TestTasteBuild5K(t *testing.T) {
	if os.Getenv("PORTICO_TASTE_5K") == "" {
		t.Skip("set PORTICO_TASTE_5K=1")
	}
	c := Open(t)
	start := time.Now()
	BuildTaste(t, c, TasteShape{Movies: 4900, Shows: 20, EpisodesPerShow: 5, Anime: 3, Seed: 7})
	t.Logf("built 5000 playable titles in %s", time.Since(start))
}
