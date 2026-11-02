package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Hosted, projection-focused evidence only. Natural film titles are permuted
// into the other kinds, retaining their Zipf word distribution. All search
// postings are produced by deriveSearch under its unchanged writer plan.
func TestSearchMixedSeed(t *testing.T) {
	songs, _ := strconv.Atoi(os.Getenv("PORTICO_SEARCH_MIXED_SONGS"))
	if songs == 0 {
		t.Skip("mixed-kind hosted corpus")
	}
	films, _ := strconv.Atoi(os.Getenv("PORTICO_SEARCH_PROFILE_ITEMS"))
	if films == 0 {
		t.Fatal("films required")
	}
	db, err := persistence.Open(filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("search-realistic-films-%d.sqlite", films)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	schedule, err := dbwork.NewCheckpointSchedule(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Sparse library authority is independently measurable: only 0.5% of films
	// are in this viewer's allowed library. Other viewers retain both libraries.
	must(`INSERT INTO libraries(id,name,kind,root) VALUES('sparse-films','Sparse films','movie','/fixture/sparse'),('mixed-books','Books','audiobook','/fixture/books')`)
	var sparse, books, music, tv int64
	for _, x := range []struct {
		name string
		id   *int64
	}{{"sparse-films", &sparse}, {"mixed-books", &books}, {"music", &music}, {"tv", &tv}} {
		if err = db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, x.name).Scan(x.id); err != nil {
			t.Fatal(err)
		}
	}
	ids := []int64{}
	rows, err := db.Query(`SELECT entity_id FROM catalog_search_documents WHERE entity_kind=1 AND entity_id%200=0`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, id := range ids {
		must(`UPDATE catalog_entities SET library_id=? WHERE id=?`, sparse, id)
		err = dbwork.WithWriteTxContext(dbwork.WithPlan(ctx, derivationUnitPlan), db, dbwork.ClassMaintenance, func(writeCtx context.Context, tx *sql.Tx) error {
			return deriveSearch(writeCtx, tx, fmt.Sprintf("[%d]", id))
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var album, show int64
	if err = db.QueryRow(`SELECT min(entity_id) FROM catalog_albums`).Scan(&album); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT min(entity_id) FROM catalog_shows`).Scan(&show); err != nil {
		t.Fatal(err)
	}
	var next int64
	if err = db.QueryRow(`SELECT max(id)+1 FROM catalog_entities`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	dbwork.ResetPlans()
	for _, group := range []struct {
		name    string
		kind    Kind
		count   int
		library int64
	}{{"songs", Track, songs, music}, {"episodes", Episode, songs / 5, tv}, {"books", Book, songs / 20, books}} {
		for i := 0; i < group.count; i++ {
			id := next
			next++
			// Fact writes use normal triggers; derivation is measured separately.
			err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
				_, e := tx.ExecContext(ctx, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,year) SELECT ?,randomblob(16),?,?,e.title,e.sort_key,e.sort_head,e.year FROM catalog_entities e WHERE e.id=?`, id, group.library, group.kind, 1+(i*7919)%films)
				if e != nil {
					return e
				}
				switch group.kind {
				case Track:
					_, e = tx.ExecContext(ctx, `INSERT INTO catalog_songs(entity_id,album_id,disc_number,track_number) VALUES(?,?,1,?)`, id, album, i+1)
				case Episode:
					_, e = tx.ExecContext(ctx, `INSERT INTO catalog_episodes(entity_id,show_id,numbering,number) VALUES(?,?,'aired',?)`, id, show, i+1)
				case Book:
					_, e = tx.ExecContext(ctx, `INSERT INTO catalog_books(entity_id,library_id,local_key) VALUES(?,?,?)`, id, books, fmt.Sprintf("mixed-%d", i))
				}
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			err = dbwork.WithWriteTxContext(dbwork.WithPlan(ctx, derivationUnitPlan), db, dbwork.ClassMaintenance, func(writeCtx context.Context, tx *sql.Tx) error {
				return deriveSearch(writeCtx, tx, fmt.Sprintf("[%d]", id))
			})
			if err != nil {
				t.Fatalf("%s/%d: %v", group.name, i, err)
			}
			if i%1000 == 0 {
				schedule.Tick(ctx)
				t.Logf("mixed %s %d/%d elapsed %s", group.name, i, group.count, time.Since(started))
			}
		}
	}
	census := map[string]int{}
	rows, err = db.Query(`SELECT k.name,count(*) FROM catalog_search_documents d JOIN catalog_kinds k ON k.id=d.entity_kind GROUP BY k.name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		var count int
		if err = rows.Scan(&k, &count); err != nil {
			t.Fatal(err)
		}
		census[k] = count
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	raw, _ := json.MarshalIndent(map[string]any{"counts": census, "sparse_films": len(ids), "total_films": films, "visible_fraction": float64(len(ids)) / float64(films), "elapsed_ms": time.Since(started).Milliseconds(), "boundary": "search producer with Zipf-distributed titles; no asset/hydration or fully settled server claim"}, "", "  ")
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "mixed-census.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
