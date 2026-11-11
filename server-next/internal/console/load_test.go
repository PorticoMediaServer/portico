package console

import (
	"runtime"
	"testing"
	"time"
)

// The live chart holds the last two minutes as sampled; the day chart holds an average for each
// of the last 24 hours, the current one so far, and forgets older hours. On Linux the machine's
// processor and memory are read from the second sample on. Nothing older is held.
func TestLoadKeepsTwoMinutesLiveAndADayOfHourlyAverages(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	streams := 0
	l := &Load{Now: func() time.Time { return now }, Streams: func() (int, int, int) { return 1000 * streams, 0, streams }}
	sampleFor := func(span time.Duration, n int) {
		streams = n
		for end := now.Add(span); now.Before(end); now = now.Add(loadEvery) {
			l.Sample()
		}
	}
	// Two hours a day and a half ago with three streams, then a quiet hour, then one stream.
	sampleFor(2*time.Hour, 3)
	now = now.Add(34 * time.Hour)
	sampleFor(time.Hour, 0)
	sampleFor(30*time.Minute, 1)
	at := now.Add(-loadEvery) // the last sample

	live := l.Live(at)
	if got := len(live["conversions"]); got != int(liveSpan/loadEvery) {
		t.Errorf("the live chart holds %d samples, not two minutes of them", got)
	}
	if last := live["homeMbps"][len(live["homeMbps"])-1]; !last.At.Equal(at) || last.Value != 1 {
		t.Errorf("the newest live point is %+v", last)
	}

	from, day := l.Day(at)
	if want := at.Truncate(time.Hour).Add(-23 * time.Hour); !from.Equal(want) {
		t.Errorf("the day starts at %v, not %v", from, want)
	}
	points := day["conversions"]
	if len(points) != 2 {
		t.Fatalf("the day holds %+v; the hours from a day and a half ago should be gone", points)
	}
	if !points[0].At.Equal(at.Truncate(time.Hour).Add(-time.Hour)) || points[0].Value != 0 {
		t.Errorf("the quiet hour reads %+v", points[0])
	}
	if !points[1].At.Equal(at.Truncate(time.Hour)) || points[1].Value != 1 {
		t.Errorf("the current hour so far reads %+v", points[1])
	}
	// What is held stays bounded however long the server runs.
	if len(l.hours) != 2 || len(l.live) != int(liveSpan/loadEvery) {
		t.Errorf("%d hours and %d samples are held", len(l.hours), len(l.live))
	}

	if runtime.GOOS == "linux" {
		for _, name := range []string{"cpu", "processCPU", "memory", "processMemory"} {
			got := live[name]
			if len(got) == 0 {
				t.Errorf("%s was not read from this machine", name)
				continue
			}
			if v := got[len(got)-1].Value; v < 0 || v > 100 {
				t.Errorf("%s reads %v percent", name, v)
			}
		}
	}
}
