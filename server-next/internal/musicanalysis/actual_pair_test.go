package musicanalysis

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/sonic"
)

// Reading a song takes two processes, the decoder and the model's helper, in the background
// pool. The runtime admits a song's decoder first and lets its helper join that place, so
// SongsAtOnce songs read side by side never hold every place while their decoders wait: on a
// pool of one place (a two- or three-thread machine) and of two (four cores) every song is read.
// With the real model, helper and ffmpeg only, as the measurement run supplies them.
func TestActualSongsSideBySideOnSmallPools(t *testing.T) {
	helper, model, ffmpeg := os.Getenv("PORTICO_SONIC_HELPER_TEST"), os.Getenv("PORTICO_SONIC_MODEL_TEST"), os.Getenv("PORTICO_FFMPEG_TEST")
	songs := strings.Split(os.Getenv("PORTICO_SONIC_SONGS"), string(os.PathListSeparator))
	if helper == "" || model == "" || ffmpeg == "" || songs[0] == "" {
		t.Skip("the model, its helper, ffmpeg and songs are supplied by the measurement run")
	}
	if _, err := mediaexec.Configure(mediaexec.Options{StateDir: t.TempDir(), SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	tracks := make([]db.TrackToAnalyse, len(songs))
	for n, song := range songs {
		info, err := os.Stat(song)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command(filepath.Join(filepath.Dir(ffmpeg), "ffprobe"), "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", song).Output()
		seconds, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
		tracks[n] = db.TrackToAnalyse{ItemID: int64(n + 1), Path: song, Size: info.Size(), DurationMS: int64(seconds * 1000)}
	}
	for _, pool := range []int{1, 2} {
		sup := &processpool.Supervisor{Limit: pool}
		runtime, err := sonic.OpenRuntime(sonic.RuntimeOptions{Helper: helper, ModelDirectory: model, Supervisor: sup,
			Libraries: filepath.SplitList(os.Getenv("PORTICO_SONIC_LIBRARIES"))})
		if err != nil {
			t.Fatal(err)
		}
		s := &Service{}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		started := time.Now()
		for from := 0; from < len(tracks); from += SongsAtOnce {
			var wg sync.WaitGroup
			for _, tr := range tracks[from:min(from+SongsAtOnce, len(tracks))] {
				wg.Go(func() {
					if a, err := s.analyse(ctx, runtime, sup, ffmpeg, tr); err != nil || a.Failed != "" {
						t.Errorf("pool of %d: %s: %v %q (%+v)", pool, tr.Path, err, a.Failed, sup.Stats())
					}
				})
			}
			wg.Wait()
		}
		cancel()
		t.Logf("pool of %d: %d songs, %d at a time, in %.1f s", pool, len(tracks), SongsAtOnce, time.Since(started).Seconds())
	}
}
