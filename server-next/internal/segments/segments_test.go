package segments

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
	"github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectionUsesSavedModeAndKeepsCompletedFiles(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := seed.Import(ctx, d, seed.Config{Seed: 7, Items: 60}, nil); err != nil {
		t.Fatal(err)
	}
	docs, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	libs, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lib := libs[0]
	reads := 0
	read := func(_ context.Context, e db.SegmentEntry, mode string) (segmentdetect.Evidence, []segmentdetect.Detection, error) {
		reads++
		if mode != "standard" && mode != "thorough" {
			t.Fatal("wrong effective mode", mode)
		}
		found := []segmentdetect.Detection{{Kind: "intro", StartUS: 1000000, EndUS: 2000000, Confidence: .85, Provenance: segmentdetect.ChapterProvenance}}
		return segmentdetect.Evidence{DurationUS: e.DurationMS * 1000}, found, nil
	}
	s := &Service{DB: d, Settings: docs, Now: time.Now, readEvidence: read}
	run := func() {
		t.Helper()
		if err := s.Kind().Run(ctx, jobs.Work{Request: jobs.Request{Target: jobs.Target{Library: lib.PublicID}}, Progress: func(int64, int64) {}}); err != nil {
			t.Fatal(err)
		}
	}
	run()
	first := reads
	if first == 0 {
		t.Fatal("no file was measured")
	}
	counts, _, err := d.SegmentCounts(ctx, lib.PublicID, "", "standard")
	if err != nil || counts.Pending != 0 || counts.Complete == 0 {
		t.Fatal(counts, err)
	}
	run()
	if reads != first {
		t.Fatal("unchanged completed files were measured again", reads, first)
	}
	configuration := settings.Segments.Get(docs)
	configuration.Mode = "thorough"
	raw, _ := json.Marshal(configuration)
	if _, err := docs.Write(ctx, 0, settings.Segments.Name(), raw, -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	run()
	if reads != first*2 {
		t.Fatal("changing detection mode did not measure files again", reads, first)
	}
	lib.Options.SegmentDetection = "off"
	if err := d.SetLibraryOptions(ctx, lib.ID, lib.Options); err != nil {
		t.Fatal(err)
	}
	run()
	if reads != first*2 {
		t.Fatal("a library switched off still measured files")
	}
	lib.Options.SegmentDetection = "standard"
	if err := d.SetLibraryOptions(ctx, lib.ID, lib.Options); err != nil {
		t.Fatal(err)
	}
	docs.PauseBackground(time.Now(), true)
	paused, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	err = s.Kind().Run(paused, jobs.Work{Request: jobs.Request{Target: jobs.Target{Library: lib.PublicID}}, Progress: func(int64, int64) {}})
	if err != context.DeadlineExceeded || reads != first*2 {
		t.Fatal("paused detection did work", err, reads)
	}
	docs.PauseBackground(time.Now(), false)
	run()
	if reads != first*3 {
		t.Fatal("the library's own mode was not used", reads, first)
	}
}
