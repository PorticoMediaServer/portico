package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func copyInventoryScaleFile(t *testing.T, dst, src string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestLegacyInventoryGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_INVENTORY_SCALE_ITEMS"))
	if n == 0 {
		t.Skip("hosted scale probe")
	}
	seed := filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("inventory-%d.sqlite", n))
	if _, err := os.Stat(seed); os.IsNotExist(err) {
		f := newLoopFixture(t, "movie")
		lib := catalogtest.New(t, f.db).Handle(f.library)
		for first := 1; first <= n; first += 128 {
			last := min(n, first+127)
			tx, err := f.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			queries := []struct {
				sql  string
				args []any
			}{
				{`WITH RECURSIVE seq(n) AS(SELECT ? UNION ALL SELECT n+1 FROM seq WHERE n<?)
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head)
 SELECT n,randomblob(16),?,1,printf('Title %09d',n),printf('title %09d',n),'t' FROM seq`, []any{first, last, lib}},
				{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
 SELECT id,printf('asset-%09d',id),?||printf('/Film %09d.mkv',id),0,0,'mkv','','',0,0,0 FROM catalog_entities WHERE id BETWEEN ? AND ?`, []any{f.root, first, last}},
				{`INSERT INTO catalog_asset_links(entity_id,asset_id,part_index) SELECT id,id,0 FROM catalog_assets WHERE id BETWEEN ? AND ?`, []any{first, last}},
				{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT printf('object-%09d',a.id),s.id,a.token,s.incarnation,printf('Film %09d.mkv',a.id),'fixture','{}',0,0
 FROM catalog_assets a JOIN library_sources s ON s.id=? WHERE a.id BETWEEN ? AND ?`, []any{f.source, first, last}},
			}
			for _, q := range queries {
				if _, err = tx.Exec(q.sql, q.args...); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				t.Fatal(err)
			}
		}
		var seq int
		var name, path string
		if err = f.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
			t.Fatal(err)
		}
		if err = f.db.Close(); err != nil {
			t.Fatal(err)
		}
		copyInventoryScaleFile(t, seed, path)
	}
	path := filepath.Join(t.TempDir(), "probe.sqlite")
	copyInventoryScaleFile(t, path, seed)
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := &loopFixture{t: t, db: db, s: New(db, catalog.New(db), assets.Probe{})}
	if err = db.QueryRow(`SELECT s.library_id,s.id,l.root FROM library_sources s JOIN libraries l ON l.id=s.library_id LIMIT 1`).Scan(&f.library, &f.source, &f.root); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	job := f.queue()
	source := startInventorySource(t, f, job)
	f.exec(`UPDATE inventory_runs SET phase='legacy_inventory',reconcile_cursor='' WHERE job_id=?`, job.ID)
	dbwork.ResetPlans()
	start := time.Now()
	var failure any
	func() {
		defer func() { failure = recover() }()
		err = f.s.adoptLegacyPage(context.Background(), job.ID, source)
	}()
	result := map[string]any{"items": n, "elapsedMs": float64(time.Since(start).Microseconds()) / 1000, "plans": dbwork.Plans(), "panic": fmt.Sprint(failure), "error": fmt.Sprint(err)}
	if (failure != nil || err != nil) && os.Getenv("PORTICO_EXPECT_INVENTORY_FAILURE") == "" {
		t.Errorf("panic=%v error=%v", failure, err)
	}
	var phase, cursor string
	if e := db.QueryRow(`SELECT phase,reconcile_cursor FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&phase, &cursor); e != nil {
		t.Fatal(e)
	}
	result["phase"], result["cursor"] = phase, cursor
	b, _ := json.MarshalIndent(result, "", "  ")
	t.Log(string(b))
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "inventory.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
