package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The oracle is the live SQL branch, independently reduced through the actual
// baseline. No schema patch/installer or accepted episode genre rows are used.
func TestFacetBaseSourceShowActualBaselineOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(library, "One", 2002)
	season := c.Season(show, 1)
	safe := c.Episode(show, season, 1, "/tv/safe.mkv")
	safe2 := c.Episode(show, season, 2, "/tv/safe2.mkv")
	hidden := c.Episode(show, season, 3, "/tv/hidden.mkv")
	second := c.Show(library, "Two", 1997)
	secondSeason := c.Season(second, 1)
	secondEp := c.Episode(second, secondSeason, 1, "/tv/two.mkv")
	denied := c.Show(library, "Denied", 2010)
	deniedSeason := c.Season(denied, 1)
	deniedEp := c.Episode(denied, deniedSeason, 1, "/tv/denied.mkv")
	c.Show(library, "Empty", 2020)
	c.Genres(show.ID, "show", "Shared", "Inherited", "Override original", "Empty override")
	c.Genres(safe.ID, "own", "Shared", "Episode own")
	c.Genres(safe.ID, "duplicate", "Shared", "Episode own")
	c.Genres(hidden.ID, "own", "Hidden sibling")
	c.Genres(second.ID, "show", "Shared")
	c.Genres(secondEp.ID, "own", "Second own")
	c.Genres(denied.ID, "show", "Denied inherited")
	giant := strings.Repeat("Long studio ", 24000)
	for _, ep := range []catalogtest.Item{safe, safe2, secondEp} {
		c.Attributes(ep.ID, "contentRating", "PG")
		c.Attributes(ep.ID, "studio", "Shared studio", giant)
	}
	c.Attributes(hidden.ID, "contentRating", "R")
	c.Attributes(hidden.ID, "studio", "Hidden studio")
	c.Attributes(deniedEp.ID, "contentRating", "R")
	c.Exec(`INSERT INTO content_rating_ages VALUES('pg',8),('r',18) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`)
	c.Exec(`UPDATE catalog_episodes SET season_id=NULL WHERE entity_id IN(?,?)`, safe.ID, safe2.ID)
	c.Exec(`UPDATE catalog_term_sources SET label_override='Override label' WHERE entity_id=? AND term_id=(SELECT id FROM catalog_terms WHERE vocab=1 AND label='Override original')`, show.ID)
	c.Exec(`UPDATE catalog_term_sources SET label_override='' WHERE entity_id=? AND term_id=(SELECT id FROM catalog_terms WHERE vocab=1 AND label='Empty override')`, show.ID)
	c.Drain()
	var globalGenres int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_show_facet_episodes WHERE field='genre'`).Scan(&globalGenres); err != nil {
		t.Fatal(err)
	}
	if globalGenres != 0 {
		t.Fatal("global episode genre expansion returned", globalGenres)
	}
	maxAge := 8
	r := identity.ContentRestrictions{MaximumAge: &maxAge, BlockUnrated: true}
	for _, field := range []string{"genre", "studio", "contentRating", "resolution", "year", "decade"} {
		plan := facetIntegratedPlan(t, c, library, "tv", field, r)
		got := facetIntegratedBuild(t, c, plan)
		source := facetSources[field]
		restriction, args := ItemRestrictionSQL("er.item_id", r)
		where := "1"
		if field == "year" || field == "decade" {
			where = "e.year BETWEEN 1800 AND 2199"
		}
		q := `SELECT ` + source.Value + `,` + source.Label + `,count(DISTINCT e.entity_id) FROM
   (SELECT er.item_id,ep.show_id entity_id,sh.year FROM catalog_browse_rows er
   JOIN catalog_episodes ep ON ep.entity_id=er.entity_id JOIN catalog_browse_rows sh ON sh.entity_id=ep.show_id
   WHERE er.library_id=? AND er.item_id IS NOT NULL AND ` + restriction + `) e ` + source.Join + ` WHERE ` + where + ` GROUP BY 1`
		rows, err := c.DB.Query(q, append([]any{library}, args...)...)
		if err != nil {
			t.Fatal(field, err)
		}
		want := map[string]FacetValue{}
		for rows.Next() {
			var f FacetValue
			if err = rows.Scan(&f.Value, &f.Label, &f.Count); err != nil {
				t.Fatal(err)
			}
			if f.Value != "" {
				want[f.Value] = f
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if got.Total != int64(len(want)) {
			t.Fatal(field, "wrong total", got.Total, len(want))
		}
		// A legal giant scalar may occupy a whole byte-bounded reader page.
		w, _ := facetWindowFixture(t, c.DB, nil)
		got = facetWindowInitial(t, w, plan.Scope, "", 200)
		actual := map[string]FacetValue{}
		for page := 0; page < 10; page++ {
			for _, f := range got.Values {
				actual[f.Value] = f
			}
			if got.Window.Next == "" {
				break
			}
			got = facetWindowNext(t, w, got.Window.Next)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("%s live oracle mismatch: got %d values, want %d", field, len(actual), len(want))
		}
	}
	var seen int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_facet_base_show_seen`).Scan(&seen); err != nil || seen != 0 {
		t.Fatal("published with current-show staging", seen, err)
	}
}

