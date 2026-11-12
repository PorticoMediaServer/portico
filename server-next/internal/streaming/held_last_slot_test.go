package streaming

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// filmWithLongerSound writes a film whose sound runs half a second past its picture (11.9 s
// of picture, 12.4 s of sound, as a film's often does), MPEG-4 in Matroska. Its length says
// there is a short tail past the 12 s mark, but the converter cuts only on a picture frame,
// so it writes no tail file: the last slot is whole as the converter leaves it. With damaged
// the middle of the file has 48 bytes changed, which the converter complains of on standard
// error and converts past. tracks is the number of sound tracks.
func (r *mediaRig) filmWithLongerSound(name string, damaged bool, tracks int) string {
	r.t.Helper()
	film := filepath.Join(r.library, name)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=11.9",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=12.4", "-map", "0:v"}
	for range tracks {
		args = append(args, "-map", "1:a")
	}
	args = append(args, "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2", "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3", film)
	if out, err := exec.Command(r.ffmpeg, args...).CombinedOutput(); err != nil {
		r.t.Fatalf("generate: %v\n%s", err, out)
	}
	if damaged {
		r.damage(film)
	}
	return film
}

// damage changes 48 bytes in the middle of a file.
func (r *mediaRig) damage(path string) {
	r.t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	for i := len(raw) / 2; i < len(raw)/2+48; i++ {
		raw[i] ^= 0x5a
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		r.t.Fatal(err)
	}
}

// countRuns runs the real converter for the stream's picture and counts each run.
func (r *mediaRig) countRuns() *atomic.Int64 {
	runs := &atomic.Int64{}
	r.streams.hls.execute = func(ctx context.Context, job mediaexec.Job) (error, string) {
		runs.Add(1)
		cmd, err := mediaexec.CommandContext(ctx, job)
		if err != nil {
			return &notStarted{err}, err.Error()
		}
		said := &tailWriter{limit: 4096}
		cmd.Stderr = said
		return childprocess.Run(ctx, cmd), said.String()
	}
	return runs
}

