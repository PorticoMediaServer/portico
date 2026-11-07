package read

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func rbCacheFixture(t *testing.T) (*rbPreparedCache, governor.Governor, *governor.Authority, *time.Time) {
	t.Helper()
	now := time.Now()
	clock := func() time.Time { return now }
	cfg := governor.Config{Now: clock, MaxGrantsPerClass: 32, Preparation: &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}}
	for c := range cfg.Partitions {
		cfg.Partitions[c] = governor.Amounts{governor.MemoryBytes: 96 << 20, governor.WALBytes: 1 << 20}
	}
	g, a, err := governor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := newRBPreparedCache(a, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, p := range cache.entries {
			if p != nil {
				if err := p.close(); err != nil {
					t.Error(err)
				}
			}
		}
	})
	return cache, g, a, &now
}
func rbCacheBegin(t *testing.T, c *rbPreparedCache, purpose rbPreparedPurpose) *rbPreparedEntry {
	t.Helper()
	key := rbPreparedKey{owner: 1, query: [32]byte{1}}
	p, err := c.begin(t.Context(), key, purpose, c.now().Add(time.Minute), 8192, governor.PublicationFootprint{{Library: 7}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func rbCacheWriter(now time.Time, class governor.Class) governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 399, ID: "cache-publication-test"}, Owner: 2, Class: class, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: now.Add(time.Minute), Budget: governor.UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Need: governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}, Footprint: []governor.Scope{{Library: 7}}}
}
func TestRBPreparedCacheAttachAndFirstHandoff(t *testing.T) {
	c, g, a, now := rbCacheFixture(t)
	p := rbCacheBegin(t, c, rbPreparedNumericFacets)
	original := p.lease.Deadline()
	*now = now.Add(5 * time.Second)
	again, err := c.begin(t.Context(), p.key, p.purpose, now.Add(time.Hour), 16384, governor.PublicationFootprint{{Library: 7}})
	if err != nil || again != p || !p.lease.Deadline().Equal(original) {
		t.Fatal("poll renewed lease or payload", err)
	}
	// This is a pure kernel test. A closed native source/response driver must
	// supply the fresh authorized fence; these values alone prove no authority.
	f := browseFence{libraryID: 7, dataRevision: 1}
	if err := p.capture(f); err != nil {
		t.Fatal(err)
	}
	for _, value := range []rbNumericFacet{{2000, 2}, {2000, 3}, {2001, 1}} {
		if err := p.retainNumeric(value.Value, value.Count); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.finish(false); !errors.Is(err, ErrInput) {
		t.Fatal("raw full window became complete", err)
	}
	if _, err := p.handoff(f, 0, 1); !errors.Is(err, ErrInput) {
		t.Fatal("partial counts published", err)
	}
	if err := p.finish(true); err != nil {
		t.Fatal(err)
	}
	if d := g.Try(rbCacheWriter(*now, governor.ScanMinimum)); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.Conflict {
		t.Fatal("ready cache released parked client's protection", d)
	}
	page, err := p.handoff(f, 0, 1)
	if err != nil || len(page.facets) != 1 || page.facets[0] != (rbNumericFacet{2000, 5}) || page.total != 6 || page.exhausted || page.next != 1 {
		t.Fatal(page, err)
	}
	page.facets[0].Count = 99
	copy, err := p.handoff(f, 0, 1)
	if err != nil || copy.facets[0].Count != 5 {
		t.Fatal("output aliased retained result", copy, err)
	}
	last, err := p.handoff(f, 1, 1)
	if err != nil || !last.exhausted || last.facets[0].Value != 2001 {
		t.Fatal(last, err)
	}
	d := g.Try(rbCacheWriter(*now, governor.ScanMinimum))
	if d.Grant == nil {
		t.Fatal("publication stayed blocked after handoff", d.Refusal)
	}
	if err := a.Retired(d.Grant); err != nil {
		t.Fatal(err)
	}
	if a.Usage()[governor.Foreground][governor.MemoryBytes] != 8192 {
		t.Fatal("optional cache bytes prematurely refunded")
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
		t.Fatal("evicted cache retained physical bytes")
	}
}
func TestRBPreparedCacheInvalidationAndExpiry(t *testing.T) {
	for _, mode := range []string{"fence", "foreground", "expiry", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c, g, a, now := rbCacheFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p, err := c.begin(ctx, rbPreparedKey{owner: 1, query: [32]byte{1}}, rbPreparedOrder, now.Add(time.Minute), 8192, nil)
			if err != nil {
				t.Fatal(err)
			}
			f := browseFence{libraryID: 7, dataRevision: 1}
			if err := p.capture(f); err != nil {
				t.Fatal(err)
			}
			if err := p.retainIDs([]int64{1, 2, 3}); err != nil {
				t.Fatal(err)
			}
			if err := p.finish(true); err != nil {
				t.Fatal(err)
			}
			want := ErrStale
			switch mode {
			case "fence":
				f.dataRevision++
			case "foreground":
				d := g.Try(rbCacheWriter(*now, governor.Foreground))
				if d.Grant == nil {
					t.Fatal(d.Refusal)
				}
				if err := a.Retired(d.Grant); err != nil {
					t.Fatal(err)
				}
				want = governor.ErrPreparationStale
			case "expiry":
				*now = now.Add(12 * time.Second)
				want = governor.ErrPreparationExpired
			case "cancel":
				cancel()
				want = context.Canceled
			}
			page, err := p.handoff(f, 0, 40)
			if !errors.Is(err, want) || page.ids != nil || page.facets != nil || page.total != 0 {
				t.Fatal("stale prepared output escaped", page, err, want)
			}
			if !p.closed || a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
				t.Fatal("invalidated values retained")
			}
		})
	}
}
func TestRBPreparedCacheCapacityNeverTruncates(t *testing.T) {
	c, _, a, _ := rbCacheFixture(t)
	p := rbCacheBegin(t, c, rbPreparedOrder)
	if err := p.capture(browseFence{}); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, cap(p.ids)+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if err := p.retainIDs(ids); !errors.Is(err, errRBPreparationCapacity) {
		t.Fatal("oversize reduction truncated or grew", err)
	}
	if !p.closed || len(p.ids) != 0 || a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
		t.Fatal("failed reduction kept values/bytes")
	}
}