func showStagedFixture(t *testing.T, n int) (*sql.DB, *FacetBases, FacetBaseScope, int64) {
	t.Helper()
	db, scope := facetRestrictedFixture(t, true)
	scope.Field = "genre"
	age := 8
	scope.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &age})
	if _, err := db.Exec(`INSERT INTO catalog_entities VALUES(10,1),(101,1); INSERT INTO catalog_browse_rows VALUES(10,1,2,NULL,2002),(101,1,4,101,2002); INSERT INTO catalog_episodes(entity_id,show_id) VALUES(101,10); INSERT INTO catalog_item_attribute_edges VALUES(101,1,'PG')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := db.Exec(`INSERT INTO catalog_terms VALUES(?,1,?)`, i+1, fmt.Sprintf("Genre %02d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO catalog_term_sources VALUES(10,'p',?,?,NULL)`, fmt.Sprintf("source-%02d", i), i+1); err != nil {
			t.Fatal(err)
		}
	}
	b := &FacetBases{DB: db, Producer: RestrictedFacetBaseProducer{}, MaxBytes: 128 << 20}
	var id int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		w, err := CaptureRestrictedFacetBaseWitness(context.Background(), tx, scope)
		if err != nil {
			return err
		}
		id, err = b.Begin(context.Background(), tx, scope, w, "authority")
		return err
	})
	return db, b, scope, id
}

