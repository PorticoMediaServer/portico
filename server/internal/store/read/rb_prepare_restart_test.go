package read

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func rbRestartBegin(t *testing.T, c *rbPreparedCache, ctx context.Context) *rbPreparedEntry {
	t.Helper()
	p, err := c.begin(ctx, rbPreparedKey{owner: 1, query: [32]byte{1}}, rbPreparedOrder, c.now().Add(time.Minute), 16384, governor.PublicationFootprint{{Library: 7}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func rbRestartFence(revision int64) browseFence {
	return browseFence{
		database: "db", token: "token", account: "account", profile: "profile",
		membership: "membership", lifetime: "lifetime", library: "movies",
		role: "member", memberLibraries: "7", profileLibraries: "7",
		expiresAt: "2030-01-01T00:00:00Z", horizon: "2030-01-01T00:00:00Z",
		profileLabels: "default", root: "root", bridgeRoot: "bridge",
		libraryID: 7, kind: 1, dataRevision: revision,
		policy: 1, policySubject: 1, policyWitness: "witness",
	}
}

// The F-04 parked-ready adversary: a complete cache waits for a poll while an
// import advances content. The wrapper restarts from the beginning of the same
// admitted job, so the parked result never escapes and physical bytes survive.
func TestRBPrepareRestartParkedReadyVsImport(t *testing.T) {
	c, g, a, now := rbCacheFixture(t)
	p := rbRestartBegin(t, c, t.Context())
	pinned := rbRestartFence(1)
	original := p.lease.Deadline()
	bytes := a.Usage()[governor.Foreground][governor.MemoryBytes]
	calls := 0
	out, err := rbPrepareRestart(p, pinned, func(attempt int) (browseFence, error) {
		calls++
		fresh := rbRestartFence(int64(attempt + 1))
		if err := p.capture(fresh); err != nil {
			return fresh, err
		}
		if attempt == 0 {
			if err := p.retainIDs([]int64{1, 2}); err != nil {
				return fresh, err
			}
			if err := p.finish(true); err != nil {
				return fresh, err
			}
			// The parked ready result observed an import after finishing.
			return rbRestartFence(2), errRBPrepareContent
		}
		if err := p.retainIDs([]int64{3, 4}); err != nil {
			return fresh, err
		}
		return fresh, p.finish(true)
	})
	if err != nil || out.Attempts != 2 || out.Restarts != 1 || calls != 2 {
		t.Fatal(out, err, calls)
	}
	if !p.lease.Deadline().Equal(original) || a.Usage()[governor.Foreground][governor.MemoryBytes] != bytes {
		t.Fatal("restart renewed the deadline or re-admitted physical bytes")
	}
	if !p.complete || !p.captured || p.fence != rbRestartFence(2) {
		t.Fatal("restarted attempt did not replace the invalidated values")
	}
	if d := g.Try(rbCacheWriter(*now, governor.ScanMinimum)); d.Grant != nil {
		t.Fatal("ready restart released the parked client's protection", d.Refusal)
	}
	page, err := p.handoff(rbRestartFence(2), 0, 40)
	if err != nil || len(page.ids) != 2 || page.ids[0] != 3 || page.ids[1] != 4 {
		t.Fatal("stale parked values published", page, err)
	}
	d := g.Try(rbCacheWriter(*now, governor.ScanMinimum))
	if d.Grant == nil {
		t.Fatal("publication did not resume after first handoff", d.Refusal)
	}
	if err := a.Retired(d.Grant); err != nil {
		t.Fatal(err)
	}
	if _, err := p.handoff(rbRestartFence(1), 0, 40); !errors.Is(err, ErrStale) {
		t.Fatal("superseded fence still published", err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
		t.Fatal("restart retained physical bytes after eviction")
	}
}

func TestRBPrepareRestartSecurityChangeDenied(t *testing.T) {
	t.Run("foreground-edit", func(t *testing.T) {
		c, g, a, now := rbCacheFixture(t)
		p := rbRestartBegin(t, c, t.Context())
		d := g.Try(rbCacheWriter(*now, governor.Foreground))
		if d.Grant == nil {
			t.Fatal(d.Refusal)
		}
		if err := a.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		calls := 0
		out, err := rbPrepareRestart(p, rbRestartFence(1), func(int) (browseFence, error) {
			calls++
			return rbRestartFence(2), errRBPrepareContent
		})
		if !errors.Is(err, governor.ErrPreparationStale) || calls != 0 || out.Attempts != 0 {
			t.Fatal("foreground edit was restarted rather than denied", out, err, calls)
		}
		if !p.closed || a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied preparation retained values/bytes")
		}
	})
	t.Run("pinned-context", func(t *testing.T) {
		c, _, a, _ := rbCacheFixture(t)
		p := rbRestartBegin(t, c, t.Context())
		calls := 0
		// Only content may move between attempts. A viewer/policy change is a
		// denial even when the producer labels the attempt as content-invalid.
		moved := rbRestartFence(1)
		moved.profile = "other-profile"
		out, err := rbPrepareRestart(p, rbRestartFence(1), func(int) (browseFence, error) {
			calls++
			return moved, errRBPrepareContent
		})
		if !errors.Is(err, ErrDenied) || calls != 1 || out.Attempts != 1 || out.Restarts != 0 {
			t.Fatal("security change restarted", out, err, calls)
		}
		if !p.closed || a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied context retained values/bytes")
		}
	})
	t.Run("delivered", func(t *testing.T) {
		c, _, _, _ := rbCacheFixture(t)
		p := rbRestartBegin(t, c, t.Context())
		if err := p.capture(rbRestartFence(1)); err != nil {
			t.Fatal(err)
		}
		if err := p.retainIDs([]int64{1}); err != nil {
			t.Fatal(err)
		}
		if err := p.finish(true); err != nil {
			t.Fatal(err)
		}
		if _, err := p.handoff(rbRestartFence(1), 0, 40); err != nil {
			t.Fatal(err)
		}
		calls := 0
		out, err := rbPrepareRestart(p, rbRestartFence(1), func(int) (browseFence, error) {
			calls++
			return rbRestartFence(2), errRBPrepareContent
		})
		if !errors.Is(err, ErrStale) || calls != 1 || out.Attempts != 1 {
			t.Fatal("delivered values restarted", out, err, calls)
		}
	})
}

func TestRBPrepareRestartDeadlineAndCancellation(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		c, _, a, now := rbCacheFixture(t)
		p := rbRestartBegin(t, c, t.Context())
		calls := 0
		out, err := rbPrepareRestart(p, rbRestartFence(1), func(attempt int) (browseFence, error) {
			calls++
			fresh := rbRestartFence(1)
			if err := p.capture(fresh); err != nil {
				return fresh, err
			}
			if err := p.retainIDs([]int64{1}); err != nil {
				return fresh, err
			}
			// The first attempt finished exactly as the original deadline expired.
			*now = p.lease.Deadline()
			return rbRestartFence(2), errRBPrepareContent
		})
		if !errors.Is(err, governor.ErrPreparationExpired) || calls != 1 || out.Attempts != 1 || out.Restarts != 0 {
			t.Fatal("restart outlived the original deadline", out, err, calls)
		}
		if !p.closed || a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("expired restart retained values/bytes")
		}
	})
	t.Run("cancelled-transaction", func(t *testing.T) {
		c, _, a, now := rbCacheFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		p, err := c.begin(ctx, rbPreparedKey{owner: 1, query: [32]byte{9}}, rbPreparedOrder, now.Add(time.Minute), 16384, nil)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		out, err := rbPrepareRestart(p, rbRestartFence(1), func(int) (browseFence, error) {
			calls++
			cancel() // earlier transaction cancellation, not a job deadline
			return rbRestartFence(2), errRBPrepareContent
		})
		if !errors.Is(err, context.Canceled) || calls != 1 || out.Attempts != 1 {
			t.Fatal("cancelled transaction restarted", out, err, calls)
		}
		if !p.closed || a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("cancelled restart retained values/bytes")
		}
	})
	t.Run("restart-limit", func(t *testing.T) {
		c, _, a, _ := rbCacheFixture(t)
		p := rbRestartBegin(t, c, t.Context())
		calls := 0
		out, err := rbPrepareRestart(p, rbRestartFence(1), func(attempt int) (browseFence, error) {
			calls++
			return rbRestartFence(int64(attempt + 2)), errRBPrepareContent
		})
		if !errors.Is(err, errRBPrepareContent) || calls != 3 || out.Attempts != 3 || out.Restarts != rbPrepareRestartLimit {
			t.Fatal("restart budget not bounded at two", out, err, calls)
		}
		if !p.closed || a.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("exhausted restart retained partial values/bytes")
		}
	})
}

// Cache-mutex contention is a wait inside the already admitted job, not a
// restart, refusal or second admission.
func TestRBPrepareRestartContention(t *testing.T) {
	c, _, _, _ := rbCacheFixture(t)
	p := rbRestartBegin(t, c, t.Context())
	c.mu.Lock()
	started := make(chan struct{})
	called := make(chan int, 1)
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := rbPrepareRestart(p, rbRestartFence(1), func(attempt int) (browseFence, error) {
			called <- attempt
			fresh := rbRestartFence(1)
			if err := p.capture(fresh); err != nil {
				return fresh, err
			}
			if err := p.retainIDs([]int64{7}); err != nil {
				return fresh, err
			}
			return fresh, p.finish(true)
		})
		done <- err
	}()
	<-started
	select {
	case attempt := <-called:
		c.mu.Unlock()
		t.Fatal("producer ran under the cache lock", attempt)
	default:
	}
	c.mu.Unlock()
	if attempt := <-called; attempt != 0 {
		t.Fatal("contention consumed a restart", attempt)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if page, err := p.handoff(rbRestartFence(1), 0, 1); err != nil || len(page.ids) != 1 || page.ids[0] != 7 {
		t.Fatal(page, err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
}
