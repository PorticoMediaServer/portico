package catalog

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Compare the complete seed relation, including display identity, with the
// original scorer before the final three causal titles are selected.
func explanationSeeds(t *testing.T, s *Service, r HomeRequest, ids []string) []string {
	t.Helper()
	base, args := recBase(r, ids)
	rows, err := s.read().Query(base+` SELECT seed.work,seed.weight,pid(e.public_id),e.title FROM seeds seed JOIN works w ON w.work=seed.work JOIN catalog_entities e ON e.id=w.id ORDER BY seed.work`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var work, id, title string
		var weight float64
		if err = rows.Scan(&work, &weight, &id, &title); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s/%g/%s/%s", work, weight, id, title))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHomeExplanationPrefilterPreservesTheOriginalSeeds(t *testing.T) {
	f := recommendationFixtureDB(t)
	r := engineRequest().scoped()
	s := New(f.db)
	pool := engineWholeLibrary(t, s, r)
	check := func(t *testing.T) []string {
		t.Helper()
		filtered, err := s.homeExplanationCandidates(r.Profile, pool)
		if err != nil {
			t.Fatal(err)
		}
		before, after := explanationSeeds(t, s, r, pool), explanationSeeds(t, s, r, filtered)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("seed relation changed:\noriginal %v\nfiltered %v", before, after)
		}
		return filtered
	}
	t.Run("empty and another viewer", func(t *testing.T) {
		engineWatched(t, f, "other", r.Now.Format(time.RFC3339), "m1")
		if got := check(t); len(got) != 0 {
			t.Fatalf("foreign facts retained %v", got)
		}
	})
	engineMeta(t, f, "m1", "tmdb", "edition-work")
	engineMeta(t, f, "m2", "tmdb", "edition-work")
	engineWatched(t, f, r.Profile, r.Now.Add(-time.Hour).Format(time.RFC3339), "m2", "e101")
	engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,favorite,revision)VALUES(?,?,1,1)`, r.Profile, f.items["t2"].ID)
	engineExec(t, f, `INSERT INTO progress(profile_id,item_id,position,playback_id)VALUES(?,?,500000,'probe')`, r.Profile, f.items["p3"].ID)
	engineExec(t, f, `INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at)VALUES(?,?,1,?)`, r.Profile, f.items["m5"].ID, r.Now.Format(time.RFC3339))
	t.Run("direct facts and complete edition works", func(t *testing.T) {
		got := check(t)
		for _, key := range []string{"m1", "m2", "e101", "e102", "e201", "e202", "t1", "t2", "t3", "p1", "p2", "p3"} {
			if !containsString(got, f.items[key].Public) {
				t.Fatalf("lost member %s", key)
			}
		}
		if len(got) >= len(pool) {
			t.Fatal("untouched works were not removed")
		}
	})
	t.Run("negative sibling", func(t *testing.T) {
		engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,not_interested,revision)VALUES(?,?,1,1)`, r.Profile, f.items["m1"].ID)
		check(t)
	})
	t.Run("current availability and restrictions", func(t *testing.T) {
		f.c.Attributes(f.items["e101"].ID, "label", "hidden")
		f.c.Drain()
		r.Restrictions.BlockedLabels = []string{"hidden"}
		engineExec(t, f, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=?`, f.items["m2"].ID)
		check(t)
	})
	t.Run("inherited container intents keep original relation", func(t *testing.T) {
		engineExec(t, f, `INSERT INTO container_personal_state(profile_id,kind,container_id,watched,watermark,revision)VALUES(?,'show',?,1,?,1)`, r.Profile, f.items["show"].ID, r.Now.Format(time.RFC3339))
		got := check(t)
		if !reflect.DeepEqual(got, pool) {
			t.Fatal("inherited state did not retain original bounded pool")
		}
	})
}

func TestHomeExplanationPrefilterKeepsFavoriteOnlySeedDisplacement(t *testing.T) {
	f := recommendationFixtureDB(t)
	r := engineRequest("movies").scoped()
	engineWatched(t, f, r.Profile, r.Now.Add(-time.Hour).Format(time.RFC3339), "m1")
	for i := 0; i < 70; i++ {
		item := f.c.Movie(f.libs["movies"], fmt.Sprintf("/movies/favorite-%d.mkv", i), fmt.Sprintf("Favorite %d", i), 2020)
		engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,favorite,last_played_at,revision)VALUES(?,?,1,?,1)`, r.Profile, item.ID, r.Now.Format(time.RFC3339))
	}
	f.c.Drain()
	s := New(f.db)
	pool := engineWholeLibrary(t, s, r)
	filtered, err := s.homeExplanationCandidates(r.Profile, pool)
	if err != nil {
		t.Fatal(err)
	}
	before, after := explanationSeeds(t, s, r, pool), explanationSeeds(t, s, r, filtered)
	if len(before) != 64 || !reflect.DeepEqual(before, after) {
		t.Fatalf("top64 changed: before%d after%d", len(before), len(after))
	}
	for _, row := range after {
		if strings.Contains(row, "/Movie m1") {
			t.Fatal("older watched seed must stay displaced")
		}
	}
}

func TestHomeExplanationFactsSeekPersonalRowsByPrimaryKey(t *testing.T) {
	f := recommendationFixtureDB(t)
	rows, err := f.db.Query(`EXPLAIN QUERY PLAN `+homeExplanationFactsSQL, idsJSON([]string{f.items["m1"].Public}), "p", "p", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		for _, alias := range []string{"p", "pg", "wi"} {
			if strings.HasPrefix(detail, "SCAN "+alias+" ") {
				t.Fatal(detail)
			}
			if strings.HasPrefix(detail, "SEARCH "+alias+" USING ") {
				seen[alias] = true
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("missing indexed personal seeks: %v", seen)
	}
}