func showSeenCount(t *testing.T, db *sql.DB, id int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_facet_base_show_seen WHERE base_id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func showStep(t *testing.T, db *sql.DB, b *FacetBases, id int64) bool {
	t.Helper()
	var done bool
	facetBaseTx(t, db, func(tx *sql.Tx) error { var err error; done, err = b.Step(context.Background(), tx, id); return err })
	return done
}

func TestFacetBaseSourceShowRollbackRestartDriftAndRetirement(t *testing.T) {
	db, b, _, id := showStagedFixture(t, 20)
	for i := 0; i < 50 && showSeenCount(t, db, id) < 12; i++ {
		if showStep(t, db, b, id) {
			t.Fatal("completed before fixture staged")
		}
	}
	if n := showSeenCount(t, db, id); n != 12 {
		t.Fatal(n)
	}
	var cursor string
	var used int64
	var counts int64
	if err := db.QueryRow(`SELECT COALESCE(sum(negative_count),0) FROM catalog_facet_base_rows WHERE base_id=?`, id).Scan(&counts); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT cursor FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&cursor)
	db.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage`).Scan(&used)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Step(context.Background(), tx, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var after string
	var afterUsed int64
	db.QueryRow(`SELECT cursor FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&after)
	db.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage`).Scan(&afterUsed)
	var afterCounts int64
	if err := db.QueryRow(`SELECT COALESCE(sum(negative_count),0) FROM catalog_facet_base_rows WHERE base_id=?`, id).Scan(&afterCounts); err != nil {
		t.Fatal(err)
	}
	if cursor != after || used != afterUsed || counts != afterCounts || showSeenCount(t, db, id) != 12 {
		t.Fatal("rollback advanced count/seen/checkpoint")
	}
	// Reconstruct the producer to prove the durable cursor/set resumes safely.
	b = &FacetBases{DB: db, Producer: RestrictedFacetBaseProducer{}, MaxBytes: 128 << 20}
	if showStep(t, db, b, id) || showSeenCount(t, db, id) != 13 {
		t.Fatal("restart lost current-show set")
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := facetBaseSnapshot(context.Background(), tx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Producer.ValidatePublication(context.Background(), tx, snap); !errors.Is(err, ErrFacetBaseUnavailable) {
		t.Fatal("nonempty seen allowed publication", err)
	}
	if _, err = tx.Exec(`UPDATE catalog_facet_bases SET complete=1 WHERE id=?`, id); err == nil {
		t.Fatal("DDL allowed activation with seen")
	}
	tx.Rollback()
	if _, err = db.Exec(`UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1`); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Step(context.Background(), tx, id); !errors.Is(err, compactcatalog.ErrFacetProducerChanged) {
		t.Fatal("source drift accepted", err)
	}
	tx.Rollback()
	before := showSeenCount(t, db, id)
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		done, err := b.RetireStep(context.Background(), tx, id)
		if done {
			t.Fatal("bulk retirement")
		}
		return err
	})
	if got := showSeenCount(t, db, id); got != before-8 {
		t.Fatal("retirement not raw8", before, got)
	}
	for i := 0; i < 30; i++ {
		var done bool
		facetBaseTx(t, db, func(tx *sql.Tx) error {
			var err error
			done, err = b.RetireStep(context.Background(), tx, id)
			return err
		})
		if done {
			return
		}
	}
	t.Fatal("retirement did not finish")
}

func TestFacetBaseSourceShowCapacityRollbackNoActivation(t *testing.T) {
	db, b, _, id := showStagedFixture(t, 2)
	// Stop immediately before the first accepted raw source row.
	for i := 0; i < 20; i++ {
		var cursor string
		db.QueryRow(`SELECT cursor FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&cursor)
		var c restrictedShowCursor
		json.Unmarshal([]byte(cursor), &c)
		if c.Phase == "showGenres" {
			break
		}
		showStep(t, db, b, id)
	}
	var used int64
	db.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage`).Scan(&used)
	b.MaxBytes = used + 1
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Step(context.Background(), tx, id); !errors.Is(err, ErrFacetBaseCapacity) {
		t.Fatal("capacity not enforced", err)
	}
	tx.Rollback()
	var now int64
	var complete bool
	db.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage`).Scan(&now)
	db.QueryRow(`SELECT complete FROM catalog_facet_bases WHERE id=?`, id).Scan(&complete)
	if now != used || complete || showSeenCount(t, db, id) != 0 {
		t.Fatal("capacity failure partially committed")
	}
	// Canonical sources do not consult BASE capacity.
	if _, err = db.Exec(`INSERT INTO catalog_terms VALUES(99,1,'Accepted canonical write')`); err != nil {
		t.Fatal(err)
	}
}

