package main

import (
	"context"
	"time"
)

func startPublisherReader() func() {
	if !boundedReaders {
		return func() {}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		db := open(databasePath)
		defer db.Close()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			q := []string{"a", "al", "jo", "東", "王"}[i%5]
			i++
			t := time.Now()
			seq, active := cpSequence.Load(), cpRunning.Load()
			_, snap, e := shortSuggestion(context.Background(), db, q, false)
			d := map[string]any{"query": q, "ms": float64(time.Since(t).Microseconds()) / 1000, "snapshot": snap, "checkpoint_overlap": active || cpRunning.Load() || seq != cpSequence.Load()}
			if e != nil {
				d["error"] = e.Error()
				emit("concurrent_read_error", d)
			} else {
				readerCount.Add(1)
				emit("concurrent_tune", d)
			}
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
	return func() { close(stop); <-done }
}
