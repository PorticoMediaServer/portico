package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Opt-in because this creates a 100k-row durable diagnostic fixture. The path
// must be supplied explicitly and must not already contain a database.
func TestContentScale100K(t *testing.T) {
	dir := os.Getenv("PORTICO_SCALE_DIR")
	if dir == "" {
		t.Skip("set PORTICO_SCALE_DIR to an isolated empty Experiment directory")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "catalog.db")
	if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("refusing existing scale database")
	}
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close() }()
	c := catalogtest.New(t, db)
	start := time.Now()
	large := c.Library("large", "Large", "movie", "/scale/large")
	other := c.Library("other", "Other", "movie", "/scale/other")
	entityIDs := map[string]int64{}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n := 1; n <= 100000; n++ {
			id := fmt.Sprintf("i%06d", n)
			library, root, libraryKey := large, "/scale/large", "large"
			if n > 90000 {
				library, root, libraryKey = other, "/scale/other", "other"
			}
			path := root + "/" + id + ".mp4"
			added := fmt.Sprintf("2026-09-04T%02d:%02d:%02d.000Z", (n/3600)%24, (n/60)%60, n%60)
			entity, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: fmt.Sprintf("Movie %06d", n), Year: 1920 + n%100, Added: added})
			if err != nil {
				return err
			}
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1024, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 3600})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if n <= 10000 {
				if _, err = tx.ExecContext(ctx, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('viewer',?,120000,0,'fixture')`, entity); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('viewer',?,?,'2026-09-04T12:00:00.000Z','paused')`, libraryKey, entity); err != nil {
					return err
				}
			}
			if n == 1 || n == 80000 || n == 80001 {
				entityIDs[id] = entity
			}
		}
		return nil
	})
	c.Drain()
	if _, e = db.Exec(`ANALYZE`); e != nil {
		t.Fatal(e)
	}
	t.Logf("seed 100000 items,100000 assets,10000 progress: %s", time.Since(start))
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	c = catalogtest.New(t, db)
	s := New(db)
	cases := []struct {
		name string
		r    ContentRequest
	}{{"title", ContentRequest{View: "browse", Sort: "title"}}, {"prefix_title", ContentRequest{View: "browse", Sort: "title", Q: "Movie 089"}}, {"prefix_added", ContentRequest{View: "browse", Sort: "added", Q: "Movie 089"}}, {"broad_prefix_added", ContentRequest{View: "browse", Sort: "added", Q: "Movie "}}, {"year_decade", ContentRequest{View: "browse", Sort: "year", Category: "decade:2000"}}, {"discover_10k_progress", ContentRequest{View: "discover"}}}
	for _, c := range cases {
		r := c.r
		r.Library = "large"
		r.Profile = "viewer"
		r.ViewerFence = "fence"
		r.Limit = 40
		r.Viewer = Viewer{Profile: r.Profile, Fence: r.ViewerFence, Libraries: []string{r.Library}}
		var durations []time.Duration
		if _, e = db.Exec(`PRAGMA shrink_memory`); e != nil {
			t.Fatal(e)
		}
		var first ContentEnvelope
		for n := 0; n < 11; n++ {
			start = time.Now()
			p, e := s.Content(r)
			elapsed := time.Since(start)
			if e != nil {
				t.Fatal(c.name, e)
			}
			if n == 0 {
				first = p
				t.Logf("%s first SQLite-pager-cold (OS warm) duration=%s", c.name, elapsed)
			} else {
				durations = append(durations, elapsed)
			}
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		t.Logf("%s steady n=10 median=%s max=%s", c.name, durations[5], durations[9])
		if len(first.Sections) > 0 && first.Sections[0].NextCursor != "" {
			r.Cursor = first.Sections[0].NextCursor
			start = time.Now()
			if _, e = s.Content(r); e != nil {
				t.Fatal(e)
			}
			t.Logf("%s continuation=%s", c.name, time.Since(start))
		}
	}
	for n := 0; n < 2; n++ {
		start = time.Now()
		home, e := s.HomeRows(HomeRequest{Viewer: Viewer{Profile: "viewer", Fence: "home", Libraries: []string{"large", "other"}}, Profile: "viewer", ViewerFence: "home", Libraries: []string{"large", "other"}})
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("Home 2 libraries iteration%d=%s sections=%d", n, time.Since(start), len(home.Rows))
	}
	librarySQL := `(SELECT id FROM catalog_libraries WHERE library_id='large')`
	scalePlan(t, db, `SELECT entity_id FROM catalog_browse_rows WHERE library_id=`+librarySQL+` AND item_id IS NOT NULL AND added_text IS NOT NULL ORDER BY COALESCE(added_text,'') DESC,entity_id DESC LIMIT 12`)
	for _, q := range []string{
		`SELECT entity_id FROM catalog_browse_rows WHERE library_id=` + librarySQL + ` AND kind=1 AND title LIKE 'Movie 089%' ESCAPE '\' ORDER BY sort_key COLLATE NOCASE,entity_id LIMIT 41`,
		`SELECT entity_id FROM catalog_browse_rows WHERE library_id=` + librarySQL + ` AND kind=1 AND title LIKE 'Movie 089%' ESCAPE '\' ORDER BY COALESCE(added_text,'') DESC,entity_id DESC LIMIT 41`,
		`SELECT value,total FROM catalog_movie_category_summaries WHERE library_id=` + librarySQL + ` AND kind=0 ORDER BY CAST(value AS INTEGER) DESC LIMIT 40`,
		fmt.Sprintf(`SELECT entity_id FROM catalog_browse_rows WHERE library_id=%s AND kind=1 AND (sort_key COLLATE NOCASE,entity_id)>('Movie 080000',%d) ORDER BY sort_key COLLATE NOCASE,entity_id LIMIT 41`, librarySQL, entityIDs["i080000"]),
	} {
		scalePlan(t, db, q)
	}
	for _, where := range []string{
		fmt.Sprintf(`(sort_key COLLATE NOCASE,entity_id)>('Movie 080000',%d)`, entityIDs["i080000"]),
		fmt.Sprintf(`sort_key COLLATE NOCASE>='Movie 080000' AND (sort_key COLLATE NOCASE,entity_id)>('Movie 080000',%d)`, entityIDs["i080000"]),
	} {
		q := `SELECT entity_id FROM catalog_browse_rows WHERE library_id=` + librarySQL + ` AND kind=1 AND ` + where + ` ORDER BY sort_key COLLATE NOCASE,entity_id LIMIT 41`
		scalePlan(t, db, q)
		start = time.Now()
		for n := 0; n < 20; n++ {
			rows, e := db.Query(q)
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				var id int64
				if e = rows.Scan(&id); e != nil {
					t.Fatal(e)
				}
			}
			rows.Close()
		}
		t.Logf("deep seek mean n=20 %s: %s", where, time.Since(start)/20)
	}
	scalePlan(t, db, `SELECT value,total FROM catalog_movie_category_summaries WHERE library_id=`+librarySQL+` AND kind=0 AND CAST(value AS INTEGER)>=1800 AND total>0 ORDER BY CAST(value AS INTEGER) DESC LIMIT 40`)
	before, e := s.ContentRevision("large", "viewer")
	if e != nil {
		t.Fatal(e)
	}
	start = time.Now()
	for n := 0; n < 100; n++ {
		if _, e = db.Exec(`UPDATE progress_activity SET updated_at=? WHERE profile_id='viewer' AND item_id=?`, fmt.Sprint(n), entityIDs["i000001"]); e != nil {
			t.Fatal(e)
		}
	}
	after, _ := s.ContentRevision("large", "viewer")
	t.Logf("100 autocommit progress writes=%s catalogDelta=%d viewerDelta=%d", time.Since(start), after.Catalog-before.Catalog, after.Viewer-before.Viewer)
	r := ContentRequest{Viewer: Viewer{Profile: "viewer", Fence: "fence", Libraries: []string{"large"}}, Library: "large", Profile: "viewer", ViewerFence: "fence", View: "browse", Limit: 40}
	page, e := s.Content(r)
	if e != nil {
		t.Fatal(e)
	}
	r.Cursor = page.Sections[0].NextCursor
	c.Fields(entityIDs["i080000"], map[string]any{"title": "Scan replacement"})
	if _, e = s.Content(r); !errors.Is(e, ErrStaleContinuation) {
		t.Fatal("scan continuation unfenced", e)
	}
	var wg sync.WaitGroup
	writerErrors := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 200; n++ {
			err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				return compactcatalog.SetFieldsTx(context.Background(), tx, entityIDs["i080001"], compactcatalog.Automatic, map[string]any{"title": fmt.Sprintf("Scan %d", n)})
			})
			if err != nil {
				writerErrors <- err
				return
			}
		}
	}()
	fresh, stale := 0, 0
	r.Cursor = ""
	start = time.Now()
	for n := 0; n < 100; n++ {
		_, err := s.Content(r)
		if errors.Is(err, ErrStaleContinuation) {
			stale++
		} else if err != nil {
			t.Fatal(err)
		} else {
			fresh++
		}
	}
	wg.Wait()
	select {
	case err := <-writerErrors:
		t.Fatal(err)
	default:
	}
	t.Logf("concurrent 200 scan mutations /100 content requests: complete=%d explicitStale=%d duration=%s", fresh, stale, time.Since(start))
	r.Cursor = page.Sections[0].NextCursor
	r.Library = "other"
	if _, e = s.Content(r); !errors.Is(e, ErrCursor) {
		t.Fatal("cross library fence failed", e)
	}
}
func scalePlan(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	rows, e := db.Query("EXPLAIN QUERY PLAN " + q)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, c int
		var detail string
		if e = rows.Scan(&a, &b, &c, &detail); e != nil {
			t.Fatal(e)
		}
		t.Logf("PLAN %s | %s", q, detail)
	}
}
