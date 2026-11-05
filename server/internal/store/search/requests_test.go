package search_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

//go:noinline
func admitBackedSessionKey(g *search.SearchRequests) (*search.SearchRequest, error) {
	backing := strings.Repeat("s", 16<<20)
	return g.Begin(context.Background(), backing[:16])
}

func TestSearchRequestsOwnBoundedSessionKey(t *testing.T) {
	// Keep the admitted request alive after dropping the caller's backing
	// string. Visible key length alone must not retain the 16 MiB allocation.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	g, err := search.NewSearchRequests(1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := admitBackedSessionKey(g)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(r)
	runtime.KeepAlive(g)
	r.Finish()
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if retained > 1<<20 {
		t.Fatalf("short session key retained %d heap bytes (limit 1 MiB)", retained)
	}
	t.Logf("short session key retained %d heap bytes", retained)
}

func TestSearchRequestsStaleFinishPreservesSupersedeChain(t *testing.T) {
	g, err := search.NewSearchRequests(3)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Begin(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.Begin(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	a.Finish()
	a.Finish()
	c, err := g.Begin(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	if b.Current() || !errors.Is(b.Context().Err(), context.Canceled) || !c.Current() {
		t.Fatal("stale A Finish detached B from the A-B-C supersede chain")
	}
	b.Finish()
	b.Finish()
	// A fourth admission probes the directory after stale B's completion;
	// merely asking c.Current would not prove c is still in that directory.
	d, err := g.Begin(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	if c.Current() || !errors.Is(c.Context().Err(), context.Canceled) || !d.Current() {
		t.Fatal("stale B Finish detached C from the next supersede")
	}
	c.Finish()
	d.Finish()
	g.Close()
}

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
