// Package metadataprovider contains bounded provider transports. Catalog matching,
// durable jobs, provenance and retry scheduling remain server application concerns.
package metadataprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/progressio"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Error struct {
	Provider   string
	Status     int
	Code       string
	RetryAfter time.Duration
	location   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s metadata: %s (HTTP %d)", e.Provider, e.Code, e.Status)
}
func (e *Error) Retryable() bool {
	return e.Status == 429 || e.Status >= 500 || e.Code == "unavailable"
}

type transport struct {
	provider, base, userAgent string
	client                    *http.Client
	gate                      chan struct{}
	inflight                  chan struct{}
	next                      time.Time
	interval                  time.Duration
	mu                        sync.Mutex
}

func newTransport(provider, base, userAgent string) *transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.MaxConnsPerHost = 4
	t.MaxIdleConnsPerHost = 4
	t.ResponseHeaderTimeout = 10 * time.Second
	capacity := 4
	if provider == "musicbrainz" || provider == "anilist" || provider == "acoustid" {
		capacity = 1
	}
	out := &transport{inflight: make(chan struct{}, capacity), provider: provider, base: base, userAgent: userAgent, interval: time.Second, gate: make(chan struct{}, 1), client: &http.Client{Transport: progressio.Transport{Base: t, Idle: 30 * time.Second, Overall: 15 * time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	out.setSpeed("normal")
	return out
}
func (t *transport) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case t.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-t.gate }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		t.mu.Lock()
		delay := time.Until(t.next)
		if delay <= 0 {
			t.next = time.Now().Add(t.interval)
			t.mu.Unlock()
			return nil
		}
		t.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}
func (t *transport) deferRequests(ctx context.Context, delay time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if deadline := time.Now().Add(delay); deadline.After(t.next) {
		t.next = deadline
	}
}
func retryDelay(value string, now time.Time) time.Duration {
	var delay time.Duration
	if seconds, e := strconv.ParseInt(value, 10, 64); e == nil && seconds > 0 {
		if seconds > 86400 {
			seconds = 86400
		}
		delay = time.Duration(seconds) * time.Second
	} else if deadline, e := http.ParseTime(value); e == nil {
		delay = deadline.Sub(now)
	}
	if delay < time.Second {
		delay = time.Second
	}
	if delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	return delay
}

// raw performs a paced GET and returns the body. It exists so that every call
// to a provider, including the older catalogue paths that want the bytes rather
// than a decoded value, shares one pacing clock, one in-flight gate and one
// Retry-After deferral. base overrides the transport's origin when non-empty.
func (t *transport) raw(ctx context.Context, client *http.Client, base, route, token string, budget int64) ([]byte, error) {
	check, admissionErr := networkAdmission(ctx)
	if admissionErr != nil {
		return nil, admissionErr
	}
	if client == nil {
		client = t.client
	}
	if !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") {
		return nil, errors.New("invalid provider route")
	}
	if base == "" {
		base = t.base
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := t.begin(ctx); err != nil {
		return nil, err
	}
	defer t.release()
	req, err := http.NewRequestWithContext(ctx, "GET", base+route, nil)
	if err != nil {
		return nil, errors.New("invalid provider request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if err := check(); err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Provider: t.provider, Code: "unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		problem := &Error{Provider: t.provider, Status: resp.StatusCode, Code: "request_rejected"}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			problem.RetryAfter = retryDelay(resp.Header.Get("Retry-After"), time.Now())
			if resp.StatusCode >= 500 && resp.Header.Get("Retry-After") == "" {
				problem.RetryAfter = 5 * time.Second
			}
			t.deferRequests(ctx, problem.RetryAfter)
		}
		return nil, problem
	}
	raw, err := readBounded(ctx, resp, budget, t.provider)
	if err != nil {
		return nil, err
	}
	if err := check(); err != nil {
		return nil, err
	}
	return raw, nil
}

// readBounded reads a response body up to a budget, so a provider cannot make this process
// spend memory it did not agree to.
func readBounded(ctx context.Context, resp *http.Response, budget int64, provider string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, budget+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Provider: provider, Code: "unavailable"}
	}
	if int64(len(raw)) > budget {
		return nil, &Error{Provider: provider, Code: "response_too_large"}
	}
	return raw, nil
}

func (t *transport) request(ctx context.Context, method, route, token string, body any, out any) error {
	check, admissionErr := networkAdmission(ctx)
	if admissionErr != nil {
		return admissionErr
	}
	// Only provider adapters construct paths. Redirect destinations are never fetched here.
	if !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") {
		return errors.New("invalid provider route")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	if err = t.begin(ctx); err != nil {
		return err
	}
	defer t.release()
	req, err := http.NewRequestWithContext(ctx, method, t.base+route, bytes.NewReader(payload))
	if err != nil {
		return errors.New("invalid provider request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if err := check(); err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Provider: t.provider, Code: "unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		problem := &Error{Provider: t.provider, Status: resp.StatusCode, Code: "request_rejected", location: resp.Header.Get("Location")}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			problem.RetryAfter = retryDelay(resp.Header.Get("Retry-After"), time.Now())
			if resp.StatusCode >= 500 && resp.Header.Get("Retry-After") == "" {
				problem.RetryAfter = 5 * time.Second
			}
			t.deferRequests(ctx, problem.RetryAfter)
		}
		return problem
	}
	const budget = 4 << 20
	if resp.ContentLength > budget {
		return &Error{Provider: t.provider, Code: "response_too_large"}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, budget+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Provider: t.provider, Code: "unavailable"}
	}
	if len(raw) > budget {
		return &Error{Provider: t.provider, Code: "response_too_large"}
	}
	if err := check(); err != nil {
		return err
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return &Error{Provider: t.provider, Code: "invalid_response"}
	}
	return nil
}

// The in-flight gate spans response consumption; the pacing gate spans only
// reserving a start time. Both are shared by all routes of one provider.
func (t *transport) begin(ctx context.Context) error {
	select {
	case t.inflight <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := t.acquire(ctx); err != nil {
		t.release()
		return err
	}
	return nil
}
func (t *transport) release() { <-t.inflight }
func (t *transport) setSpeed(speed string) {
	interval := time.Second
	switch t.provider {
	case "tmdb":
		interval = 100 * time.Millisecond
		if speed == "gentle" {
			interval = 500 * time.Millisecond
		}
		if speed == "fast" {
			interval = 50 * time.Millisecond
		}
	case "tvdb":
		interval = 500 * time.Millisecond
		if speed == "gentle" {
			interval = time.Second
		}
		if speed == "fast" {
			interval = 200 * time.Millisecond
		}
	case "anilist":
		interval = 2100 * time.Millisecond
		if speed == "gentle" {
			interval = 4 * time.Second
		}
	}
	t.mu.Lock()
	t.interval = interval
	t.mu.Unlock()
}
