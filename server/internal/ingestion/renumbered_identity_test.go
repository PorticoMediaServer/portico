package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// After a restart a network, FUSE or pooled mount can come back under another
// device number, and some filesystems hand out new inode numbers too. Neither
// makes a file a different file: the library folder is still the same folder,
// every file keeps its catalogue record, and nothing is analysed again.
func TestRenumberedDeviceAndInodeNumbersDoNotReplaceFiles(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	if err := os.MkdirAll(media, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"One (2000).mp4", "Two (2001).mp4"} {
		if err := os.WriteFile(filepath.Join(media, name), []byte(name), 0600); err != nil {
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
	run := func() {
		t.Helper()
		job, e := s.QueueMode(ctx, lib.ID, "", "full", nil)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 100; i++ {
			s.process(ctx, job.ID, lib.ID)
			state, e := s.Get(job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.Status == "complete" {
				return
			}
			if state.Status == "failed" {
				t.Fatal(state)
			}
		}
		t.Fatal("scan did not finish")
	}
	run()
	var rootIdentity, incarnation string
	if err = db.QueryRow(`SELECT root_identity,incarnation FROM library_sources WHERE library_id=?`, lib.ID).Scan(&rootIdentity, &incarnation); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rootIdentity, "dev:") {
		t.Skipf("this platform's folder identity (%q) has no device number to renumber", rootIdentity)
	}
	type record struct{ object, asset, identity string }
	read := func() map[string]record {
		t.Helper()
		out := map[string]record{}
		rows, e := db.Query(`SELECT relative_path,id,asset_id,object_identity FROM inventory_objects WHERE retired=0`)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			var r record
			if e = rows.Scan(&path, &r.object, &r.asset, &r.identity); e != nil {
				t.Fatal(e)
			}
			out[path] = r
		}
		return out
	}
	before := read()
	if len(before) != 2 {
		t.Fatalf("files catalogued: %d", len(before))
	}
	for path, r := range before {
		if !strings.HasPrefix(r.identity, "ino:") {
			t.Fatalf("%s is kept under an identity that carries a device number: %q", path, r.identity)
		}
	}
	// As the previous boot would have left it: the folder under another device
	// number, and the files under inode numbers the filesystem no longer uses.
	inode := rootIdentity[strings.Index(rootIdentity, ":ino:"):]
	if _, err = db.Exec(`UPDATE library_sources SET root_identity=? WHERE library_id=?`, "dev:999999"+inode, lib.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE inventory_objects SET object_identity='ino:1'||rowid,analysis_revision=revision,analysis_state='complete'`); err != nil {
		t.Fatal(err)
	}
	run()
	after := read()
	for path, was := range before {
		now, ok := after[path]
		if !ok || now.object != was.object || now.asset != was.asset {
			t.Fatalf("%s was treated as a different file: before %+v after %+v", path, was, now)
		}
		if now.identity != was.identity {
			t.Fatalf("%s did not take the identity observed now: %q", path, now.identity)
		}
	}
	var replaced, reanalyse int
	var nowIncarnation, nowRoot string
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM inventory_changes WHERE kind='replaced'),(SELECT count(*) FROM inventory_objects WHERE analysis_revision<>revision)`).Scan(&replaced, &reanalyse); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT incarnation,root_identity FROM library_sources WHERE library_id=?`, lib.ID).Scan(&nowIncarnation, &nowRoot); err != nil {
		t.Fatal(err)
	}
	if replaced != 0 || reanalyse != 0 {
		t.Fatalf("renumbering replaced %d files and queued %d for analysis again", replaced, reanalyse)
	}
	if nowIncarnation != incarnation || nowRoot != rootIdentity {
		t.Fatalf("the folder was not recognised as the same folder: incarnation %q→%q identity %q→%q", incarnation, nowIncarnation, rootIdentity, nowRoot)
	}
	if n := 0; db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE kind=1`).Scan(&n) != nil || n != 2 {
		t.Fatalf("titles after the renumbering: %d", n)
	}
}
