package mediaanalysis

import (
	"context"
	"fmt"
	"testing"

	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func countSegmentRows(t *testing.T, s *Service, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func insertSegmentRows(t *testing.T, s *Service, prefix, library, state string, show int64, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.db.Exec(`INSERT INTO segment_files(object_id,source_revision,library_id,show_id,mode,state,updated_ms) VALUES(?,'r',?,?,'standard',?,1)`, fmt.Sprintf("%s%04d", prefix, i), library, show, state); err != nil {
			t.Fatal(err)
		}
	}
}

// SRV022: each catch-up examines one window of segment rows for files that left
// the inventory, so a large settled corpus is never walked whole in one hold.
func TestSegmentOrphanCheckExaminesOneWindowPerPass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	s := &Service{db: c.DB}
	ctx := context.Background()
	total := 2*segmentOrphanPage + 88
	insertSegmentRows(t, s, "gone", "missing-library", "complete", 0, total)
	for pass, left := range []int{total - segmentOrphanPage, total - 2*segmentOrphanPage, 0} {
		if _, err := s.segmentCatchup(ctx); err != nil {
			t.Fatal(err)
		}
		if n := countSegmentRows(t, s, `SELECT count(*) FROM segment_files`); n != left {
			t.Fatalf("pass %d: %d rows left, want %d", pass, n, left)
		}
	}
	if s.segmentOrphanCursor != "" {
		t.Fatalf("cursor %q after a short window", s.segmentOrphanCursor)
	}
}

// SEG2: pending work of a library that is off is dropped a page at a time.
func TestDisabledSegmentWorkIsDroppedInPages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	s := &Service{db: c.DB}
	ctx := context.Background()
	insertSegmentRows(t, s, "off", "off-library", "pending", 0, segmentDropPage+100)
	insertSegmentRows(t, s, "done", "off-library", "complete", 0, 10)
	for _, left := range []int{100, 0} {
		if err := dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
			return dropDisabledSegmentWork(ctx, tx, map[string]segmentLibrary{})
		}); err != nil {
			t.Fatal(err)
		}
		if n := countSegmentRows(t, s, `SELECT count(*) FROM segment_files WHERE state='pending'`); n != left {
			t.Fatalf("%d pending left, want %d", n, left)
		}
	}
	if n := countSegmentRows(t, s, `SELECT count(*) FROM segment_files WHERE state='complete'`); n != 10 {
		t.Fatalf("completed evidence dropped: %d", n)
	}
}

// SEG2: a redetect larger than one batch requeues every file across batches.
func TestSegmentRedetectRequeuesInBatches(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSegmentFixture(t)
	ctx := context.Background()
	insertSegmentRows(t, f.service, "extra", "tv", "complete", f.show.ID, redetectBatchSize+44)
	n, err := f.service.SegmentRedetect(ctx, nil, f.show.Public)
	if err != nil || n != redetectBatchSize+44 {
		t.Fatalf("redetect %d %v", n, err)
	}
	if left := countSegmentRows(t, f.service, `SELECT count(*) FROM segment_files WHERE show_id=? AND state<>'pending'`, f.show.ID); left != 0 {
		t.Fatalf("%d files not requeued", left)
	}
}
