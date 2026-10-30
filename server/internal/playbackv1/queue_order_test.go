package playbackv1

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"testing"
)

func TestShuffledSelectorGrowthRetainsPlacement(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms)VALUES('q','device','d','local','a','p',1,1,1)`)
	l := &layout{q: queueRow{id: "q", shuffle: true, domain: 2, first: 0, seed: 42}, segments: []segment{{snapshot: "body", count: 2}, {snapshot: "growing", count: 2}, {snapshot: "tail", count: 1}}, spans: []orderSpan{{start: 0, count: 1}, {source: true, start: 2, count: 2}, {start: 1, count: 1}, {source: true, start: 4, count: 1}}}
	tx, e := c.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = l.extendShuffleOrder(context.Background(), tx, "growing", 4); e != nil {
		t.Fatal(e)
	}
	l.segments[1].count = 4
	if l.total() != 7 {
		t.Fatal(l.total())
	}
	want := []int64{0, 2, 3, 4, 5, 1, 6}
	for i, src := range want {
		if got := l.orderToSource(int64(i)); got != src {
			t.Fatalf("position %d: %d, want %d", i, got, src)
		}
	}
}
