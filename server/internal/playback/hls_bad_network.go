package playback

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"
)

// Playback on a bad network is not playback that fails. It is playback that
// keeps asking: a phone on a train reconnects every few seconds, a television
// on congested wifi abandons a segment request and immediately makes another,
// a person scrubbing over a satellite link sends twenty seeks before the first
// one has been answered. Every one of those is a healthy client behaving
// correctly for its circumstances, and every one of them used to cost this
// server the same as a real new viewer.
//
// Three things here, each bounding one of those:
//
//   - A conversion whose viewer has gone silent is reclaimed, without ending
//     their session. The segments stay on disk and the producer restarts the
//     moment they come back.
//   - Many requests waiting for the same segment share one wait, instead of each
//     polling the database ten times a second.
//   - A session may restart its producer often, but not without limit, so a
//     scrub storm cannot become a storm of ffmpeg restarts.

const (
	// hlsAbandonedAfter is how long a conversion runs without anybody fetching a
	// segment from it before it is reclaimed. It is deliberately longer than any
	// pause a network causes and far shorter than the session lifetime: a viewer
	// who walks away mid-film, or whose phone loses signal in a tunnel, stops
	// costing the server a converter within two minutes and loses nothing.
	hlsAbandonedAfter = 2 * time.Minute
	// hlsRestartWindow and hlsRestartBudget bound producer restarts per session.
	// A person scrubbing makes a few seeks a second at most and never reaches
	// this; a client in a reconnect loop, or one whose seek requests are being
	// retried by a proxy, reaches it immediately.
	hlsRestartWindow = 10 * time.Second
	hlsRestartBudget = 12
)

// hlsAbandonedAfterValue is the live threshold; the constant above is its
// default and its documentation.
var hlsAbandonedAfterValue = hlsAbandonedAfter

// hlsAbandonedAfterForTest moves the reclaim threshold a test needs to cross.
func hlsAbandonedAfterForTest(v time.Duration) time.Duration {
	previous := hlsAbandonedAfterValue
	hlsAbandonedAfterValue = v
	return previous
}

// reclaimAbandoned cancels producers whose viewer has stopped fetching. The
// session is left alone: it is still valid, still authorised, and still
// resumable — this reclaims the processor, not the person's place in the film.
func (h *HLS) reclaimAbandoned() {
	now := time.Now()
	var abandoned []string
	h.mu.Lock()
	for id := range h.active {
		// Only grid conversions. A planned audio session produces everything in
		// one run with no segment requests in between, so silence there means
		// working, not gone.
		if h.windows[id] == nil {
			continue
		}
		usage := h.usage[producerSession(id)]
		if usage == nil || usage.served.IsZero() {
			continue
		}
		if now.Sub(usage.served) >= hlsAbandonedAfterValue {
			abandoned = append(abandoned, producerSession(id))
		}
	}
	h.mu.Unlock()
	for _, id := range abandoned {
		h.abandoned.Add(1)
		h.cancelSession(id)
	}
}

// restartAllowed reports whether this session may restart its producer now.
//
// The budget is per session and refills over a window, so the common case —
// somebody dragging the scrubber — is never touched, while a client that asks
// for a restart hundreds of times a second is answered with the converter it
// already has. The caller treats a refusal as "no restart happened", which is
// the same outcome its poll already handles: the segment is not there yet, and
// the retryable answer says so.
func (h *HLS) restartAllowedLocked(id string) bool {
	now := time.Now()
	if h.restarts == nil {
		h.restarts = map[string]*restartBudget{}
	}
	budget := h.restarts[id]
	if budget == nil {
		budget = &restartBudget{window: now}
		h.restarts[id] = budget
	}
	if now.Sub(budget.window) >= hlsRestartWindow {
		budget.window, budget.count = now, 0
	}
	if budget.count >= hlsRestartBudget {
		return false
	}
	budget.count++
	return true
}

// restartBudget is one session's producer-restart allowance.
type restartBudget struct {
	window time.Time
	count  int
}

// segmentWait is one shared wait for one segment of one session.
type segmentWait struct {
	done chan struct{}
	path string
	err  error
}

// awaitSegment waits for a segment that is being produced, sharing the wait
// between everybody asking for it.
//
// Without this, ten reconnecting clients asking for the same segment ran ten
// poll loops, each querying the session state ten times a second: a hundred
// queries a second for one segment, produced by clients doing nothing wrong.
// Now the first caller polls and the rest wait on its answer, each still bounded
// by its own deadline so a slow leader never holds a fast client past its own
// budget.
func (h *HLS) awaitSegment(ctx context.Context, id string, index int, poll func(context.Context) (string, error)) (string, error) {
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		key := id + ":" + strconv.Itoa(index)
		h.mu.Lock()
		if h.segmentWaits == nil {
			h.segmentWaits = map[string]*segmentWait{}
		}
		existing := h.segmentWaits[key]
		if existing != nil {
			h.mu.Unlock()
			select {
			case <-existing.done:
				// The leader's answer is a file path and a file on disk; both are
				// equally true for every follower.
				if (errors.Is(existing.err, context.Canceled) || errors.Is(existing.err, context.DeadlineExceeded)) && ctx.Err() == nil {
					continue
				}
				return existing.path, existing.err
			case <-ctx.Done():
				return "", ErrSegmentPreparing
			}
		}
		wait := &segmentWait{done: make(chan struct{})}
		h.segmentWaits[key] = wait
		h.mu.Unlock()
		wait.path, wait.err = poll(ctx)
		if ctx.Err() != nil {
			wait.err = ctx.Err()
		}
		h.mu.Lock()
		delete(h.segmentWaits, key)
		h.mu.Unlock()
		close(wait.done)
		return wait.path, wait.err
	}
}

// segmentReady is the file check a wait ends on, factored out so the leader and
// a late follower answer identically.
func segmentReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// waitsInFlight is for tests and diagnostics: how many distinct segments have a
// shared wait open right now.
func (h *HLS) waitsInFlight() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.segmentWaits)
}
