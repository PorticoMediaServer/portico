package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestScanMissingSourceAfterInterruptionPreservesCatalogAndRetryHistory(t *testing.T) {
	base, pathErr := filepath.EvalSymlinks(t.TempDir())
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	root, path := filepath.Join(base, "source"), filepath.Join(base, "db")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := catalog.New(db)
	fixtures := catalogtest.New(t, db)
	library, err := c.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, library.ID); err != nil {
		t.Fatal(err)
	}
	s := New(db, c, assets.Probe{})
	job, err := s.Queue(library.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A durable job interrupted with prior progress must retain the catalog and
	// its historical counters if the source is missing when work resumes.
	old := fixtures.Movie(fixtures.Handle(library.ID), filepath.Join(root, "Old.mp4"), "Existing movie", 0)
	fixtures.Drain()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE jobs SET status='running',processed=4 WHERE id=?`, []any{job.ID}},
	} {
		if _, err = db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err = os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = New(db, catalog.New(db), assets.Probe{})
	s.process(context.Background(), job.ID, library.ID)
	fixtures = catalogtest.New(t, db)
	fixtures.Drain()
	j, err := s.Get(job.ID)
	if err != nil || j.Status != "failed" || j.Processed != 4 || j.Error != sourceUnavailable {
		t.Fatal(j, err)
	}
	var available int
	if err = db.QueryRow(`SELECT available FROM catalog_assets WHERE token=?`, old.Token).Scan(&available); err != nil || available != 1 {
		t.Fatal("missing source changed catalog availability", available, err)
	}
	var finished string
	if err = db.QueryRow(`SELECT finished_at FROM job_observations WHERE job_id=?`, job.ID).Scan(&finished); err != nil || finished == "" {
		t.Fatal("missing terminal observation", err)
	}
	if err = os.Rename(root+"-offline", root); err != nil {
		t.Fatal(err)
	}
	// The folder is back but empty. An empty folder where there was media is
	// what an unmounted disk looks like: the scan stops and nothing is marked.
	retry, err := s.Queue(library.ID)
	if err != nil || retry.ID == job.ID {
		t.Fatal(retry, err)
	}
	s.process(context.Background(), retry.ID, library.ID)
	fixtures.Drain()
	j, err = s.Get(retry.ID)
	if err != nil || j.Status != "failed" || j.Error != sourceUnavailable {
		t.Fatal(j, err)
	}
	j, err = s.Get(job.ID)
	if err != nil || j.Status != "failed" || j.Processed != 4 {
		t.Fatal("retry overwrote failure history", j, err)
	}
	if err = db.QueryRow(`SELECT available FROM catalog_assets WHERE token=?`, old.Token).Scan(&available); err != nil || available != 1 {
		t.Fatal("an empty folder marked its media missing", available, err)
	}
	var missing int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_missing_titles`).Scan(&missing); err != nil || missing != 0 {
		t.Fatal("an empty folder marked titles missing", missing, err)
	}
	// With another file present the folder is plainly there, and the one
	// complete listing that does not contain the old file marks it missing.
	if err = os.WriteFile(filepath.Join(root, "Other.mp4"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	confirmation, err := s.Queue(library.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.process(context.Background(), confirmation.ID, library.ID)
	fixtures.Drain()
	j, err = s.Get(confirmation.ID)
	if err != nil || j.Status != "complete" {
		t.Fatal(j, err)
	}
	if err = db.QueryRow(`SELECT available FROM catalog_assets WHERE token=?`, old.Token).Scan(&available); err != nil || available != 0 {
		t.Fatal("a complete listing without the file did not mark it missing", available, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM catalog_missing_titles m JOIN catalog_entities e ON e.id=m.entity_id WHERE e.title='Existing movie'`).Scan(&missing); err != nil || missing != 1 {
		t.Fatal("the title that lost its only file is not marked missing", missing, err)
	}
}

func TestScanSourceFailureDoesNotReplaceCancellationOrShutdown(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(base, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalog.New(db)
	library, err := c.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, c, assets.Probe{})
	job, err := s.Queue(library.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.process(ctx, job.ID, library.ID)
	j, err := s.Get(job.ID)
	if err != nil || j.Status != "queued" || j.Error != "" || j.Processed != 0 {
		t.Fatal("shutdown classified as source failure", j, err)
	}
	if err = s.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(root); err != nil {
		t.Fatal(err)
	}
	s.process(context.Background(), job.ID, library.ID)
	j, err = s.Get(job.ID)
	if err != nil || j.Status != "cancelled" || j.Error != "" {
		t.Fatal("source failure replaced cancel", j, err)
	}
}

func TestScanFinalSourceCheckUsesRealIsolatedRootBeforeReconciliation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(base, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalog.New(db)
	fixtures := catalogtest.New(t, db)
	lib, err := c.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	s := New(db, c, assets.Probe{})
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	existing := fixtures.Movie(fixtures.Handle(lib.ID), filepath.Join(root, "Retained.mp4"), "Existing", 0)
	fixtures.Drain()
	// Drive the actual inventory/adoption phases. Stop exactly before the final
	// directory-verification quantum instead of mutating the retired scan_queue.
	for turn := 0; turn < 32; turn++ {
		j, e := s.Get(job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.Phase == "verifying" {
			break
		}
		if j.Status == "failed" || j.Status == "complete" || j.Status == "complete_with_warnings" {
			t.Fatalf("never reached verification: %+v", j)
		}
		s.processQuantum(context.Background(), job.ID, lib.ID)
	}
	before, err := s.Get(job.ID)
	if err != nil || before.Phase != "verifying" {
		t.Fatalf("fixture phase: %+v %v", before, err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	checks, removed := 0, false
	store.Guard = func(string) error {
		checks++
		// readySource has just observed the real root in its isolated helper;
		// remove it before the final directory proof obtains its own handle.
		if checks == 2 {
			if e := os.Rename(root, root+"-offline"); e != nil {
				return e
			}
			removed = true
		}
		return nil
	}
	s.SetStorage(store)
	s.processQuantum(context.Background(), job.ID, lib.ID)
	j, err := s.Get(job.ID)
	// The step that loses the folder sets it aside as unreadable, as it would
	// any folder; the scan stops when it next looks for the library's root.
	// What matters is below: it never finishes, and nothing is marked missing.
	for n := 0; n < 8 && err == nil && j.Status == "running"; n++ {
		s.processQuantum(context.Background(), job.ID, lib.ID)
		j, err = s.Get(job.ID)
	}
	if err != nil || j.Status != "failed" || j.Error == "" || !removed {
		t.Fatal(j, checks, removed, err)
	}
	var available, authoritative int
	if err = db.QueryRow(`SELECT authoritative FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&authoritative); err != nil || authoritative != 0 {
		t.Fatal("failed final proof became authoritative", authoritative, err)
	}
	if err = db.QueryRow(`SELECT available FROM catalog_assets WHERE token=?`, existing.Token).Scan(&available); err != nil || available != 1 {
		t.Fatal("final missing root reconciled existing item", available, err)
	}
}
