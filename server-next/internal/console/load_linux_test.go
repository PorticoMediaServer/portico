//go:build linux

package console

import (
	"testing"
	"time"
)

// The machine's processor figure is the share of clock ticks spent busy between two samples.
// The chart test takes its samples back to back, within one tick, so it cannot say whether
// that figure is ever produced. This one can: the reading itself must parse, and two samples
// a few ticks apart must give a figure.
func TestTheMachinesProcessorIsReadBetweenTwoSamples(t *testing.T) {
	if busy, total, ok := readStat(); !ok || total == 0 || busy > total {
		t.Fatalf("/proc/stat was not read: busy %d of %d, ok %v", busy, total, ok)
	}
	var m machine
	start := time.Now()
	m.sample(start, map[string]float64{})
	// Ticks are a hundredth of a second. Wait until the machine's count has moved past the
	// first sample's (read after it, so never behind it), at most a second.
	_, first, _ := readStat()
	for deadline := start.Add(time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, now, _ := readStat(); now > first || time.Now().After(deadline) {
			break
		}
	}
	got := map[string]float64{}
	m.sample(time.Now(), got)
	for _, name := range []string{"cpu", "processCPU"} {
		v, read := got[name]
		if !read || v < 0 || v > 100 {
			t.Errorf("%s between two samples a few ticks apart: %v, read %v", name, v, read)
		}
	}
}
