package musicanalysis

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// A library's analysis reads its songs, saves what it reads and ends when nothing is left; a
// library turned off while it reads stops within one group of songs, keeping what was read,
// and the songs it did not reach are still waiting when it is turned on again.
func TestTheJobReadsAndStopsWhenTurnedOff(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := seed.Import(ctx, d, seed.Config{Seed: 3, Items: 600}, nil); err != nil {
		t.Fatal(err)
	}
	libraries, err := d.MusicLibraries(ctx)
	if err != nil || len(libraries) == 0 {
		t.Fatalf("music libraries: %v %v", libraries, err)
	}
	lib := libraries[0]
	all, err := d.TracksToAnalyse(ctx, lib.ID, "stand-in", 0, 100000)
	if err != nil || len(all) < 20 {
		t.Fatalf("songs to read: %d %v", len(all), err)
	}
	var readSongs atomic.Int64
	stopAt := int64(-1)
	s := &Service{DB: d, Log: slog.Default(), Now: time.Now, Hours: func() settings.HeavyWorkSettings { return settings.HeavyWorkSettings{Hours: settings.HeavyAnyTime} },
		model: "stand-in"}
	s.read = func(ctx context.Context, t db.TrackToAnalyse) (db.TrackAnalysis, error) {
		if readSongs.Add(1) == stopAt {
			off := lib.Options
			off.NoSoundAnalysis = true
			if _, _, err := d.EditLibrary(ctx, lib.PublicID, db.LibraryEdit{Options: &off}); err != nil {
				return db.TrackAnalysis{}, err
			}
		}
		return db.TrackAnalysis{Track: t, Embedding: make([]byte, 800), Tags: make([]byte, 200)}, nil
	}
	run := func() error {
		return s.Kind().Run(ctx, jobs.Work{Request: jobs.Request{Target: jobs.Target{Library: lib.PublicID}}, Progress: func(int64, int64) {}})
	}

	docs, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	s.Settings = docs
	docs.PauseBackground(s.Now(), true)
	paused, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err := s.Kind().Run(paused, jobs.Work{Request: jobs.Request{Target: jobs.Target{Library: lib.PublicID}}, Progress: func(int64, int64) {}}); err == nil || readSongs.Load() != 0 {
		t.Fatalf("paused analysis read %d songs: %v", readSongs.Load(), err)
	}
	docs.PauseBackground(s.Now(), false)

	stopAt = 5
	if err := run(); err != nil {
		t.Fatalf("the job turned off part way ended with %v", err)
	}
	read := readSongs.Load()
	left, _ := d.TracksToAnalyse(ctx, lib.ID, "stand-in", 0, 100000)
	if read > stopAt+SongsAtOnce || len(left) != len(all)-int(read) {
		t.Fatalf("turned off after song %d, it read %d and %d of %d are left", stopAt, read, len(left), len(all))
	}

	on := lib.Options
	on.NoSoundAnalysis = false
	if _, _, err := d.EditLibrary(ctx, lib.PublicID, db.LibraryEdit{Options: &on}); err != nil {
		t.Fatal(err)
	}
	stopAt = -1
	if err := run(); err != nil {
		t.Fatalf("the job turned on again ended with %v", err)
	}
	if left, _ := d.TracksToAnalyse(ctx, lib.ID, "stand-in", 0, 10); len(left) != 0 || readSongs.Load() != int64(len(all)) {
		t.Fatalf("after reading everything %d are left and %d were read of %d", len(left), readSongs.Load(), len(all))
	}
}
