package ingestion

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestQueuedScanWaitsForAdmissionAndResumes(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	lib, err := cat.Create("Films", "movie", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, cat, assets.Probe{})
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	admitted := false
	calls := 0
	s.Admission = func(context.Context, *sql.Tx, string) (bool, error) { calls++; return admitted, nil }
	s.processQuantum(context.Background(), job.ID, lib.ID)
	j, err := s.Get(job.ID)
	if err != nil || j.Status != "queued" || calls != 1 {
		t.Fatal(j, calls, err)
	}
	admitted = true
	s.processQuantum(context.Background(), job.ID, lib.ID)
	j, err = s.Get(job.ID)
	if err != nil || j.Status == "queued" {
		t.Fatal("admitted scan did not progress", j, err)
	}
}
