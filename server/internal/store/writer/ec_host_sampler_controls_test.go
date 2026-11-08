package writer

import (
	"os"
	"testing"
	"time"
)

// The sampler emits a header and batched samples through the asynchronous stream,
// stops and flushes before anything appended after close(), and reports its own
// tick lateness. Liveness only: no wall-clock threshold is asserted.
func TestHostSamplerEmitsBatchedSamplesAndFlushesOnClose(t *testing.T) {
	s, path := asyncControlStream(t)
	t.Setenv("PORTICO_EC_HOST_SAMPLER_MS", "10")
	h := startHostSampler(s, os.TempDir())
	if h == nil {
		t.Fatal("sampler disabled")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		h.mu.Lock()
		n := h.samples
		h.mu.Unlock()
		if n >= 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sampler produced no samples")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.close()
	sum := h.summary().(map[string]any)
	if err := s.append("final", map[string]any{"host_sampler": sum}); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	records := readStreamRecords(t, path)
	var header, samples, finals int
	total := 0
	lastKind := ""
	for _, r := range records {
		lastKind, _ = r["kind"].(string)
		switch lastKind {
		case "host_sampler_start":
			header++
			if r["interval_ns"] != float64(10e6) || r["origin_utc_ns"] == nil {
				t.Fatal("header incomplete", r)
			}
		case "host_samples":
			samples++
			list, _ := r["samples"].([]any)
			total += len(list)
			for _, x := range list {
				m := x.(map[string]any)
				if m["tick_late_ns"].(float64) < -1e6 || m["at_ns"].(float64) <= 0 {
					t.Fatal("bad sample", m)
				}
			}
		case "final":
			finals++
		}
	}
	if header != 1 || samples == 0 || finals != 1 || lastKind != "final" {
		t.Fatalf("stream shape: header=%d batches=%d finals=%d last=%s", header, samples, finals, lastKind)
	}
	if int64(total) != sum["samples"].(int64) {
		t.Fatalf("flush lost samples: stream %d, summary %v", total, sum["samples"])
	}
}

func TestHostSamplerCanBeDisabled(t *testing.T) {
	s, _ := asyncControlStream(t)
	t.Setenv("PORTICO_EC_HOST_SAMPLER_MS", "0")
	if h := startHostSampler(s, os.TempDir()); h != nil {
		t.Fatal("PORTICO_EC_HOST_SAMPLER_MS=0 must disable the sampler")
	}
	var nilSampler *hostSampler
	nilSampler.close() // nil-safe, as the paired component calls it unconditionally
	if nilSampler.summary() != nil {
		t.Fatal("nil sampler has no summary")
	}
	_ = s.close()
}
