// Package fetch sends the requests every online source of details makes: spaced out so the
// server never floods a provider, waiting when a provider asks for a pause, never following
// a redirect, and never reading more than a real answer could be.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// UserAgent names the server to providers. Some, like MusicBrainz, refuse a request
// without one that says what is asking and where to read about it.
const UserAgent = "Portico/1.0 ( https://github.com/PorticoMediaServer/portico )"

const maxAnswer = 4 << 20 // no answer from a provider's API is larger; more is not read

// SlowDown is a provider's request to wait before asking again. Until it passes, every
// request to that provider fails with it at once, without being sent.
type SlowDown struct {
	Provider string
	Until    time.Time
}

func (e *SlowDown) Error() string {
	return fmt.Sprintf("%s asked for a pause until %s", e.Provider, e.Until.Format(time.TimeOnly))
}

// Status is an answer other than success, not found or a pause. Body is the start of what
// the provider said, for the log.
type Status struct {
	Provider string
	Code     int
	Body     string
}

func (e *Status) Error() string { return fmt.Sprintf("%s answered %d", e.Provider, e.Code) }

// Client sends one provider's requests. Make one per provider and share it, so every caller
// of that provider waits its turn.
type Client struct {
	name string
	// every is the least time between the starts of two requests.
	every time.Duration
	// maxWait is the longest pause a provider can ask for that a request waits out and then
	// tries once more; a longer one fails the request with *SlowDown.
	maxWait time.Duration
	// Busy503 says this provider answers 503 when asked too often (MusicBrainz does), so a
	// 503 is a pause even without a Retry-After.
	Busy503 bool
	// MovedIsAnswer says this provider answers an id that was merged into another with a 301
	// that carries the other's whole answer (MusicBrainz does), so that body is the answer.
	// The redirect itself is still never followed.
	MovedIsAnswer bool
	// Bucket names the share of the provider's allowance a request is counted against, for a
	// provider that counts kinds of request apart (MusicBrainz counts searches, which every
	// client of the service shares, apart from lookups). Nil counts every request together.
	Bucket func(*http.Request) string
	http   *http.Client

	mu      sync.Mutex
	next    time.Time // the earliest the next request may start, by spacing
	paused  time.Time // the end of the last pause the provider asked for
	budgets map[string]*budget
}

// budget is what a provider's X-RateLimit headers last said about one bucket: how many
// requests are left of how many, until when.
type budget struct {
	limit, left int
	reset       time.Time
	next        time.Time // the earliest the next request of this bucket may start
}

// New makes a client that starts at most one request every `every` and waits out a pause of
// up to maxWait.
func New(name string, every, maxWait time.Duration) *Client {
	return &Client{name: name, every: every, maxWait: maxWait, budgets: map[string]*budget{}, http: &http.Client{
		Timeout: 20 * time.Second,
		// Every address is built by the provider's own code; an answer that points elsewhere
		// is not followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Do sends a request built by the provider's code and returns the body of a 200 answer. It
// fails with metadata.ErrNotFound for a 404, *SlowDown for a pause it cannot wait out, and
// *Status for any other answer. A request with a body must be built with a body that can be
// read again (http.NewRequest does that for bytes and strings), since a pause sends it twice.
func (c *Client) Do(req *http.Request) ([]byte, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	bucket := ""
	if c.Bucket != nil {
		bucket = c.Bucket(req)
	}
	for try := 0; ; try++ {
		if err := c.wait(req.Context(), bucket); err != nil {
			return nil, err
		}
		send := req
		if try > 0 {
			send = req.Clone(req.Context())
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				send.Body = body
			}
		}
		res, err := c.http.Do(send)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		c.count(bucket, res.Header)
		raw, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer+1))
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		switch {
		case res.StatusCode == http.StatusOK:
			if len(raw) > maxAnswer {
				return nil, fmt.Errorf("%s sent an answer larger than any real one", c.name)
			}
			return raw, nil
		case res.StatusCode == http.StatusMovedPermanently && c.MovedIsAnswer && len(raw) > 0:
			if len(raw) > maxAnswer {
				return nil, fmt.Errorf("%s sent an answer larger than any real one", c.name)
			}
			return raw, nil
		case res.StatusCode == http.StatusNotFound:
			return nil, metadata.ErrNotFound
		case res.StatusCode == http.StatusTooManyRequests ||
			res.StatusCode == http.StatusServiceUnavailable && (c.Busy503 || res.Header.Get("Retry-After") != ""):
			pause := c.pause(res.Header.Get("Retry-After"))
			if try > 0 || pause > c.maxWait {
				return nil, &SlowDown{Provider: c.name, Until: time.Now().Add(pause)}
			}
		default:
			return nil, &Status{Provider: c.name, Code: res.StatusCode, Body: string(raw[:min(len(raw), 300)])}
		}
	}
}

// wait holds a request until its turn. A pause longer than the client waits out fails it at
// once, and so does an allowance that is used up until later than that.
func (c *Client) wait(ctx context.Context, bucket string) error {
	c.mu.Lock()
	now := time.Now()
	hold := c.paused
	if t := c.allowance(bucket, now); t.After(hold) {
		hold = t
	}
	if hold.Sub(now) > c.maxWait {
		c.mu.Unlock()
		return &SlowDown{Provider: c.name, Until: hold}
	}
	start := now
	for _, t := range []time.Time{c.next, hold} {
		if t.After(start) {
			start = t
		}
	}
	c.next = start.Add(c.every)
	if b := c.budgets[bucket]; b != nil && b.left > 0 {
		b.left-- // counted now, so requests sent together do not all see the same allowance
	}
	c.mu.Unlock()
	if start.After(now) {
		t := time.NewTimer(start.Sub(now))
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// allowance is the earliest a request of a bucket may start by what the provider last said
// is left of its allowance. A used-up allowance waits for its reset. Under a tenth left, the
// rest is spread to the reset, using at most half of it, so the server slows down before it
// is refused and leaves some for the provider's other clients. Called with mu held.
func (c *Client) allowance(bucket string, now time.Time) time.Time {
	b := c.budgets[bucket]
	if b == nil || !b.reset.After(now) {
		return time.Time{}
	}
	switch {
	case b.left <= 0:
		return b.reset
	case b.left*10 < b.limit:
		gap := b.reset.Sub(now) / time.Duration(max(b.left/2, 1))
		start := b.next
		if start.Before(now) {
			start = now
		}
		b.next = start.Add(gap)
		return start
	}
	return time.Time{}
}

// count keeps what a provider's X-RateLimit headers say about a bucket. The reset is Unix
// seconds, or seconds from now for a small number.
func (c *Client) count(bucket string, h http.Header) {
	limit, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	left, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, err3 := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || limit <= 0 {
		return
	}
	at := time.Unix(reset, 0)
	if reset < 1e9 {
		at = time.Now().Add(time.Duration(reset) * time.Second)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.budgets[bucket]
	if b == nil {
		b = &budget{}
		c.budgets[bucket] = b
	}
	b.limit, b.left, b.reset = limit, left, at
}

// pause records how long the provider asked everyone to wait: Retry-After in seconds or as a
// date, 10 seconds when it did not say, and never more than an hour.
func (c *Client) pause(retryAfter string) time.Duration {
	pause := 10 * time.Second
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		pause = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(retryAfter); err == nil {
		pause = time.Until(at)
	}
	pause = min(max(pause, 0), time.Hour)
	c.mu.Lock()
	if until := time.Now().Add(pause); until.After(c.paused) {
		c.paused = until
	}
	c.mu.Unlock()
	return pause
}
