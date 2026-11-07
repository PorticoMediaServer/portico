package governor

import (
	"context"
	"time"
)

// One same-ledger metadata barrier, not a grant or native authority. All old
// physical holdings retain their grants. New readers/W refuse while it drains;
// no cancel, expiry or snapshot callback return is treated as physical release.
func (c *Checkpointer) beginRecovery(parent context.Context, deadline time.Time) (func(), error) {
	g := c.authority.ledger
	g.mu.Lock()
	if g.resetOwner != nil {
		g.mu.Unlock()
		return nil, ErrBusy
	}
	g.resetOwner = c
	g.mu.Unlock()
	release := func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.resetOwner == c {
			g.resetOwner = nil
		}
	}
	handed := false
	defer func() {
		if !handed {
			release()
		}
	}()
	until := minTime(deadline, g.config.Now().Add(1250*time.Millisecond))
	ctx, cancel := c.deadlineContext(parent, until)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			handed = true
			return release, err
		}
		g.mu.Lock()
		held := false
		for _, h := range g.live {
			if h.keeper == c.keeper && h.keeperRole == keeperLifetime {
				continue
			}
			if h.snapshot != nil && h.snapshot.nativeSafe.Load() {
				continue
			}
			if h.request.Writer || h.checkpoint || h.request.Need[Reader] != 0 {
				held = true
				break
			}
		}
		if !held {
			g.mu.Unlock()
			handed = true
			return release, nil
		}
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			handed = true
			return release, ctx.Err()
		case <-changed:
		}
	}
}
