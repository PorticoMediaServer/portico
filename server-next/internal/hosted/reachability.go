package hosted

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// ReachabilityAddress is exactly one published address checked by Services' identity proof.
type ReachabilityAddress struct {
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Reachable bool   `json:"reachable"`
}

// ReachabilityResult is a completed outside check, not a router's opening acknowledgement.
type ReachabilityResult struct {
	CheckedAt time.Time             `json:"checkedAt"`
	Addresses []ReachabilityAddress `json:"addresses"`
}

// Reachable asks only for this server's registered addresses. There is no retry or timer.
// Six attempts per rolling hour, including failures, bound owner-triggered traffic; a
// Services Retry-After can require a longer wait. Only Services contacts the registered server; callers supply no target.
func (c *Client) Reachable(ctx context.Context, since time.Time) (ReachabilityResult, error) {
	if err := ctx.Err(); err != nil {
		return ReachabilityResult{}, err
	}
	now := c.o.Now()
	c.mu.Lock()
	live := c.reachCalls[:0]
	for _, at := range c.reachCalls {
		if at.Add(time.Hour).After(now) {
			live = append(live, at)
		}
	}
	c.reachCalls = live
	next := c.reachAfter
	if len(live) >= 6 {
		next = maxTime(next, live[0].Add(time.Hour))
	}
	if next.After(now) {
		c.mu.Unlock()
		return ReachabilityResult{}, &BusyError{Wait: next.Sub(now)}
	}
	c.reachCalls = append(c.reachCalls, now)
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second) // Services' seven-second proof and bounded queue wait.
	defer cancel()
	var out ReachabilityResult
	err := c.call(ctx, http.MethodPost, "/v1/servers/"+c.o.ServerID+"/reachability", since, "", nil, &out)
	var busy *BusyError
	if errors.As(err, &busy) {
		c.mu.Lock()
		c.reachAfter = maxTime(c.reachAfter, c.o.Now().Add(max(busy.Wait, time.Minute)))
		c.mu.Unlock()
	}
	if err != nil {
		return ReachabilityResult{}, err
	}
	if out.CheckedAt.IsZero() || out.Addresses == nil || len(out.Addresses) > 8 {
		return ReachabilityResult{}, ErrNotConnected
	}
	return out, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
