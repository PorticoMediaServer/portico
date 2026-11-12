package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/musicanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/sonic"
)

// Sound analysis lists each song of a music library once, page by page, until it has an
// analysis by the model in force; a song whose file was replaced, or a newer model, brings it
// back. A song that could not be read is not listed again.
func TestSoundAnalysisReadsEachSongOnce(t *testing.T) {
	s, _, _ := seededWith(t, 600, 0)
	ctx := context.Background()
	libraries, err := s.db.MusicLibraries(ctx)
	if err != nil || len(libraries) == 0 {
		t.Fatalf("music libraries: %v %v", libraries, err)
	}
	lib := libraries[0]
	var all []db.TrackToAnalyse
	for after := int64(0); ; {
		page, err := s.db.TracksToAnalyse(ctx, lib.ID, "m1", after, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].ItemID
	}
	if len(all) == 0 || all[0].Path == "" || all[0].Size == 0 {
		t.Fatalf("songs to read: %+v", all)
	}
	var batch []db.TrackAnalysis
	for n, track := range all {
		a := db.TrackAnalysis{Track: track, Model: "m1", Embedding: make([]byte, sonic.Dimensions*4), Tags: make([]byte, sonic.TagCount*4)}
		if n == 0 {
			a = db.TrackAnalysis{Track: track, Model: "m1", Failed: "This song's sound could not be read."}
		}
		batch = append(batch, a)
	}
	if err := s.db.SaveTrackAnalyses(ctx, batch, s.now()); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.db.TracksToAnalyse(ctx, lib.ID, "m1", 0, 10); len(left) != 0 {
		t.Fatalf("after every song was read, %d are listed again", len(left))
	}
	if again, _ := s.db.TracksToAnalyse(ctx, lib.ID, "m2", 0, 1000); len(again) != len(all) {
		t.Fatalf("a newer model lists %d of %d songs", len(again), len(all))
	}
	replaced := batch[1]
	replaced.Track.Size++
	if err := s.db.SaveTrackAnalyses(ctx, []db.TrackAnalysis{replaced}, s.now()); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.db.TracksToAnalyse(ctx, lib.ID, "m1", 0, 10); len(left) != 1 || left[0].ItemID != replaced.Track.ItemID {
		t.Fatalf("a song read from a file of another size lists %+v", left)
	}
}

// Without the sound model installed, the analysis job says it can't run and reads nothing;
// outside the owner's hours for heavy work it waits for them.
func TestSoundAnalysisWithoutTheModelAndOutsideItsHours(t *testing.T) {
	s, _, _ := seededWith(t, 600, 0)
	ctx := context.Background()
	tools := func(context.Context) (*processpool.Supervisor, string, error) { return &processpool.Supervisor{}, "/usr/bin/ffmpeg", nil }
	music := &musicanalysis.Service{DB: s.db, Log: s.log, Tools: tools, Now: s.now,
		Hours:  func() settings.HeavyWorkSettings { return settings.General.Get(s.settings).HeavyWork },
		Locate: func(string) sonic.RuntimeOptions { return sonic.RuntimeOptions{Helper: "/nowhere/portico-sonic", ModelDirectory: "/nowhere/musicnn-mtt"} }}
	if r, _, _ := music.Runtime(ctx); r != nil {
		t.Fatal("a server without the model has a runtime")
	}
	libraries, _ := s.db.MusicLibraries(ctx)
	err := music.Kind().Run(ctx, jobs.Work{Job: db.Job{}, Request: jobs.Request{Target: jobs.Target{Library: libraries[0].PublicID}}, Progress: func(int64, int64) {}})
	var done jobs.DoneWith
	if !errors.As(err, &done) || done.Note != "Music analysis can't run on this server." {
		t.Fatalf("the job without the model ended with %v", err)
	}

	noon := time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local)
	if until, paused := musicanalysis.PausedUntil(settings.HeavyWorkSettings{Hours: settings.HeavyOvernight}, noon); !paused ||
		!until.Equal(time.Date(2026, 11, 13, 1, 0, 0, 0, time.Local)) {
		t.Fatalf("overnight hours at noon: paused %v until %v", paused, until)
	}
	if _, paused := musicanalysis.PausedUntil(settings.HeavyWorkSettings{Hours: settings.HeavyAnyTime}, noon); paused {
		t.Fatal("Any time paused heavy work")
	}
}
