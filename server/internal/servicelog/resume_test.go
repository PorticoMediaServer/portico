package servicelog

import (
	"sync"
	"testing"
)

func TestLogResumeAndGap(t *testing.T) {
	r := New(Options{Capacity: 3})
	defer r.Close()
	for i := 0; i < 4; i++ {
		r.Record("info", "server", "entry")
	}
	after := int64(2)
	replay, reset, ch, cancel := r.SubscribeAfter("info", &after)
	defer cancel()
	if reset || len(replay) != 2 || replay[0].Sequence != 3 {
		t.Fatal(replay, reset)
	}
	r.Record("info", "server", "next")
	if record := <-ch; record.Sequence != 5 {
		t.Fatal(record)
	}
	after = 0
	replay, reset, _, stop := r.SubscribeAfter("info", &after)
	defer stop()
	if !reset || len(replay) != 3 {
		t.Fatal(replay, reset)
	}
	other := New(Options{})
	defer other.Close()
	if seq, err := other.EventSequence(r.EventID(3)); err != nil || seq != -1 {
		t.Fatal(seq, err)
	}
}
func TestLogSubscriptionConcurrentRetirement(t *testing.T) {
	r := New(Options{})
	defer r.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, cancel := r.Subscribe("info")
				r.Record("info", "server", "event")
				cancel()
			}
		}()
	}
	wg.Wait()
}