func TestRBPreparedCacheFenceBytesAndNoFootprintExpansion(t *testing.T) {
	c, _, a, now := rbCacheFixture(t)
	key := rbPreparedKey{owner: 1, query: [32]byte{2}}
	p, err := c.begin(t.Context(), key, rbPreparedOrder, now.Add(time.Minute), 16384, governor.PublicationFootprint{{Library: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.begin(t.Context(), key, rbPreparedOrder, now.Add(time.Hour), 16384, nil); !errors.Is(err, ErrInput) {
		t.Fatal("attach widened the held footprint", err)
	}
	f := browseFence{libraryID: 7, library: "movies", root: strings.Repeat("r", 512), bridgeRoot: strings.Repeat("b", 512)}
	if err := p.capture(f); err != nil {
		t.Fatal(err)
	}
	if cap(p.ids) == 0 || cap(p.ids) >= int(p.lease.PayloadBytes()/8) {
		t.Fatal("actual fence bytes were not reserved before vector allocation")
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	key.query[0]++
	p, err = c.begin(t.Context(), key, rbPreparedOrder, now.Add(time.Minute), 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.capture(browseFence{root: strings.Repeat("x", 4096)}); !errors.Is(err, errRBPreparationCapacity) {
		t.Fatal("wide fence escaped the byte reservation", err)
	}
	if a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
		t.Fatal("rejected fence left a physical holding")
	}
}
func TestRBPreparedCacheCancelAfterHandoffAndExpiredAttach(t *testing.T) {
	c, _, a, now := rbCacheFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	key := rbPreparedKey{owner: 1, query: [32]byte{3}}
	p, err := c.begin(ctx, key, rbPreparedOrder, now.Add(time.Minute), 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := browseFence{}
	if err := p.capture(f); err != nil {
		t.Fatal(err)
	}
	if err := p.retainIDs([]int64{1}); err != nil {
		t.Fatal(err)
	}
	if err := p.finish(true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.handoff(f, 0, 40); err != nil {
		t.Fatal(err)
	}
	cancel()
	if page, err := p.handoff(f, 0, 40); !errors.Is(err, context.Canceled) || len(page.ids) != 0 {
		t.Fatal("cancelled optional cache reused", page, err)
	}
	if a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
		t.Fatal("cancelled cache not evicted")
	}
	key.query[0]++
	p, err = c.begin(t.Context(), key, rbPreparedOrder, now.Add(time.Minute), 8192, nil)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(12 * time.Second)
	if _, err := c.begin(t.Context(), key, rbPreparedOrder, now.Add(time.Hour), 8192, nil); !errors.Is(err, governor.ErrPreparationExpired) {
		t.Fatal("expired attach secretly renewed", err)
	}
	if !p.closed {
		t.Fatal("expired job retained values")
	}
}
