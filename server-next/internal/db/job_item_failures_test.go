package db_test

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Per-title failures commit with the state changes; joining or resuming keeps them, while
// successful replay, a fresh run, title deletion and the existing job tidy-up remove them.
func TestJobStateOutcomesAndTheirLifetime(t *testing.T) {
	l := newLibrary(t, 600)
	films := everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	var music string
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryMusic {
			music = lib.PublicID
		}
	}
	albums := everything(t, l.d, l.all, db.ItemQuery{Library: music, Kinds: []db.ItemKind{db.KindAlbum}})
	if len(films) == 0 || len(albums) < 2 {
		t.Fatal("fixture needs films and albums")
	}
	ask := db.NewJob{Kind: "test-state-outcomes", Target: "one", OwnerID: l.all.UserID}
	j, err := l.d.StartJob(ctx, ask)
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	film := db.ItemEdit{ItemID: films[0].PublicID, StateEdit: db.StateEdit{Watched: &yes}}
	album := db.ItemEdit{ItemID: albums[0].PublicID, StateEdit: db.StateEdit{Watched: &yes}}
	states, refused, err := l.d.SetJobStates(ctx, l.all, []db.ItemEdit{album, film}, j.ID)
	if err != nil || refused[0] == nil || refused[1] != nil || !states[1].Watched {
		t.Fatalf("mixed outcomes: %+v %v %v", states, refused, err)
	}
	check := func(want int) {
		t.Helper()
		got, err := l.d.JobItemFailures(ctx, l.all, j.ID, 0, 200)
		if err != nil || len(got) != want {
			t.Fatalf("%d failures, want %d: %v", len(got), want, err)
		}
	}
	check(1)
	if again, err := l.d.StartJob(ctx, ask); err != nil || again.ID != j.ID {
		t.Fatalf("join: %+v %v", again, err)
	}
	check(1)
	if _, err := l.d.RunJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.d.PauseJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	check(1)
	// State changes cannot commit if recording a refusal in that write fails.
	favorite := db.ItemEdit{ItemID: films[0].PublicID, StateEdit: db.StateEdit{Favorite: &yes}}
	if _, _, err := l.d.SetJobStates(ctx, l.all, []db.ItemEdit{favorite, album}, j.ID+1000000); err == nil {
		t.Fatal("a failure for a nonexistent job committed")
	}
	if it, err := l.d.Item(ctx, l.all, films[0].PublicID); err != nil || it.State.Favorite {
		t.Fatalf("state escaped the rolled-back write: %+v %v", it.State, err)
	}
	album.StateEdit = db.StateEdit{Favorite: &yes}
	if _, _, err := l.d.SetJobStates(ctx, l.all, []db.ItemEdit{album}, j.ID); err != nil {
		t.Fatal(err)
	}
	check(0)
	album.StateEdit = db.StateEdit{Watched: &yes}
	if _, _, err := l.d.SetJobStates(ctx, l.all, []db.ItemEdit{album}, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.d.FinishJob(ctx, j.ID, db.JobFailed, "Some titles could not be changed."); err != nil {
		t.Fatal(err)
	}
	if again, err := l.d.StartJob(ctx, ask); err != nil || again.ID != j.ID {
		t.Fatalf("new run: %+v %v", again, err)
	}
	check(0)
	secondAlbum := album
	secondAlbum.ItemID = albums[1].PublicID
	if _, _, err := l.d.SetJobStates(ctx, l.all, []db.ItemEdit{album, secondAlbum}, j.ID); err != nil {
		t.Fatal(err)
	}
	page, err := l.d.JobItemFailures(ctx, l.all, j.ID, 0, 200)
	if err != nil || len(page) != 2 {
		t.Fatalf("before deletion: %+v %v", page, err)
	}
	if err := l.d.DeleteItem(ctx, page[1].PublicID); err != nil {
		t.Fatal(err)
	}
	continued, err := l.d.JobItemFailures(ctx, l.all, j.ID, page[0].ItemID, 200)
	if err != nil || len(continued) != 0 {
		t.Fatalf("deletion between pages: %+v %v", continued, err)
	}
	if err := l.d.DeleteItem(ctx, page[0].PublicID); err != nil {
		t.Fatal(err)
	}
	check(0)
	film.Ago = 36 * 24 * time.Hour
	if _, _, err := l.d.SetJobStates(ctx, l.all, []db.ItemEdit{film}, j.ID); err != nil {
		t.Fatal(err)
	}

	check(1)
	if err := l.d.FinishJob(ctx, j.ID, db.JobFailed, "Some titles could not be changed."); err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.RemoveEndedJobs(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	check(0)
}
