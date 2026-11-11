package db_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The periodic save of every stream's position is background work done a few positions to a
// transaction, so a person's tap that arrives while it runs is written between two steps and
// does not wait for the whole of it. Every position is saved all the same.
func TestATapGoesBetweenTheStepsOfAPositionSave(t *testing.T) {
	l := newLibrary(t, 900)
	films := everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200})
	if len(films) < 100 {
		t.Fatalf("the test library has %d films", len(films))
	}
	var batch []db.Progress
	for n := range 600 {
		film := films[n%len(films)]
		batch = append(batch, db.Progress{Viewer: l.viewers[n%7], ItemID: film.PublicID, PlayID: fmt.Sprint("play-", n),
			PositionMS: film.DurationMS / 2, At: time.Now(), WatchedMS: 1000})
	}
	saved := make(chan time.Time, 1)
	go func() {
		if _, err := l.d.SaveProgress(ctx, db.Background, batch); err != nil {
			t.Error(err)
		}
		saved <- time.Now()
	}()
	time.Sleep(20 * time.Millisecond)
	yes := true
	state(t, l.d, l.all, films[0].PublicID, db.StateEdit{Favorite: &yes})
	tapped := time.Now()
	if done := <-saved; !tapped.Before(done) {
		t.Fatalf("the tap was written %s after the whole position save had finished", tapped.Sub(done))
	}
	for n := range 7 {
		film := films[n]
		detail, err := l.d.Item(ctx, l.viewers[n], film.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.State.PositionMS != film.DurationMS/2 {
			t.Fatalf("%q was saved at %d ms for profile %d, want %d", film.Title, detail.State.PositionMS, n, film.DurationMS/2)
		}
	}
}
