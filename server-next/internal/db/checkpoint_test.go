package db_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// While an import runs under constant reads, the WAL is emptied by holding the import back
// for a moment, long before the size at which the writer would make people wait for reads.
func TestWALStaysSmallUnderConstantReads(t *testing.T) {
	const drain, insist = 1 << 20, 1 << 30
	t.Cleanup(db.SetWALLimits(drain, insist, 2*time.Second))
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := seed.Import(ctx, d, seed.Config{Seed: 7, Items: 500}, nil); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 4)
	if err != nil {
		t.Fatal(err)
	}
	libs, err := d.Libraries(ctx, viewers[0])
	if err != nil {
		t.Fatal(err)
	}
	films, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	if err != nil || len(films.Items) == 0 {
		t.Fatalf("films: %d, %v", len(films.Items), err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 { // reads, one straight after another
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					d.Items(ctx, viewers[0], db.ItemQuery{Library: libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, WithTotal: true})
				}
			}
		})
	}
	var largest int64
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(2 * time.Millisecond):
				if info, err := os.Stat(filepath.Join(dir, "main.db-wal")); err == nil {
					largest = max(largest, info.Size())
				}
			}
		}
	})
	if _, err := seed.Import(ctx, d, seed.Config{Seed: 8, Items: 4000}, nil); err != nil {
		t.Fatal(err)
	}
	close(done)
	wg.Wait()
	// The import wrote many times the drain size. Had the file not been emptied as it went,
	// it would hold all of that.
	if largest > 8*drain {
		t.Fatalf("the WAL reached %d KiB under constant reads; it should be emptied near %d KiB", largest>>10, drain>>10)
	}
	if largest < drain {
		t.Fatalf("the WAL only reached %d KiB, so this test never held the import back", largest>>10)
	}
}

// On a server that is never quiet (reads one after another, a person writing every few
// milliseconds) the WAL may never empty. Background work must go on all the same.
func TestBackgroundWorkGoesOnWhenTheWALNeverEmpties(t *testing.T) {
	t.Cleanup(db.SetWALLimits(1<<20, 1<<30, 50*time.Millisecond))
	l := newLibrary(t, 500)
	films, err := l.d.Items(ctx, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	if err != nil || len(films.Items) == 0 {
		t.Fatalf("films: %d, %v", len(films.Items), err)
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					l.d.Items(ctx, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, WithTotal: true})
				}
			}
		})
	}
	wg.Go(func() {
		for n := int64(0); ; n++ {
			select {
			case <-done:
				return
			case <-time.After(2 * time.Millisecond):
				position := 1000 + n
				l.d.SetState(ctx, l.all, films.Items[int(n)%len(films.Items)].PublicID, db.StateEdit{PositionMS: &position})
			}
		}
	})
	imported := make(chan error, 1)
	go func() {
		_, err := seed.Import(ctx, l.d, seed.Config{Seed: 8, Items: 2000}, nil)
		imported <- err
	}()
	select {
	case err := <-imported:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(90 * time.Second):
		t.Error("the import was still held back after 90 seconds")
	}
	close(done)
	wg.Wait()
}
