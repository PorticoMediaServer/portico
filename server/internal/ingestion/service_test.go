package ingestion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestDurableContinuationAndCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	media := filepath.Join(root, "media")
	if e := os.Mkdir(media, 0700); e != nil {
		t.Fatal(e)
	}
	// This source spans more than one process call. A restart must resume
	// all bounded pages, even when smaller source pages require more turns.
	for i := 0; i < 20*storage.MaxInventoryBatch; i++ {
		if e := os.Mkdir(filepath.Join(media, fmt.Sprint(i)), 0700); e != nil {
			t.Fatal(e)
		}
	}
	path := filepath.Join(root, "db")
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	cat := catalog.New(db)
	lib, e := cat.Create("Movies", "movie", media)
	if e != nil {
		t.Fatal(e)
	}
	s := New(db, cat, assets.Probe{})
	job, e := s.Queue(lib.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.process(context.Background(), job.ID, lib.ID)
	j, e := s.Get(job.ID)
	if e != nil || j.Status != "running" {
		t.Fatalf("unfinished work reported complete: %+v %v", j, e)
	}
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = New(db, catalog.New(db), assets.Probe{})
	for turn := 0; turn < 100; turn++ {
		s.process(context.Background(), job.ID, lib.ID)
		j, e = s.Get(job.ID)
		if e != nil || j.Status != "running" {
			break
		}
	}
	if e != nil || j.Status != "complete" {
		t.Fatalf("restart continuation %+v %v", j, e)
	}
	var completed int
	if e = db.QueryRow(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND state='done'`, job.ID).Scan(&completed); e != nil || completed != 20*storage.MaxInventoryBatch+1 {
		t.Fatalf("restart lost directory pages: %d %v", completed, e)
	}
	job, e = s.Queue(lib.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Cancel(job.ID); e != nil {
		t.Fatal(e)
	}
	s.process(context.Background(), job.ID, lib.ID)
	j, e = s.Get(job.ID)
	if e != nil || j.Status != "cancelled" {
		t.Fatalf("cancelled job restarted %+v %v", j, e)
	}
}
