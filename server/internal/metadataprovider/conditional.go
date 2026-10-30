package metadataprovider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// A refresh of a document that has not changed is the cheapest request there is, if you ask
// for it: TMDB and TVDB both answer a detail route with an ETag, and both answer
// If-None-Match with 304 and no body. Nothing here asked, so every refresh of a library of
// two or three million items re-downloaded documents that were identical to the ones already
// stored, spent the provider's rate allowance on them, and re-parsed them.
//
// The validators are the provider's, opaque to us, stored beside the document and sent back
// unchanged. A provider that offers neither simply never produces a 304, and the caller pays
// what it paid before.

// Conditional carries a document's validators in both directions: what was stored from the
// last fetch on the way out, what the provider returned on the way back.
type Conditional struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
}

// Empty says there is nothing to ask conditionally with, so the request is an ordinary one.
func (c Conditional) Empty() bool { return c.ETag == "" && c.LastModified == "" }

// ErrNotModified is the provider saying the stored document is still current. It is not a
// failure: it is the answer, and it costs no body, no parsing and — with most providers — no
// rate allowance to speak of.
var ErrNotModified = errors.New("the provider's document has not changed")

// validatorLimit keeps a provider's header from becoming unbounded storage. Real ETags are
// tens of bytes; anything larger is not one, and is dropped rather than stored.
const validatorLimit = 256

func validator(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > validatorLimit || strings.ContainsAny(v, "\r\n\x00") {
		return ""
	}
	return v
}

// apply sets the conditional request headers a stored document allows.
func (c Conditional) apply(req *http.Request) {
	if tag := validator(c.ETag); tag != "" {
		req.Header.Set("If-None-Match", tag)
	}
	if since := validator(c.LastModified); since != "" {
		req.Header.Set("If-Modified-Since", since)
	}
}

// conditionalFrom reads the validators a response offers for next time. A 304 usually repeats
// the ETag and usually omits Last-Modified, so the previous values are kept where the response
// is silent: forgetting one would turn every second refresh back into a full download.
func conditionalFrom(resp *http.Response, previous Conditional) Conditional {
	out := Conditional{ETag: validator(resp.Header.Get("ETag")), LastModified: validator(resp.Header.Get("Last-Modified"))}
	if out.ETag == "" {
		out.ETag = validator(previous.ETag)
	}
	if out.LastModified == "" {
		out.LastModified = validator(previous.LastModified)
	}
	return out
}

// rawConditional is raw with validators. A 304 returns ErrNotModified and the validators to
// store; every other outcome behaves exactly as the unconditional call does.
func (t *transport) rawConditional(ctx context.Context, client *http.Client, base, route, token string, budget int64, in Conditional) ([]byte, Conditional, error) {
	check, admissionErr := networkAdmission(ctx)
	if admissionErr != nil {
		return nil, in, admissionErr
	}
	if client == nil {
		client = t.client
	}
	if !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") {
		return nil, in, errors.New("invalid provider route")
	}
	if base == "" {
		base = t.base
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := t.begin(ctx); err != nil {
		return nil, in, err
	}
	defer t.release()
	req, err := http.NewRequestWithContext(ctx, "GET", base+route, nil)
	if err != nil {
		return nil, in, errors.New("invalid provider request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	in.apply(req)
	if err := check(); err != nil {
		return nil, in, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, in, ctx.Err()
		}
		return nil, in, &Error{Provider: t.provider, Code: "unavailable"}
	}
	defer resp.Body.Close()
	if err := check(); err != nil {
		return nil, in, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return nil, conditionalFrom(resp, in), ErrNotModified
	}
	if resp.StatusCode != http.StatusOK {
		problem := &Error{Provider: t.provider, Status: resp.StatusCode, Code: "request_rejected"}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			problem.RetryAfter = retryDelay(resp.Header.Get("Retry-After"), time.Now())
			t.deferRequests(ctx, problem.RetryAfter)
		}
		return nil, in, problem
	}
	raw, err := readBounded(ctx, resp, budget, t.provider)
	if err != nil {
		return nil, in, err
	}
	if err := check(); err != nil {
		return nil, in, err
	}
	return raw, conditionalFrom(resp, Conditional{}), nil
}
