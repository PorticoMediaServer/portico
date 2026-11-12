package musicanalysis

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/sonic"
)

func TestMain(m *testing.M) {
	if handled, err := mediaexec.RunHelper(os.Args[1:]); handled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// With the real model, helper and ffmpeg, each song given is read into an embedding and 50
// style scores, and the time and processor time each took are logged: the measurement the
// owner's hours for heavy work are judged by. It runs only where they are supplied.
func TestActualSongsAreRead(t *testing.T) {
	helper, model, ffmpeg := os.Getenv("PORTICO_SONIC_HELPER_TEST"), os.Getenv("PORTICO_SONIC_MODEL_TEST"), os.Getenv("PORTICO_FFMPEG_TEST")
	songs := strings.Split(os.Getenv("PORTICO_SONIC_SONGS"), string(os.PathListSeparator))
	if helper == "" || model == "" || ffmpeg == "" || songs[0] == "" {
		t.Skip("the model, its helper, ffmpeg and songs are supplied by the measurement run")
	}
	if _, err := mediaexec.Configure(mediaexec.Options{StateDir: t.TempDir(), SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	sup := &processpool.Supervisor{Limit: 2}
	runtime, err := sonic.OpenRuntime(sonic.RuntimeOptions{Helper: helper, ModelDirectory: model, Supervisor: sup,
		Libraries: filepath.SplitList(os.Getenv("PORTICO_SONIC_LIBRARIES"))})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	var audio, wall, cpu time.Duration
	for n, song := range songs {
		info, err := os.Stat(song)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(filepath.Join(filepath.Dir(ffmpeg), "ffprobe"), "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", song).Output()
		seconds, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
		if err != nil || perr != nil {
			t.Fatalf("the length of %s: %v %v", song, err, perr)
		}
		before := children()
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		a, err := s.analyse(ctx, runtime, sup, ffmpeg, db.TrackToAnalyse{ItemID: int64(n + 1), Path: song, Size: info.Size(), DurationMS: int64(seconds * 1000)})
		cancel()
		took, used := time.Since(started), children()-before
		if err != nil || a.Failed != "" || len(a.Embedding) != sonic.Dimensions*4 || len(a.Tags) != sonic.TagCount*4 {
			t.Fatalf("%s: %v %q", song, err, a.Failed)
		}
		t.Logf("%s: %.0f s of audio read in %.2f s, %.2f s of processor time", filepath.Base(song), seconds, took.Seconds(), used.Seconds())
		audio, wall, cpu = audio+time.Duration(seconds*float64(time.Second)), wall+took, cpu+used
	}
	t.Logf("%d songs, %.0f min of audio: %.2f s and %.2f s of processor time a song; %.3f s of processor time per minute of audio",
		len(songs), audio.Minutes(), wall.Seconds()/float64(len(songs)), cpu.Seconds()/float64(len(songs)), cpu.Seconds()/audio.Minutes())
}

// children is the processor time of the processes this test started that have ended.
func children() time.Duration {
	var u syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &u)
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}
