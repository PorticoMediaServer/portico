package metadata

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

func TestMBMutationPickerSchedulesReadOnlyContinuationAndSettles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, names := mbLaneDB(t)
	c := catalogtest.New(t, db)
	lib := c.Library("disabled", "Disabled music", "music", "/disabled")
	artist := c.Artist(lib, "Disabled artist")
	album := c.Album(artist, "Disabled album", 2020)
	for i := 0; i < 300; i++ {
		c.Song(album, i+1, fmt.Sprintf("/disabled/%d.flac", i), fmt.Sprintf("Excluded %d", i))
	}
	c.Exec(`UPDATE mb_provider_policies SET enabled=0 WHERE library_id='disabled'`)
	c.Exec(`UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
	c.Exec(`UPDATE mb_jobs SET next_attempt='2000-01-01T00:00:00Z',lease_until='' WHERE kind='song'`)
	c.Exec(`UPDATE mb_jobs SET next_attempt='2001-01-01T00:00:00Z' WHERE kind='song' AND entity_id=?`, names["song"].ID)
	s := New(db, "")
	s.resetMBMutationPick()
	t.Cleanup(s.resetMBMutationPick)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, progress := metadataProgress(ctx)
	if err := dbwork.ReadHandle(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), db).QueryRowContext(ctx, `SELECT 1`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	before := backgroundGateAcquisitions()
	turns := 0
	found := false
	worker.Run(ctx, "mb-excluded-prefix-regression", nil, func(ctx context.Context) time.Duration {
		turns++
		progress.Reset()
		kind, public, err := s.pickMBMutationSafe(ctx, "2026-10-28T12:00:00Z")
		if err != nil {
			t.Error(err)
			cancel()
			return 0
		}
		delay := s.providerPassRetry(progress)
		if kind != "" {
			if kind != "song" || public != names["song"].Public {
				t.Errorf("wrong eligible job %s %s", kind, public)
			}
			if delay != 0 {
				t.Errorf("successful read-only pick retained continuation: %s", delay)
			}
			found = true
			cancel()
			return 0
		}
		if delay <= 0 {
			t.Error("excluded window lost its next pass without a commit or external wake")
			cancel()
		}
		return delay
	})
	if !found || turns != 3 {
		t.Fatalf("idle worker did not cross300excluded keys: found=%v turns=%d err=%v", found, turns, ctx.Err())
	}
	if n := backgroundGateAcquisitions() - before; n != 0 {
		t.Fatalf("read-only continuation acquired%d writers", n)
	}
	c.Exec(`UPDATE mb_jobs SET status='needs_selection'`)
	idle, idleProgress := metadataProgress(context.Background())
	kind, public, err := s.pickMBMutationSafe(idle, "2026-10-28T12:00:00Z")
	if err != nil || kind != "" || public != "" {
		t.Fatal(kind, public, err)
	}
	if delay := s.providerPassRetry(idleProgress); delay != 0 {
		t.Fatal("exhausted picker scheduled polling", delay)
	}
	if s.takeMBMutationPickContinuation() {
		t.Fatal("continuation was not consumed once")
	}
}
