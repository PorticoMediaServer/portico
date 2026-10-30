package telemetry

import (
	"sync"
	"time"
)

// Traffic counts successfully written media bytes, not configured encoder rates
// or the player's estimated connection bandwidth. One short ring per stream.
type Traffic struct {
	mu       sync.Mutex
	streams  map[string]*trafficStream
	previous int64
}
type trafficStream struct {
	slots   [6]trafficSlot
	address string
	remote  bool
	last    int64
}
type trafficSlot struct {
	second int64
	bytes  int64
}

func (t *Traffic) Add(id, address string, remote bool, n int, at time.Time) {
	if id == "" || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams == nil {
		t.streams = map[string]*trafficStream{}
	}
	sec := at.Unix()
	s := t.streams[id]
	if s == nil {
		s = &trafficStream{}
		t.streams[id] = s
	}
	slot := &s.slots[sec%6]
	if slot.second != sec {
		*slot = trafficSlot{second: sec}
	}
	slot.bytes += int64(n)
	s.address, s.remote, s.last = address, remote, sec
}
func (t *Traffic) Rate(id string, at time.Time) (float64, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.streams[id]
	if s == nil {
		return 0, ""
	}
	var bytes int64
	sec := at.Unix()
	for _, slot := range s.slots {
		if slot.second > sec-5 && slot.second <= sec {
			bytes += slot.bytes
		}
	}
	return float64(bytes) * 8 / 5000, s.address // kbps, five seconds of actual writes
}
func (t *Traffic) Sample(at time.Time) (Metric, Metric) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sec := at.Unix()
	var home, away int64
	for id, s := range t.streams {
		if sec-s.last > 60 {
			delete(t.streams, id)
			continue
		}
		for _, slot := range s.slots {
			if slot.second > sec-2 && slot.second <= sec {
				if s.remote {
					away += slot.bytes
				} else {
					home += slot.bytes
				}
			}
		}
	}
	return Available(float64(home) * 8 / 2000000), Available(float64(away) * 8 / 2000000)
}
