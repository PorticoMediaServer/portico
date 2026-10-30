package playback

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMusicPrivatePrefetchAggregateNeverEvictsLiveBudgetsR36(t *testing.T) {
	s := &Service{}
	var total atomic.Int64
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() { defer wg.Done(); total.Add(s.PrefetchTake("same", 97, 1000)) }()
	}
	wg.Wait()
	if total.Load() != 1000 || s.PrefetchCharge("same", 1, 1000) {
		t.Fatal(total.Load())
	}
	for i := 0; i < 4095; i++ {
		if !s.PrefetchCharge(fmt.Sprint(i), 1, 1) {
			t.Fatal(i)
		}
	}
	if s.PrefetchCharge("extra", 1, 1) || s.PrefetchCharge("same", 1, 1000) {
		t.Fatal("full map reset an active grant")
	}
	s.ForgetPrefetch("0")
	if !s.PrefetchCharge("extra", 1, 1) {
		t.Fatal("durable retirement did not free entry")
	}
	if s.PrefetchCharge("same", -1, 1000) {
		t.Fatal("negative charge accepted")
	}
	s.PrefetchRefund("same", 12)
	if n := s.PrefetchTake("same", 20, 1000); n != 12 {
		t.Fatal(n)
	}
	close := s.RetainPrefetch("same")
	s.ForgetPrefetch("same")
	if n := s.PrefetchTake("same", 1000, 1000); n != 0 {
		t.Fatal("stop regranted live writer", n)
	}
	close()
	if _, ok := s.prefetched["same"]; ok {
		t.Fatal("retired writer counter leaked")
	}
}
