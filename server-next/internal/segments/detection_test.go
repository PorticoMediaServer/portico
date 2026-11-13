package segments

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect/segmentsynth"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

func TestMain(m *testing.M) {
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// The real background job must publish confirmed markers, not just accept a
// substituted measurement: scan three tiny episodes, decode their bounded
// opening/closing regions through confinement, and compare the stored evidence.
func TestDetectionJobFindsSharedIntrosAndCredits(t *testing.T) {
	ctx := context.Background()
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	t.Setenv("PORTICO_FFMPEG", ffmpeg)
	t.Setenv("PORTICO_FFPROBE", ffprobe)
	root := t.TempDir()
	library := filepath.Join(root, "library")
	season := filepath.Join(library, "Example Show (2020)", "Season 01")
	if err := os.MkdirAll(season, 0700); err != nil {
		t.Fatal(err)
	}
	intro, credits := segmentsynth.Theme(11), segmentsynth.Theme(12)
	colds, bodies, tails := []float64{0, 75, 130}, []float64{110, 100, 200}, []float64{0, 20, 0}
	for i := range 3 {
		programme := segmentsynth.Programme{}
		if colds[i] > 0 {
			programme = append(programme, segmentsynth.Part{Signal: segmentsynth.Speech(uint64(100 + i)), Length: colds[i]})
		}
		programme = append(programme, segmentsynth.Part{Signal: intro, Length: 50}, segmentsynth.Part{Signal: segmentsynth.Silence(), Length: 1},
			segmentsynth.Part{Signal: segmentsynth.Speech(uint64(1100 + i)), Length: bodies[i]}, segmentsynth.Part{Signal: credits, Length: 40})
		if tails[i] > 0 {
			programme = append(programme, segmentsynth.Part{Signal: segmentsynth.Speech(uint64(2100 + i)), Length: tails[i]})
		} else {
			programme = append(programme, segmentsynth.Part{Signal: segmentsynth.Silence(), Length: 2})
		}
		pcm := filepath.Join(root, "episode.pcm")
		if err := os.WriteFile(pcm, programme.Render(16000), 0600); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(season, fmt.Sprintf("Example Show S01E%02d.mkv", i+1))
		args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=320x180:rate=5:duration=%.3f", programme.Duration()),
			"-f", "s16le", "-ar", "16000", "-ac", "1", "-i", pcm, "-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "38",
			"-g", "50", "-c:a", "aac", "-b:a", "96k", "-ac", "2", "-ar", "48000", "-shortest", file}
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	d, err := db.Open(ctx, filepath.Join(root, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lib, err := d.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil || stats.Probed != 3 {
		t.Fatal(stats, err)
	}
	docs, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := &processpool.Supervisor{Limit: 2}
	s := &Service{DB: d, Settings: docs, Now: time.Now, Tools: func(context.Context) (*processpool.Supervisor, string, error) { return supervisor, ffmpeg, nil }}
	if err := s.Kind().Run(ctx, jobs.Work{Request: jobs.Request{Target: jobs.Target{Library: lib.PublicID}}, Progress: func(int64, int64) {}}); err != nil {
		t.Fatal(err)
	}
	entries, err := d.SegmentEntries(ctx, lib.PublicID, "", 0, 25)
	if err != nil || len(entries) != 3 {
		t.Fatal(entries, err)
	}
	for _, e := range entries {
		markers, err := d.SegmentMarkers(ctx, e.MediaID)
		if err != nil || e.State != "complete" {
			t.Fatal(e, markers, err)
		}
		kinds := map[db.MarkerKind]bool{}
		for _, m := range markers {
			if m.Source != db.MarkerConfirmed {
				continue
			}
			kinds[m.Kind] = true
			if m.Kind == db.MarkerIntro && math.Abs(float64(m.StartMS)-colds[e.Number-1]*1000) > 1000 {
				t.Fatal("intro outside the known opening", e.Number, m)
			}
		}
		if !kinds[db.MarkerIntro] || !kinds[db.MarkerCredits] {
			t.Fatal("job did not publish both confirmed detections", e.Number, markers)
		}
	}
}
