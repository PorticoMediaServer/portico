package db_test

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An import uses its full batch until a person's write has had to wait behind background
// work. Then its transactions are kept short, and go back to full when nobody has waited
// for a while.
func TestImportBatchesShortenOnlyWhileSomeoneWaits(t *testing.T) {
	// Long enough that a slow machine cannot run it out between the wait and the look.
	t.Cleanup(db.SetBusyFor(time.Hour))
	l := newLibrary(t, 300)
	films, err := l.d.Items(ctx, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	if err != nil || len(films.Items) == 0 {
		t.Fatalf("films: %d, %v", len(films.Items), err)
	}
	// Forty files took 25 ms: with nobody waiting the next batch is forty again.
	next := func() int { return l.d.NextImportFiles(40, 40, 25*time.Millisecond) }
	yes := true
	state(t, l.d, l.all, films.Items[0].PublicID, db.StateEdit{Favorite: &yes}) // a write that waited for nothing
	if got := next(); got != 40 {
		t.Fatalf("with nobody waiting the next batch is %d files, want 40", got)
	}
	// Background work has the writer, a tap joins the queue behind it, and only then is the
	// background work let go. Each step is waited for, not timed.
	holding, queued, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	held, tapped := make(chan error, 1), make(chan error, 1)
	go func() { held <- db.HoldWriterTelling(l.d, holding, release) }()
	<-holding
	go func() { tapped <- db.PersonWriteTelling(l.d, queued) }()
	<-queued
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if err := <-tapped; err != nil {
		t.Fatal(err)
	}
	// Five milliseconds' worth of a batch that took 25 ms for forty files is eight.
	if got := next(); got != 8 {
		t.Fatalf("after a tap waited the next batch is %d files, want 8", got)
	}
	// Once nobody has waited for the busy time, batches are full again.
	db.SetBusyFor(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if got := next(); got != 40 {
		t.Fatalf("with nobody waiting any more the next batch is %d files, want 40", got)
	}
}
