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

// Branch-only probe: production triggers stay installed. The fixture contains
// only facts needed to expose the rating publication's raw candidate walk.
func TestRatingWriterGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_RATING_SCALE_ITEMS"))
	if n == 0 {
		t.Skip("hosted scale probe")
	}
	root := os.Getenv("PORTICO_SCALE_SEEDS")
	if root == "" {
		t.Fatal("seed directory required")
	}
	seed := filepath.Join(root, fmt.Sprintf("rating-%d.sqlite", n))
	if _, err := os.Stat(seed); os.IsNotExist(err) {
		db, e := persistence.Open(seed)
		if e != nil {
			t.Fatal(e)
		}
		lib := cc1Library(t, db, "films", "Films", "movie", "/films")
		cc1Exec(t, db, `INSERT INTO catalog_attribute_terms(id,field_id,value_key) VALUES(900001,1,'scale-rating')`)
		for first := 1; first <= n; first += 256 {
			last := first + 255
			if last > n {
				last = n
			}
			tx, e := db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			_, e = tx.Exec(`WITH RECURSIVE seq(n) AS(SELECT ? UNION ALL SELECT n+1 FROM seq WHERE n<?)
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head)
 SELECT n,randomblob(16),?,1,printf('Title %09d',n),printf('title %09d',n),'t' FROM seq`, first, last, lib)
			if e == nil {
				_, e = tx.Exec(`INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid)
 SELECT id,900001,'Scale rating',id FROM catalog_entities WHERE id BETWEEN ? AND ?`, first, last)
			}
			if e != nil {
				tx.Rollback()
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); e != nil {
				t.Fatal(e)
			}
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "measure.sqlite")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	data = nil
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result := map[string]any{"items": n, "fixture": "raw film facts, one rating spelling, all production triggers"}
	probe := func(name string, fn func() error) {
		dbwork.ResetPlans()
		start := time.Now()
		var failure any
		func() { defer func() { failure = recover() }(); err = fn() }()
		result[name] = map[string]any{"elapsedMs": float64(time.Since(start).Microseconds()) / 1000, "plans": dbwork.Plans(), "panic": fmt.Sprint(failure), "error": fmt.Sprint(err)}
		if (failure != nil || err != nil) && os.Getenv("PORTICO_EXPECT_RATING_FAILURE") == "" {
			t.Errorf("%s: panic=%v error=%v", name, failure, err)
		}
	}
	probe("classify", func() error {
		_, e := ClassifyPendingRatings(context.Background(), db, func(string) (int, bool) { return 18, true })
		return e
	})
	if err == nil {
		cc1Exec(t, db, `UPDATE compact_visibility_source_state SET turn=3 WHERE id=1`)
		probe("fanout", func() error {
			return dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { _, e := StepVisibilitySourceFanout(context.Background(), tx, 8); return e })
		})
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	t.Log(string(b))
	if out := os.Getenv("PORTICO_SCALE_REPORT"); out != "" {
		if err = os.WriteFile(filepath.Join(out, "rating.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
