package compactcatalog

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestBrowseGenerationRetainsRowsUntilSwitch(t *testing.T) {
	db, show := failingFixture(t, "generations.sqlite")
	w := NewWorker(db)
	if err := w.Rebuild(context.Background(), DomainBrowseRows); err != nil {
		t.Fatal(err)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_browse_rows SET title='New title' WHERE entity_id=?`, show)
		return err
	})
	var title string
	if err := db.QueryRow(`SELECT title FROM catalog_browse_rows_serving WHERE entity_id=?`, show).Scan(&title); err != nil || title != "Show" {
		t.Fatalf("old generation: %q %v", title, err)
	}
	if CheckReadiness(context.Background(), db, DomainBrowseRows) != nil {
		t.Fatal("browse unavailable during rebuild")
	}
	cc1Drain(t, db)
	if n := cc1Scalar(t, db, `SELECT retaining FROM catalog_generations`); n != 0 {
		t.Fatalf("generation never switched: %d", n)
	}
}

func TestSearchGenerationsSwitchAndRebuildAgain(t *testing.T) {
	db, show := failingFixture(t, "search-generation.sqlite")
	w := NewWorker(db)
	if err := w.Rebuild(context.Background(), DomainSearch); err != nil {
		t.Fatal(err)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET title='Different' WHERE id=?`, show)
		return err
	})
	for i := 0; i < 100; i++ {
		n, err := w.Step(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	if slot := cc1Scalar(t, db, `SELECT active_slot FROM catalog_derivations WHERE domain=17`); slot != 1 {
		t.Fatalf("slot %d", slot)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_search_titles_next WHERE catalog_search_titles_next MATCH 'Different'`); n != 1 {
		t.Fatalf("next index has %d matches", n)
	}
	if err := w.Rebuild(context.Background(), DomainSearch); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		n, err := w.Step(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	if slot := cc1Scalar(t, db, `SELECT active_slot FROM catalog_derivations WHERE domain=17`); slot != 0 {
		t.Fatalf("second slot %d", slot)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH 'Different'`); n != 1 {
		t.Fatalf("rebuilt index has %d matches", n)
	}
}

func TestRetentionKeepsReferencedTerms(t *testing.T) {
	db, show := failingFixture(t, "retention.sqlite")
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_terms(vocab,key,label) VALUES(1,'unused','Unused'),(1,'used','Used')`)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_entity_terms(entity_id,term_id) SELECT ?,id FROM catalog_terms WHERE vocab=1 AND key='used'`, show)
		return err
	})
	for i := 0; i < 50; i++ {
		more := false
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			more, err = PruneTx(ctx, tx, 4)
			return err
		})
		if !more {
			break
		}
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_terms WHERE key='unused'`); n != 0 {
		t.Fatal("unused vocabulary retained")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_terms WHERE key='used'`); n != 1 {
		t.Fatal("referenced vocabulary removed")
	}
}

