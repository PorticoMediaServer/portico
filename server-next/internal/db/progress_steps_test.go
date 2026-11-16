package db_test

import (
	"fmt"
	"sync"
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
	holding, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	saved := make(chan error, 1)
	go func() { saved <- db.SaveProgressBetweenSteps(ctx, l.d, batch, holding, release) }()
	select {
	case <-holding:
	case <-time.After(30 * time.Second):
		t.Fatal("the first position-save step did not commit")
	}
	// A real position was saved, but the rest of the batch must still be pending.
	first, err := l.d.Item(ctx, batch[0].Viewer, batch[0].ItemID)
	if err != nil || first.State.PositionMS != batch[0].PositionMS {
		t.Fatal("the first step did not save its position", err)
	}
	later, err := l.d.Item(ctx, batch[8].Viewer, batch[8].ItemID)
	if err != nil || later.State.PositionMS != 0 {
		t.Fatal("the whole position save ran before a tap could go between steps", err)
	}
	yes := true
	state(t, l.d, l.all, films[0].PublicID, db.StateEdit{Favorite: &yes})
	unblock()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the remaining position-save steps did not finish")
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