// lastSlotsPlay asks for the last slot of every playlist of a stream, as a player at the end
// of the title does, and fails unless each is given out within a few seconds.
func (r *mediaRig) lastSlotsPlay(grant string) {
	r.t.Helper()
	for _, p := range r.playlists(grant) {
		if len(p.segments) == 0 {
			continue
		}
		last := p.segments[len(p.segments)-1]
		deadline := time.Now().Add(30 * time.Second)
		for {
			response := r.get(context.Background(), grant, last, "")
			if response.Code == http.StatusOK {
				break
			}
			if response.Code != http.StatusServiceUnavailable || time.Now().After(deadline) {
				r.t.Fatalf("the last slot %s of %s was not given out: %d %s", last, p.name, response.Code, response.Body.String())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// Review desk B, #2902, taken in as written (only the wait is shorter and the runs are
// counted). A title whose length says there is a short tail past the last six-second mark,
// but whose picture ends before that mark, has no tail file: its last slot is whole as the
// converter left it once the converter has finished the title. A converter that finished
// cleanly but wrote a line to standard error on the way (a damaged frame, the commonest thing
// FFmpeg says) used to be taken as not having finished, so the last slot was never given out
// and a new converter started about every half second for as long as the player asked.
func TestADamagedFrameDoesNotHoldTheLastSlotForEver(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		name := "a clean film"
		if damaged {
			name = "a film with one damaged stretch"
		}
		t.Run(name, func(t *testing.T) {
			rig := newMediaRig(t)
			runs := rig.countRuns()
			film := rig.filmWithLongerSound("film.mkv", damaged, 1)
			stream, plan := rig.start("film", film, 0, nil)
			if plan.VideoAction != "convert" {
				t.Fatalf("not converted: %s", plan.Strategy)
			}
			t.Logf("planned length %.3fs: %d listed slots, %d written", plan.Duration, hlsSegmentCount(plan.Duration), hlsWrittenSlots(plan.Duration))
			rig.ready("film")
			rig.lastSlotsPlay(stream.Grant)
			// The first run converts the whole film; one more is allowed for a player that
			// asks for the end before the first has reached it.
			if n := runs.Load(); n > 2 {
				t.Fatalf("the converter ran %d times for a 12 s film", n)
			}
			rig.streams.Stop("film")
		})
	}
}

// The same with a second sound track, which is a rendition of its own with its own
// converter: its last slot is decided the same way.
func TestADamagedFrameDoesNotHoldAnAudioTracksLastSlot(t *testing.T) {
	rig := newMediaRig(t)
	film := rig.filmWithLongerSound("two-tracks.mkv", true, 2)
	stream, plan := rig.start("film", film, 0, nil)
	if len(plan.Renditions) < 2 {
		t.Fatalf("two sound tracks gave %d renditions", len(plan.Renditions))
	}
	rig.ready("film")
	rig.lastSlotsPlay(stream.Grant)
	rig.streams.Stop("film")
}

// A viewer who starts near the end (resuming, or a seek to the last minute) starts the
// converter at the last slot: that slot is given out, whole, whether the title has a tail
// to carry (a 12.4 s film whose picture runs to its end) or none (the damaged film above).
func TestASeekNearTheEndPlaysTheLastSlot(t *testing.T) {
	t.Run("with a tail to carry", func(t *testing.T) {
		rig := newMediaRig(t)
		runs := rig.countRuns()
		film := rig.generate("film-12.4.avi", 16, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3", "-t", "12.4")
		stream, plan := rig.start("film", film, 11000, nil)
		if plan.VideoAction != "convert" || hlsSegmentCount(plan.Duration) != 2 {
			t.Fatalf("%s with %d slots", plan.Strategy, hlsSegmentCount(plan.Duration))
		}
		rig.lastSlotsPlay(stream.Grant)
		rig.requireEnds(rig.trackEnds(film), rig.playedEnds(stream.Grant))
		if n := runs.Load(); n > 3 {
			t.Fatalf("the converter ran %d times", n)
		}
		rig.streams.Stop("film")
	})
	t.Run("with no tail file", func(t *testing.T) {
		rig := newMediaRig(t)
		runs := rig.countRuns()
		film := rig.filmWithLongerSound("film.mkv", true, 1)
		stream, _ := rig.start("film", film, 11000, nil)
		rig.lastSlotsPlay(stream.Grant)
		if n := runs.Load(); n > 2 {
			t.Fatalf("the converter ran %d times", n)
		}
		rig.streams.Stop("film")
	})
}

// A song with a damaged stretch, converted for a browser, plays its last slot: the converter
// complains of the damage and converts the song to its end.
func TestADamagedSongPlaysItsLastSlot(t *testing.T) {
	rig := newMediaRig(t)
	song := rig.generate("song.mka", 16, "-map", "1:a", "-c:a", "ac3", "-t", "12.6")
	rig.damage(song)
	stream, plan := rig.start("song", song, 0, nil)
	if plan.AudioAction != "convert" || plan.Mode != "hls" {
		t.Fatalf("AC-3 for a browser was not converted over HLS: %s audio %s", plan.Strategy, plan.AudioAction)
	}
	rig.ready("song")
	rig.lastSlotsPlay(stream.Grant)
	rig.streams.Stop("song")
}

// The same films on a WebDAV server, read through the loopback bridge: the last slot of the
// picture and of each sound track is given out, whether there is a tail to carry or none, and
// the film with a tail plays to its last sample. A source that never answers ends in the
// plain failure, not in "still being prepared" for as long as the player asks.
func TestARemoteFilmPlaysItsLastSlot(t *testing.T) {
	rig := newMediaRig(t)
	damaged := rig.filmWithLongerSound("damaged.mkv", true, 2)
	tail := rig.generate("tail.avi", 16, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3", "-t", "12.4")
	files := map[string]string{}
	for _, path := range []string{damaged, tail} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files["Films/"+filepath.Base(path)] = string(raw)
	}
	dav := remotetest.NewDAV(t, files)
	src, _ := remote.NewWebDAV(dav.URL, dav.User, dav.Password)
	storage := remote.Throttled(src, remote.NewThrottle(remote.Limits{Connections: 4, PerSecond: 50}))
	start := func(session, path string) (Stream, DeliveryPlan) {
		request, info := rig.request(path)
		plan, err := Plan(request)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := rig.streams.Start(context.Background(), StartRequest{SessionID: session, Plan: plan, Request: &request,
			Source: RemoteFile(storage, "Films/"+filepath.Base(path), info.Size(), info.ModTime())})
		if err != nil {
			t.Fatalf("start %s: %v", path, err)
		}
		return stream, plan
	}

	stream, plan := start("damaged", damaged)
	if plan.VideoAction != "convert" || len(plan.Renditions) < 2 {
		t.Fatalf("%s with %d renditions", plan.Strategy, len(plan.Renditions))
	}
	rig.ready("damaged")
	rig.lastSlotsPlay(stream.Grant)
	rig.streams.Stop("damaged")

	stream, _ = start("tail", tail)
	rig.ready("tail")
	rig.lastSlotsPlay(stream.Grant)
	rig.requireEnds(rig.trackEnds(tail), rig.playedEnds(stream.Grant))
	rig.streams.Stop("tail")

	// From its first read on, the source never answers again.
	dav.Breaks.Store(1 << 30)
	stream, _ = start("gone", tail)
	name := "segment-000001.ts"
	deadline := time.Now().Add(150 * time.Second)
	for {
		response := rig.get(context.Background(), stream.Grant, name, "")
		if response.Code == http.StatusOK {
			t.Fatalf("%s was made from a source that never answered", name)
		}
		if response.Code != http.StatusServiceUnavailable {
			t.Logf("a source that never answers ends in %d %s", response.Code, strings.TrimSpace(response.Body.String()))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a source that never answers left %s still being prepared after 150 s", name)
		}
		time.Sleep(500 * time.Millisecond)
	}
	rig.streams.Stop("gone")
}
