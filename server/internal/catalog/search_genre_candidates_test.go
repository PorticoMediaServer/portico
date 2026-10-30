package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func genreCandidateStage(t *testing.T, c *catalogtest.Catalog, library int64) (*FacetBases, int64, int64) {
	t.Helper()
	b := &FacetBases{DB: c.DB, Producer: &facetFixtureProducer{}, MaxBytes: 128 << 20}
	scope := FacetBaseScope{LibraryID: library, Library: "movies", Field: "genre", Policy: FacetBasePolicy(Viewer{}.Restrictions), Source: SearchVideoGenreSource + `:[{"id":1,"library":"movies"}]`}
	var base, value int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		base, err = b.Begin(ctx, tx, scope, "candidate-fixture", "authority")
		if err != nil {
			return err
		}
		s, err := facetBaseSnapshot(ctx, tx, base, false)
		if err != nil {
			return err
		}
		if err = b.put(ctx, tx, s, FacetBaseFact{Value: "Genre", Label: "Genre", Count: 1}); err != nil {
			return err
		}
		if err = tx.QueryRow(`SELECT value_id FROM catalog_facet_base_rows WHERE base_id=?`, base).Scan(&value); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO catalog_search_genre_ready(base_id,value_id) VALUES(?,?)`, base, value)
		return err
	})
	return b, base, value
}

// 49 consecutive better targets exhaust the 48-bit gap at the head. Exercise
// both repack phases, top100 eviction and the actual class-default writer plan.
func TestGenreCandidateBackgroundMedia49BetterTargetsRepackAndRetire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	library := c.Library("movies", "Movies", "movie", "/movies")
	items := make([]catalogtest.Item, 110)
	for i := range items {
		items[i] = c.Movie(library, fmt.Sprintf("/movies/%03d.mkv", i), fmt.Sprintf("Title %03d", i), 2000)
		selectSearchGenreArt(t, c, items[i], "item", "poster")
	}
	c.Drain()
	for i, item := range items {
		c.Exec(`UPDATE catalog_browse_rows SET rating_max=? WHERE entity_id=?`, i, item.ID)
	}
	b, base, value := genreCandidateStage(t, c, library)
	repacks, maxChanges := 0, int64(0)
	dbwork.ResetPlans()
	for _, item := range items {
		cursor := genreCandidateCursor{}
		for turn := 0; ; turn++ {
			if turn > 100 {
				t.Fatal("candidate failed to finish", cursor)
			}
			done := false
			err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
				var before, after int64
				if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
					return err
				}
				old := cursor.Repack
				var err error
				done, err = stepGenreCandidate(context.Background(), tx, base, value, item.ID, 1, 2, &cursor)
				if err != nil {
					return err
				}
				if old == 0 && cursor.Repack == 1 {
					repacks++
				}
				raw, _ := json.Marshal(cursor)
				if _, err = tx.Exec(`UPDATE catalog_facet_base_work SET cursor=? WHERE base_id=?`, string(raw), base); err != nil {
					return err
				}
				if err = tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
					return err
				}
				if after-before > maxChanges {
					maxChanges = after - before
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if done {
				break
			}
		}
	}
	stats := dbwork.Plans()
	if stats.Overruns != 0 {
		t.Fatalf("ordinary top100 maintenance exceeded class-default plan: %+v", stats)
	}
	t.Logf("candidate gated write largest measured cost: %+v", stats.Largest)
	if repacks < 2 {
		t.Fatal("fixture did not exhaust head gaps twice", repacks)
	}
	if maxChanges > dbwork.DefaultPlan(dbwork.ClassBackgroundMedia).Rows {
		t.Fatal("default row plan exceeded", maxChanges)
	}
	var count, targets, negative int
	if err := c.DB.QueryRow(`SELECT count(*),sum(position<0) FROM catalog_search_genre_candidates WHERE base_id=?`, base).Scan(&count, &negative); err != nil {
		t.Fatal(err)
	}
	c.DB.QueryRow(`SELECT count(*) FROM catalog_search_genre_targets WHERE base_id=?`, base).Scan(&targets)
	if count != 100 || targets != 100 || negative != 0 {
		t.Fatal(count, targets, negative)
	}
	rows, err := c.DB.Query(`SELECT rating FROM catalog_search_genre_candidates c JOIN catalog_search_genre_targets t USING(base_id,entity_id) WHERE c.base_id=? ORDER BY position`, base)
	if err != nil {
		t.Fatal(err)
	}
	expected := float64(109)
	for rows.Next() {
		var rating float64
		if err = rows.Scan(&rating); err != nil {
			t.Fatal(err)
		}
		if rating != expected {
			t.Fatal("repack lost rank", rating, expected)
		}
		expected--
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if expected != 9 {
		t.Fatal(expected)
	}
	// Publish a real retained base, detach its active reference, then reclaim all
	// FK children in bounded transactions. Targets must not block header delete.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		done, err := b.Step(ctx, tx, base)
		if err == nil && !done {
			return errors.New("not published")
		}
		return err
	})
	c.Exec(`UPDATE catalog_facet_base_classes SET active_base=NULL WHERE active_base=?`, base)
	for turn := 0; ; turn++ {
		if turn > 100 {
			t.Fatal("retirement did not finish")
		}
		done := false
		if err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			var err error
			done, err = b.RetireStep(context.Background(), tx, base)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	for _, table := range []string{"catalog_search_genre_candidates", "catalog_search_genre_targets", "catalog_search_genre_ready", "catalog_facet_bases"} {
		var n int
		column := "base_id"
		if table == "catalog_facet_bases" {
			column = "id"
		}
		if err = c.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+column+`=?`, base).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	t.Logf("head-gap repacks=%d; max trigger-inclusive rows/quantum=%d, unchanged background-media budget=%d", repacks, maxChanges, dbwork.DefaultPlan(dbwork.ClassBackgroundMedia).Rows)
}

type genreFinalChargeProducer struct{}

func (genreFinalChargeProducer) Next(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, _ string) (FacetBaseBatch, error) {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_search_genre_targets(base_id,entity_id,public_id,kind,rating,sort_key) VALUES(?,42,zeroblob(16),1,0,?)`, s.ID, strings.Repeat("x", 1024))
	return FacetBaseBatch{Done: true}, err
}
func (genreFinalChargeProducer) ValidatePublication(context.Context, *sql.Tx, FacetBaseSnapshot) error {
	return nil
}
func TestGenreFinalNoFactsCapacityIsAtomic(t *testing.T) {
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	library := c.Library("movies", "Movies", "movie", "/movies")
	b, base, _ := genreCandidateStage(t, c, library)
	var used int64
	if err := c.DB.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	b.MaxBytes = used + 1
	b.Producer = genreFinalChargeProducer{}
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { _, err := b.Step(context.Background(), tx, base); return err })
	if !errors.Is(err, ErrFacetBaseCapacity) {
		t.Fatal("final empty-Facts quantum bypassed global charge", err)
	}
	var complete, targets int
	var after int64
	c.DB.QueryRow(`SELECT complete FROM catalog_facet_bases WHERE id=?`, base).Scan(&complete)
	c.DB.QueryRow(`SELECT count(*) FROM catalog_search_genre_targets WHERE base_id=?`, base).Scan(&targets)
	c.DB.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&after)
	if complete != 0 || targets != 0 || after != used {
		t.Fatal("capacity error committed partial producer data", complete, targets, after, used)
	}
}