func TestFirstSearchBuildServesEachCommittedBatch(t *testing.T) {
	db := cc1OpenDB(t, "first-generation.sqlite")
	library := cc1Library(t, db, "films", "Films", "movie", "/films")
	cc1Entity(t, db, Entity{Library: library, Kind: Movie, Key: "one", Title: "First"}, nil)
	cc1Entity(t, db, Entity{Library: library, Kind: Movie, Key: "two", Title: "Second"}, nil)
	w := NewWorker(db)
	if _, err := w.Step(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH 'First'`); n != 1 {
		t.Fatal("first committed batch unavailable")
	}
	if n := cc1Scalar(t, db, `SELECT rebuilding FROM catalog_derivations WHERE domain=17`); n != 1 {
		t.Fatal("fixture did not leave the build running")
	}
}

func TestRetainedBrowseRowsKeepIndexedOrder(t *testing.T) {
	db, _ := failingFixture(t, "generation-indexes.sqlite")
	for _, order := range []struct{ expression, index string }{
		{"e.sort_key COLLATE NOCASE", "title"},
		{"COALESCE(e.added_text,'')", "added"},
		{"e.year", "year"},
		{"COALESCE(e.duration_max,0)", "duration"},
		{"COALESCE(e.rating_max,0)", "rating"},
		{"e.release_key", "release_key"},
		{"e.latest_aired_key", "latest_aired_key"},
		{"e.critic_key", "critic_key"},
		{"e.episode_count", "episode_count"},
		{"e.album_key", "album_key"},
		{"e.artist_key", "artist_key"},
		{"e.track_key", "track_key"},
		{"e.file_size", "file_size"},
		{"e.resolution_key", "resolution_key"},
		{"e.random_key", "random_key"},
	} {
		t.Run(order.index, func(t *testing.T) {
			query := ServingSQL(`SELECT ce.public_id,e.entity_id,e.title,e.year,COALESCE(e.added_text,''),e.sort_key,COALESCE(e.duration_max,0),COALESCE(e.rating_max,0),` + order.expression + ` FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.library_id=? AND e.kind=? ORDER BY ` + order.expression + `,e.entity_id LIMIT 20`)
			rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			previousIndex := false
			var plans []string
			for rows.Next() {
				var a, b, c int
				var detail string
				if err = rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				plans = append(plans, detail)
				if strings.Contains(detail, "USE TEMP B-TREE") {
					t.Fatalf("browse order materializes the generation: %v", plans)
				}
				if strings.Contains(detail, "catalog_browse_"+order.index+"_previous") {
					previousIndex = true
				}
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !previousIndex {
				t.Fatalf("retained generation did not use its order index: %v", plans)
			}
		})
	}
}

func TestGenerationRetainsTheOriginalRowOnUpsert(t *testing.T) {
	db, show := failingFixture(t, "generation-upsert.sqlite")
	w := NewWorker(db)
	if err := w.Rebuild(context.Background(), DomainBrowseRows); err != nil {
		t.Fatal(err)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_rows SELECT * FROM catalog_browse_rows WHERE entity_id=? ON CONFLICT(entity_id) DO UPDATE SET title='Updated'`, show)
		return err
	})
	var title string
	if err := db.QueryRow(`SELECT title FROM catalog_browse_rows_serving WHERE entity_id=?`, show).Scan(&title); err != nil || title != "Show" {
		t.Fatalf("upsert hid the original row: %q %v", title, err)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows_serving`); n != cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows`) {
		t.Fatal("upsert changed the serving membership")
	}
}

// Both search generations hold the same document shape: the expanded search
// text the index is built from and the folded text substring matches verify
// against. The index follows its documents through the tables' triggers alone,
// so a rebuild leaves each index consistent with its documents.
func TestSearchGenerationsCarryTheSameDocumentsAndStayConsistent(t *testing.T) {
	db, show := failingFixture(t, "search-generation-shape.sqlite")
	w := NewWorker(db)
	drain := func() {
		t.Helper()
		for i := 0; i < 200; i++ {
			n, err := w.Step(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return
			}
		}
		t.Fatal("derivation never settled")
	}
	drain()
	for round, tables := range [][2]string{{"catalog_search_documents_next", "catalog_search_titles_next"}, {"catalog_search_documents", "catalog_search_titles"}} {
		if err := w.Rebuild(context.Background(), DomainSearch); err != nil {
			t.Fatal(err)
		}
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET title=? WHERE id=?`, []string{"Señor Número", "Wall-E Returns"}[round], show)
			return err
		})
		drain()
		if slot := cc1Scalar(t, db, `SELECT active_slot FROM catalog_derivations WHERE domain=17`); slot != 1-round {
			t.Fatalf("round %d: active slot %d", round, slot)
		}
		var title, search, normalized string
		if err := db.QueryRow(`SELECT title,search,normalized FROM `+tables[0]+` WHERE entity_id=?`, show).Scan(&title, &search, &normalized); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if title != []string{"Señor Número", "Wall-E Returns"}[round] || search == "" || normalized == "" || strings.Contains(normalized, "ñ") {
			t.Fatalf("round %d: document %q search %q normalized %q", round, title, search, normalized)
		}
		if n := cc1Scalar(t, db, `SELECT count(*) FROM `+tables[1]+` WHERE `+tables[1]+` MATCH ?`, []string{"senor", "wall"}[round]); n != 1 {
			t.Fatalf("round %d: %d index matches", round, n)
		}
		for _, index := range []string{"catalog_search_titles", "catalog_search_titles_next"} {
			if _, err := db.Exec(`INSERT INTO ` + index + `(` + index + `) VALUES('integrity-check')`); err != nil {
				t.Fatalf("round %d: %s is not consistent with its documents: %v", round, index, err)
			}
		}
	}
}
