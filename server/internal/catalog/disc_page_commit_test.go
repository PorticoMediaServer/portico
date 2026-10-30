package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestDiscObservationCommitsWithSingleAndBatchedPages(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("pages-%d", count), func(t *testing.T) {
			root := t.TempDir()
			db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			s := New(db)
			library, err := s.Create("Discs", "movie", root)
			if err != nil {
				t.Fatal(err)
			}
			source, err := scanLibrarySource(db.QueryRow(`SELECT `+sourceColumns+` FROM library_sources WHERE library_id=? ORDER BY id LIMIT 1`, library.ID))
			if err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			job := identity.Token()
			exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','unit')`, job, library.ID)
			exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,?,?,?,0)`, job, source.ID, source.Generation, source.Incarnation, source.RootIdentity)
			var pages []InventoryPageWrite
			for i := 1; i <= count; i++ {
				relative := fmt.Sprintf("Film%d", i)
				if err = os.MkdirAll(filepath.Join(root, relative, "BDMV"), 0o700); err != nil {
					t.Fatal(err)
				}
				req := storage.InventoryRequest{Root: root, RelativePath: relative, RootIdentity: source.RootIdentity, Limit: 128}
				page, err := storage.LocalInventoryPage(req)
				if err != nil {
					t.Fatal(err)
				}
				v, err := storage.LocalInventoryDirectory(req)
				if err != nil {
					t.Fatal(err)
				}
				v.Name = relative
				exec(`INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,?)`, job, relative)
				pages = append(pages, InventoryPageWrite{Relative: relative, Page: page, DiscRoot: &v})
			}
			// Fail the last title observation, after the page's cursor writes.
			// Even the first page of a batch must remain resumable after rollback.
			exec(fmt.Sprintf(`CREATE TRIGGER refuse_disc_observation BEFORE INSERT ON inventory_objects WHEN NEW.relative_path='Film%d' BEGIN SELECT RAISE(ABORT,'injected disc observation failure'); END`, count))
			if err = s.CommitInventoryPages(context.Background(), job, pages); err == nil {
				t.Fatal("injected observation failure was ignored")
			}
			var done, objects int
			if err = db.QueryRow(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND state='done'`, job).Scan(&done); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE source_id=?`, source.ID).Scan(&objects); err != nil {
				t.Fatal(err)
			}
			if done != 0 || objects != 0 {
				t.Fatalf("partial page commit: done=%d objects=%d", done, objects)
			}
			exec(`DROP TRIGGER refuse_disc_observation`)
			for replay := 0; replay < 2; replay++ {
				if err = s.CommitInventoryPages(context.Background(), job, pages); err != nil {
					t.Fatal(err)
				}
				for _, page := range pages {
					var state string
					var queued int
					if err = db.QueryRow(`SELECT state FROM inventory_directories WHERE job_id=? AND relative_path=?`, job, page.Relative).Scan(&state); err != nil {
						t.Fatal(err)
					}
					if err = db.QueryRow(`SELECT count(*) FROM inventory_objects o JOIN inventory_analysis_queue q ON q.object_id=o.id WHERE o.source_id=? AND o.relative_path=? AND o.state='available' AND q.job_id=?`, source.ID, page.Relative, job).Scan(&queued); err != nil {
						t.Fatal(err)
					}
					if state != "done" || queued != 1 {
						t.Fatalf("page %s: state=%s queued titles=%d", page.Relative, state, queued)
					}
				}
			}
		})
	}
}