func TestGenreCandidateWideKeysYieldBeforeNextScalar(t *testing.T) {
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	library := c.Library("movies", "Movies", "movie", "/movies")
	items := []catalogtest.Item{}
	for i := 0; i < 3; i++ {
		item := c.Movie(library, fmt.Sprintf("/movies/%d.mkv", i), fmt.Sprint(i), 2000)
		selectSearchGenreArt(t, c, item, "item", "poster")
		items = append(items, item)
	}
	c.Drain()
	for i, item := range items {
		key := string(rune('a' + i))
		if i < 2 {
			key += strings.Repeat("x", 300<<10)
		}
		c.Exec(`UPDATE catalog_browse_rows SET rating_max=0,sort_key=? WHERE entity_id=?`, key, item.ID)
	}
	_, base, value := genreCandidateStage(t, c, library)
	for _, item := range items[:2] {
		cursor := genreCandidateCursor{}
		for turn := 0; ; turn++ {
			if turn > 5 {
				t.Fatal(cursor)
			}
			done := false
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				var err error
				done, err = stepGenreCandidate(ctx, tx, base, value, item.ID, 1, 2, &cursor)
				return err
			})
			if done {
				break
			}
		}
	}
	cursor := genreCandidateCursor{}
	for i := 0; i < 2; i++ {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = stepGenreCandidate(ctx, tx, base, value, items[2].ID, 1, 2, &cursor)
			return err
		})
		if done || cursor.After != int64(i+1)*genrePositionGap {
			t.Fatal("wide scalar did not yield before loading next candidate", done, cursor)
		}
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		done, err := stepGenreCandidate(ctx, tx, base, value, items[2].ID, 1, 2, &cursor)
		if err == nil && !done {
			return errors.New("wide-key continuation lost its insertion")
		}
		return err
	})
}