// Opt-in small physical measurement against persistence.Open's actual baseline.
// This isolates the added ID set, not a whole-catalogue/capacity qualification.
func TestFacetBaseSourceShowPhysicalSize(t *testing.T) {
	if os.Getenv("PORTICO_P228_SHOW_SIZE") != "1" {
		t.Skip("opt-in small physical measurement")
	}
	c := catalogtest.Open(t)
	b := &FacetBases{DB: c.DB, Producer: &facetFixtureProducer{}, MaxBytes: 128 << 20}
	scope := FacetBaseScope{LibraryID: 1, Library: "tv", Field: "genre", Policy: FacetBasePolicy(identity.ContentRestrictions{BlockUnrated: true}), Source: RestrictedShowFacetBaseSource}
	var base int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		base, err = b.Begin(ctx, tx, scope, "physical-fixture", "authority")
		return err
	})
	// Shared identities are seeded before the set-only comparison. No labels are
	// stored in the measured primary/index objects, even for legal giant labels.
	ids := make([]int64, 20000)
	for first := 0; first < len(ids); first += 1000 {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for i := first; i < first+1000; i++ {
				var err error
				ids[i], err = ensureFacetBaseValueID(ctx, tx, scope, fmt.Sprintf("Genre %06d", i))
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	type size struct{ primary, reverse, ledger int64 }
	measure := func() size {
		t.Helper()
		var s size
		for name, dest := range map[string]*int64{"catalog_facet_base_show_seen": &s.primary, "catalog_facet_base_show_seen_value": &s.reverse} {
			if err := c.DB.QueryRow(`SELECT COALESCE(sum(pgsize),0) FROM dbstat WHERE name=?`, name).Scan(dest); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.DB.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage`).Scan(&s.ledger); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, n := range []int{256, 512, 20000} {
		before := measure()
		for first := 0; first < n; first += 1000 {
			end := min(first+1000, n)
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				for _, id := range ids[first:end] {
					if _, err := tx.Exec(`INSERT INTO catalog_facet_base_show_seen VALUES(?,?)`, base, id); err != nil {
						return err
					}
				}
				return nil
			})
		}
		after := measure()
		if after.ledger-before.ledger != 128*int64(n) {
			t.Fatal("seen ledger formula mismatch", before, after, n)
		}
		t.Logf("actual baseline current-show IDs=%d primary=%d reverse=%d root-inclusive=%d incremental=%d logical-charge=%d", n, after.primary, after.reverse, after.primary+after.reverse, after.primary+after.reverse-before.primary-before.reverse, after.ledger-before.ledger)
		for showSeenCount(t, c.DB, base) > 0 {
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				_, err := retireRestrictedShowSeenStep(ctx, tx, base)
				return err
			})
		}
		if afterClear := measure(); afterClear.ledger != before.ledger {
			t.Fatal("reclaim did not release logical charge", afterClear, before)
		}
	}
	rows, err := c.DB.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("fixture FK violation")
	}
	rows.Close()
	var integrity string
	if err = c.DB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
}

func TestFacetBaseSourceShowRawRejectedSourcesAndFullKeys(t *testing.T) {
	db, b, scope, id := showStagedFixture(t, 0)
	for i := 1; i <= 19; i++ {
		vocab := 2
		if i > 9 {
			vocab = 1
		}
		if _, err := db.Exec(`INSERT INTO catalog_terms VALUES(?,?,?)`, i, vocab, "Rejected"); err != nil {
			t.Fatal(err)
		}
		var override any
		if i > 9 {
			override = ""
		}
		if _, err := db.Exec(`INSERT INTO catalog_term_sources VALUES(10,'p',?,?,?)`, fmt.Sprintf("%02d", i), i, override); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO catalog_terms VALUES(20,1,'Empty key accepted'),(21,1,'After rejected'),(22,1,'Other provider'); INSERT INTO catalog_term_sources VALUES(10,'','',20,NULL),(10,'p','20',21,NULL),(10,'z','20',22,NULL)`); err != nil {
		t.Fatal(err)
	}
	// Minimal fixtures have no production source triggers: these seed rows keep
	// the fixture fence unchanged. Inspect one raw-row checkpoint at a time.
	consumed := 0
	for i := 0; i < 120; i++ {
		done := showStep(t, db, b, id)
		var raw string
		err := db.QueryRow(`SELECT cursor FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&raw)
		if err == nil {
			var c restrictedShowCursor
			if err = json.Unmarshal([]byte(raw), &c); err != nil {
				t.Fatal(err)
			}
			if c.Phase == "showGenres" && c.TermStarted {
				consumed++
			}
		} else if !done {
			t.Fatal(err)
		}
		if done {
			if consumed != 22 {
				t.Fatal("raw rejects did not consume complete provider/source keys", consumed)
			}
			w, _ := facetWindowFixture(t, db, nil)
			p := facetWindowInitial(t, w, scope, "", 10)
			if !reflect.DeepEqual(facetWindowValues(p), []string{"After rejected", "Empty key accepted", "Other provider"}) {
				t.Fatal(p.Values)
			}
			return
		}
	}
	t.Fatal("raw source walk did not finish")
}

func TestFacetBaseSourceShowEligibilityGuards(t *testing.T) {
	db, scope := facetRestrictedFixture(t, true)
	scope.Field = "genre"
	age := 8
	scope.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &age})
	_, err := db.Exec(`INSERT INTO catalog_entities VALUES(10,1),(20,1),(30,1),(40,1),(101,1),(102,1),(103,1),(104,1);
 INSERT INTO catalog_episodes(entity_id,show_id) VALUES(101,10),(102,20),(103,30),(104,40);
 INSERT INTO catalog_browse_rows VALUES(10,1,2,NULL,2000),(30,1,2,NULL,2000),(40,1,2,NULL,2000),(101,1,4,101,2000),(102,1,4,102,2000),(103,2,4,103,2000),(104,1,4,NULL,2000);
 INSERT INTO catalog_item_attribute_edges VALUES(101,1,'PG'),(102,1,'PG'),(103,1,'PG'),(104,1,'PG');
 INSERT INTO catalog_terms VALUES(1,1,'Accepted'),(2,1,'Missing show browse'),(3,1,'Other library'),(4,1,'NULL item');
 INSERT INTO catalog_term_sources VALUES(10,'p','1',1,NULL),(20,'p','2',2,NULL),(30,'p','3',3,NULL),(40,'p','4',4,NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	facetRestrictedBuild(t, db, scope)
	w, _ := facetWindowFixture(t, db, nil)
	got := facetWindowInitial(t, w, scope, "", 10)
	if !reflect.DeepEqual(facetWindowValues(got), []string{"Accepted"}) {
		t.Fatal(got.Values)
	}
}

func TestFacetBaseSourceShowClearsRaw8BeforeNextShow(t *testing.T) {
	db, b, _, id := showStagedFixture(t, 20)
	if _, err := db.Exec(`INSERT INTO catalog_entities VALUES(20,1),(201,1); INSERT INTO catalog_browse_rows VALUES(20,1,2,NULL,2002),(201,1,4,201,2002); INSERT INTO catalog_episodes(entity_id,show_id) VALUES(201,20); INSERT INTO catalog_item_attribute_edges VALUES(201,1,'PG'); INSERT INTO catalog_term_sources VALUES(20,'p','same',1,NULL)`); err != nil {
		t.Fatal(err)
	}
	cleared := false
	for step := 0; step < 150; step++ {
		var raw string
		if err := db.QueryRow(`SELECT cursor FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var before restrictedShowCursor
		json.Unmarshal([]byte(raw), &before)
		n := showSeenCount(t, db, id)
		done := showStep(t, db, b, id)
		after := showSeenCount(t, db, id)
		if before.Phase == "clear" {
			if n-after != min(n, 8) {
				t.Fatal("normal show cleanup exceeded raw8", n, after)
			}
			var checkpoint string
			if err := db.QueryRow(`SELECT cursor FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&checkpoint); err != nil {
				t.Fatal(err)
			}
			var current restrictedShowCursor
			if err := json.Unmarshal([]byte(checkpoint), &current); err != nil {
				t.Fatal(err)
			}
			if current.Show != before.Show {
				t.Fatal("next show consumed before cleanup")
			}
			cleared = true
		}
		if done {
			if !cleared || after != 0 {
				t.Fatal("incomplete normal cleanup")
			}
			var count int
			if err := db.QueryRow(`SELECT -r.negative_count FROM catalog_facet_base_rows r JOIN catalog_facet_base_values v ON v.id=r.value_id WHERE r.base_id=? AND v.value='Genre 00'`, id).Scan(&count); err != nil || count != 2 {
				t.Fatal("show overlap counted incorrectly", count, err)
			}
			return
		}
	}
	t.Fatal("normal cleanup did not complete")
}
