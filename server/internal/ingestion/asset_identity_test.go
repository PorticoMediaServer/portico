package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A file whose bytes are replaced at the same path keeps its identity (the
// asset token and the item's public id) across rescans, and a file that goes
// missing and comes back is available again.
func TestSamePathReplacementKeepsIdentityAndReturnRestoresAvailability(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(root, "media")
	if err = os.MkdirAll(media, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Keep (2000).mp4", "Move (2001).mp4", "Vanish (2002).mp4"} {
		if err = os.WriteFile(filepath.Join(media, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(filepath.Join(root, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalog.New(db)
	lib, err := c.Create("Movies", "movie", media)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	s := New(db, c, assets.Probe{})
	ctx := context.Background()
	run := func(mode string) {
		t.Helper()
		job, e := s.QueueMode(ctx, lib.ID, "", mode, nil)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 100; i++ {
			scanInfoTurn(t, s, db, job.ID, lib.ID)
			state, e := s.Get(job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.Status == "complete" {
				// Scan completion releases the finite source41 reducer. Observe
				// availability only after that ordinary derived work settles.
				if err := compactcatalog.Drain(ctx, db); err != nil {
					t.Fatal(err)
				}
				return
			}
			if state.Status == "failed" {
				t.Fatal(state)
			}
		}
		t.Fatal("scan did not finish")
	}
	token := func(relative string) (string, bool) {
		t.Helper()
		var id string
		var available bool
		if e := db.QueryRow(`SELECT token,available FROM catalog_assets WHERE path=?`, filepath.Join(media, relative)).Scan(&id, &available); e != nil {
			t.Fatalf("%s: %v", relative, e)
		}
		return id, available
	}
	item := func(relative string) string {
		t.Helper()
		var public string
		if e := db.QueryRow(`SELECT pid(e.public_id) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id WHERE a.path=?`, filepath.Join(media, relative)).Scan(&public); e != nil {
			t.Fatalf("%s: %v", relative, e)
		}
		return public
	}
	run("")
	keep, _ := token("Keep (2000).mp4")
	keepItem := item("Keep (2000).mp4")
	run("full")
	file := filepath.Join(media, "Keep (2000).mp4")
	if err = os.WriteFile(file, []byte("replacement bytes, longer"), 0600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err = os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	run("full")
	if got, _ := token("Keep (2000).mp4"); got != keep {
		t.Fatal("a same-path replacement changed the asset's identity", keep, got)
	}
	if got := item("Keep (2000).mp4"); got != keepItem {
		t.Fatal("a same-path replacement changed the item's public id", keepItem, got)
	}
	vanish := filepath.Join(media, "Vanish (2002).mp4")
	if err = os.Rename(vanish, filepath.Join(root, "aside.mp4")); err != nil {
		t.Fatal(err)
	}
	run("full")
	run("full")
	if _, available := token("Vanish (2002).mp4"); available {
		t.Fatal("a missing file is still available")
	}
	if err = os.Rename(filepath.Join(root, "aside.mp4"), vanish); err != nil {
		t.Fatal(err)
	}
	run("full")
	if _, available := token("Vanish (2002).mp4"); !available {
		t.Fatal("a returned file is not available again")
	}
}
