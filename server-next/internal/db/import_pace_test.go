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
	t.Cleanup(db.SetBusyFor(300 * time.Millisecond))
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
	release, held, tapped := make(chan struct{}), make(chan error, 1), make(chan error, 1)
	go func() { held <- db.HoldWriter(l.d, release) }()
	time.Sleep(50 * time.Millisecond) // the background write has the writer
	go func() {
		_, _, err := l.d.SetState(ctx, l.all, films.Items[1].PublicID, db.StateEdit{Favorite: &yes})
		tapped <- err
	}()
	time.Sleep(50 * time.Millisecond) // the tap is waiting behind it
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
	time.Sleep(400 * time.Millisecond)
	if got := next(); got != 40 {
		t.Fatalf("with nobody waiting any more the next batch is %d files, want 40", got)
	}
}
