package playbackv1

import (
	"fmt"
	"testing"
)

// A2: the duration summary was keyed on the queue revision, which every
// advance moves, so each advance in a large queue walked every entry again.
// Its key is now the queue's membership: playing through the queue keeps it,
// and every change to what the queue holds moves it.
func TestQueueDurationKeyIgnoresThePlayhead(t *testing.T) {
	base := func() *layout {
		return &layout{
			q:        queueRow{id: "q", revision: 7, current: 3, session: "s1", currentEntry: "e3", shuffle: true, seed: 11, lap: 0, domain: 2, first: 1},
			segments: []segment{{id: "g1", snapshot: "a", first: 0, count: 100, removed: 1, state: "ready", built: 100}, {id: "g2", snapshot: "b", first: 0, count: 50, state: "ready", built: 50}},
			removals: map[string][]int64{"a": {42}},
			spans:    []orderSpan{{start: 0, count: 2}, {source: true, start: 2, count: 147}},
		}
	}
	want := base().membership()
	played := base()
	played.q.revision, played.q.current, played.q.session, played.q.currentEntry, played.q.automatic = 9, 5, "s2", "e5", 2
	if got := played.membership(); got != want {
		t.Fatal("an advance changed the duration key")
	}
	for name, edit := range map[string]func(*layout){
		"removal":       func(l *layout) { l.segments[0].removed, l.removals["a"] = 2, []int64{42, 43} },
		"other removal": func(l *layout) { l.removals["a"] = []int64{41} },
		"enqueue":       func(l *layout) { l.segments = append(l.segments, segment{id: "g3", snapshot: "c", count: 1, state: "ready", built: 1}) },
		"built":         func(l *layout) { l.segments[1].state, l.segments[1].built = "building", 20 },
		"source":        func(l *layout) { l.segments[1].sourceRevision = "r2" },
		"reshuffle":     func(l *layout) { l.q.seed = 12 },
		"new lap":       func(l *layout) { l.q.lap = 1 },
		"play order":    func(l *layout) { l.spans[1].count = 146 },
	} {
		l := base()
		edit(l)
		if l.membership() == want {
			t.Errorf("%s kept the duration key", name)
		}
	}
}

// The cache keeps the most recently used summaries, rather than starting over
// whenever it fills.
func TestQueueDurationCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	var c queueDurationCache
	for i := 0; i < queueDurationLimit; i++ {
		c.put(fmt.Sprint(i), queueDurationSummary{ms: int64(i), known: true})
	}
	if v, ok := c.get("0"); !ok || v.ms != 0 {
		t.Fatalf("first summary: %+v %v", v, ok)
	}
	c.put("new", queueDurationSummary{ms: 1000, known: true})
	if _, ok := c.get("1"); ok {
		t.Fatal("the least recently used summary was kept")
	}
	for _, key := range []string{"0", "2", fmt.Sprint(queueDurationLimit - 1), "new"} {
		if _, ok := c.get(key); !ok {
			t.Fatalf("summary %s was evicted", key)
		}
	}
	if len(c.values) != queueDurationLimit || c.order.Len() != queueDurationLimit {
		t.Fatalf("%d summaries, %d in order", len(c.values), c.order.Len())
	}
}
