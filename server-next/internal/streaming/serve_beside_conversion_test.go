package streaming

import (
	"context"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"
)

// A film played as it is or repackaged starts, and seeks, as fast beside a conversion that has
// the whole machine as it does alone: serving costs next to nothing, and the converter's
// threads must not crowd it out. A measurement, not a check of this machine's speed: it runs
// when PORTICO_MEASURE_SERVING=1 and reports both times.
func TestServingStartsAsFastBesideAWholeMachineConversion(t *testing.T) {
	if os.Getenv("PORTICO_MEASURE_SERVING") != "1" {
		t.Skip("a measurement; set PORTICO_MEASURE_SERVING=1 to run it")
	}
	rig := newMediaRig(t)
	remux := rig.generate("remux.mkv", 30, "-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac")
	direct := rig.generate("direct.mp4", 30, "-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac", "-movflags", "+faststart")
	heavy := rig.generate("heavy.mkv", 30, "-vf", "scale=3840:2160,format=yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast",
		"-x265-params", "log-level=error", "-c:a", "aac")
	// time to the first segment of a repackaged start at position, or the first bytes of the
	// original by range.
	first := func(session, path string, positionMs int64) time.Duration {
		began := time.Now()
		stream, plan := rig.start(session, path, positionMs, nil)
		if stream.Mode == "direct" {
			if response := rig.get(context.Background(), stream.Grant, "original", "bytes=0-65535"); response.Code != http.StatusPartialContent {
				t.Fatalf("the original answered %d", response.Code)
			}
		} else {
			if plan.VideoAction != "copy" {
				t.Fatalf("%s is not repackaged: %s", path, plan.Strategy)
			}
			p := rig.playlists(stream.Grant)[0]
			index := int(positionMs / 1000 / HLSSegmentSeconds)
			if len(rig.fetch(stream.Grant, p.segments[index])) == 0 {
				t.Fatal("the segment was empty")
			}
		}
		took := time.Since(began)
		rig.streams.Stop(session)
		return took
	}
	median := func(path string, positionMs int64) time.Duration {
		var times []time.Duration
		for i := range 5 {
			times = append(times, first("s"+string(rune('a'+i)), path, positionMs))
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		return times[2]
	}
	measure := func() map[string]time.Duration {
		return map[string]time.Duration{"direct start": median(direct, 0), "repackaged start": median(remux, 0), "repackaged seek to 18 s": median(remux, 18000)}
	}
	alone := measure()
	// A conversion with the whole machine: the first encode for playback gets every thread.
	stream, plan := rig.start("heavy", heavy, 0, nil)
	if plan.VideoAction != "convert" {
		t.Fatalf("the heavy film is not converted: %s", plan.Strategy)
	}
	rig.ready("heavy")
	// Keep it converting: a request for a segment ahead, answered or not once it is stopped.
	ahead := rig.playlists(stream.Grant)[0].segments[3]
	go rig.get(context.Background(), stream.Grant, ahead, "")
	time.Sleep(2 * time.Second)
	beside := measure()
	status, _ := rig.streams.Status("heavy")
	rig.streams.Stop("heavy")
	t.Logf("the conversion beside: %s, %+v", plan.OutputVideoCodec, status.Execution)
	for _, name := range []string{"direct start", "repackaged start", "repackaged seek to 18 s"} {
		t.Logf("%-24s alone %6.0f ms, beside the conversion %6.0f ms", name, float64(alone[name].Microseconds())/1000, float64(beside[name].Microseconds())/1000)
	}
}
