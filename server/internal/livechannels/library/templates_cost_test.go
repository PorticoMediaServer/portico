package librarychannels

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Demo, 23 Sep: listing templates (and POST defaults, which lists them) ran a
// full preview with a first-day schedule for each of nine templates, in write
// transactions, and blew the request budget on a one-core host. The listing now
// probes eligibility only, in a read snapshot: it neither schedules a day nor
// waits for the write gate, so it answers while another writer holds it.
func TestTemplatesAreCheapAndNeverWaitForTheWriteGate(t *testing.T) {
	f := justinsLibrary(t)
	s, _ := New(f.db)
	held, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassSecurityFence)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	templates, err := s.Templates(ctx, ownerAuthority(nil))
	if err != nil {
		t.Fatalf("templates while a writer holds the gate: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("listing templates took %s", elapsed)
	}
	applicable := 0
	for _, tpl := range templates {
		if tpl.Applicable {
			applicable++
		}
	}
	if len(templates) != 9 || applicable == 0 {
		t.Fatalf("%d templates, %d applicable", len(templates), applicable)
	}
	var entries int
	if err = held.Tx().QueryRow(`SELECT count(*) FROM lc_entries`).Scan(&entries); err != nil || entries != 0 {
		t.Fatalf("templates scheduled entries: %d %v", entries, err)
	}
}

// A full preview near its request deadline returns the part of the first day
// it made, marked incomplete, instead of failing.
func TestPreviewFirstDayStopsBeforeTheDeadline(t *testing.T) {
	f := justinsLibrary(t)
	s, _ := New(f.db)
	s.now = func() time.Time { return f.now }
	c := channel("tv", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode"}, "sequential", "in-order")
	ctx, cancel := context.WithTimeout(context.Background(), previewDeadlineMargin/2)
	defer cancel()
	p, err := s.Preview(ctx, ownerAuthority(nil), c)
	if err != nil {
		t.Fatal(err)
	}
	if p.Complete {
		t.Fatalf("a preview cut short by its deadline says complete (%d entries)", len(p.FirstDay))
	}
}
