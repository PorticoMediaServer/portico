package db_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A committed partial page survives a changed page boundary, and a lost write rolls back
// both its entries and its keys. Keys must also survive a shutdown beyond the client retry
// window while the unfinished job still exists.
func TestListJobPartialPagesAreSafeToReplay(t *testing.T) {
	l := newLibrary(t, 600)
	films := everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	now := time.Now()
	list, err := l.d.CreateList(ctx, l.all, db.NewList{Kind: db.ListPlaylist, Name: "Mix", SortName: "mix"}, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	job, err := l.d.StartJob(ctx, db.NewJob{Kind: "add_view_to_list", Target: "partial", OwnerID: l.all.UserID})
	if err != nil {
		t.Fatal(err)
	}
	add := func(ids ...string) {
		t.Helper()
		if _, err := l.d.AddJobToList(ctx, l.all, list.PublicID, ids, job, now); err != nil {
			t.Fatal(err)
		}
	}
	counts := func(want int64) {
		t.Helper()
		got, err := l.d.VisibleJobCounts(ctx, l.all, job.ID)
		if err != nil || got.Done != want || got.Added != want || got.Already != 0 {
			t.Fatalf("outcomes: %+v, %v, want %d", got, err, want)
		}
	}
	add(films[0].PublicID, films[1].PublicID)
	// A title disappears from the view before restart; the second page now overlaps the
	// first. Only the new title follows, not another copy of the overlap.
	add(films[1].PublicID, films[2].PublicID)
	got, _, _ := entries(t, l.d, l.all, list.PublicID)
	want := []string{films[0].PublicID, films[1].PublicID, films[2].PublicID}
	if !slices.Equal(got, want) {
		t.Fatalf("changed pages: %v, want %v", got, want)
	}
	counts(3)
	// Force a failure after the write has added one title but before its second title can
	// be processed. The whole write, including the first title's key, must roll back.
	key := "list-view:" + job.PublicID + ":" + films[4].PublicID
	if err := l.d.RememberCreated(ctx, l.all.UserID, key, "another request", "0 0", now); err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.AddJobToList(ctx, l.all, list.PublicID, []string{films[3].PublicID, films[4].PublicID}, job, now); !errors.Is(err, db.ErrKeyReused) {
		t.Fatalf("failed write: %v", err)
	}
	if got, _, _ := entries(t, l.d, l.all, list.PublicID); !slices.Equal(got, want) {
		t.Fatalf("entries escaped rollback: %v", got)
	}
	counts(3) // the failed transaction must not leave an outcome either
	add(films[3].PublicID)
	want = append(want, films[3].PublicID)
	// An unrelated creating request ages ordinary keys out, as after a long shutdown.
	now = now.Add(40 * 24 * time.Hour)
	if err := l.d.RememberCreated(ctx, l.all.UserID, "ordinary", "ordinary", "ordinary", now); err != nil {
		t.Fatal(err)
	}
	add(want...)
	counts(4) // replay must not count the same original addition twice
	if got, _, _ := entries(t, l.d, l.all, list.PublicID); !slices.Equal(got, want) {
		t.Fatalf("old unfinished job added titles twice: %v", got)
	}
	// Another intentional addition is a new job and may add the same playlist title.
	other, err := l.d.StartJob(ctx, db.NewJob{Kind: "add_view_to_list", Target: "intentional", OwnerID: l.all.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.AddJobToList(ctx, l.all, list.PublicID, want[:1], other, now); err != nil {
		t.Fatal(err)
	}
	want = append(want, want[0])
	if got, _, _ := entries(t, l.d, l.all, list.PublicID); !slices.Equal(got, want) {
		t.Fatalf("intentional repeat: %v, want %v", got, want)
	}
}

func TestListJobsExpandHoldersOnceAndKeepCollectionsUnique(t *testing.T) {
	l := newLibrary(t, 600)
	now := time.Now()
	var music string
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryMusic {
			music = lib.PublicID
		}
	}
	albums := everything(t, l.d, l.all, db.ItemQuery{Library: music, Kinds: []db.ItemKind{db.KindAlbum}})
	album := albums[0].PublicID
	tracks, err := l.d.PlayableUnder(ctx, l.all, album)
	if err != nil || len(tracks) == 0 {
		t.Fatalf("album: %v %v", tracks, err)
	}
	for _, kind := range []string{db.ListPlaylist, db.ListCollection} {
		list, err := l.d.CreateList(ctx, l.all, db.NewList{Kind: kind, Name: kind, SortName: kind}, "", "", now)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"first", "first", "another"} {
			job, err := l.d.StartJob(ctx, db.NewJob{Kind: "add_view_to_list", Target: kind + target, OwnerID: l.all.UserID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.d.AddJobToList(ctx, l.all, list.PublicID, []string{album}, job, now); err != nil {
				t.Fatal(err)
			}
		}
		got, _, _ := entries(t, l.d, l.all, list.PublicID)
		want := 1
		if kind == db.ListPlaylist {
			want = 2 * len(tracks)
		}
		if len(got) != want {
			t.Fatalf("%s has %d entries, want %d", kind, len(got), want)
		}
	}
}
