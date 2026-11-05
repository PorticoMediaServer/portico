package search_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestSearchRequestsSupersedeKeepsPhysicalCustody(t *testing.T) {
	g, e := search.NewSearchRequests(2)
	if e != nil {
		t.Fatal(e)
	}
	a, e := g.Begin(context.Background(), "session-a")
	if e != nil {
		t.Fatal(e)
	}
	b, e := g.Begin(context.Background(), "session-a")
	if e != nil {
		t.Fatal(e)
	}
	if a.Current() || !b.Current() || !errors.Is(a.Context().Err(), context.Canceled) || a.Generation() >= b.Generation() {
		t.Fatal("same-session supersede")
	}
	if _, e = g.Begin(context.Background(), "session-b"); !errors.Is(e, search.ErrReadCapacity) {
		t.Fatal("supersede refunded unretired request")
	}
	a.Finish()
	c, e := g.Begin(context.Background(), "session-b")
	if e != nil {
		t.Fatal(e)
	}
	a.Finish()
	if !b.Current() || !c.Current() {
		t.Fatal("old finish removed new request / another session")
	}
	b.Cancel()
	if _, e = g.Begin(context.Background(), "session-a"); !errors.Is(e, search.ErrReadCapacity) {
		t.Fatal("cancel refunded unretired request")
	}
	b.Finish()
	d, e := g.Begin(context.Background(), "session-a")
	if e != nil {
		t.Fatal(e)
	}
	if !d.Current() || !c.Current() || d.Generation() <= c.Generation() {
		t.Fatal("request generation/session isolation")
	}
	g.Close()
	if c.Current() || d.Current() || c.Context().Err() == nil || d.Context().Err() == nil {
		t.Fatal("close did not cancel")
	}
	if _, e = g.Begin(context.Background(), "session-a"); !errors.Is(e, search.ErrReadCapacity) {
		t.Fatal("closed group accepted work")
	}
	c.Finish()
	d.Finish()
}

func TestSearchRequestsRefusalAndConcurrentCancellation(t *testing.T) {
	if _, e := search.NewSearchRequests(0); !errors.Is(e, search.ErrReadCapacity) {
		t.Fatal("zero capacity")
	}
	g, _ := search.NewSearchRequests(2)
	for _, key := range []string{"", strings.Repeat("x", 1025)} {
		if _, e := g.Begin(context.Background(), key); !errors.Is(e, search.ErrSessionKey) {
			t.Fatal("invalid session key")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := g.Begin(ctx, "a"); !errors.Is(e, context.Canceled) {
		t.Fatal("canceled request admitted")
	}
	r, e := g.Begin(context.Background(), "a")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 30; j++ {
				r.Cancel()
				_ = r.Current()
				r.Finish()
			}
		})
	}
	wg.Wait()
	q, e := g.Begin(context.Background(), "a")
	if e != nil || !q.Current() {
		t.Fatal("retired slot unavailable")
	}
	r.Finish()
	if !q.Current() {
		t.Fatal("late finish revoked replacement")
	}
	q.Finish()
	g.Close()
}
