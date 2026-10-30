package remotemedia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ValidateSourceDAVConfig is the owner-save configuration path. Its strict
// origin approval is separate from generic descriptor/library approvals.
func ValidateSourceDAVConfig(ctx context.Context, c DAVConfig, resolver Resolver) (DAVConfig, error) {
	u, err := parseURL(c.Root)
	if err != nil || u.RawQuery != "" || strings.Contains(c.Username, ":") || strings.ContainsAny(c.Username+c.Password, "\r\n\x00") {
		return c, ErrPolicy
	}
	approval, err := (Policy{Resolver: resolver}).PrepareEndpointApproval(ctx, c.Root)
	if err != nil {
		return c, err
	}
	if u.Scheme != "https" {
		addresses, e := (Policy{Resolver: resolver}).resolve(ctx, u.Hostname())
		if e != nil {
			return c, e
		}
		for _, ip := range addresses {
			if !nativePrivate(ip) {
				return c, ErrPolicy
			}
		}
	}
	if u.Scheme != "https" && (!c.InsecureLocal || len(approval.Addresses) == 0) {
		return c, ErrPolicy
	}
	c.Root = strings.TrimSuffix(u.String(), "/") + "/"
	c.Approval = approval
	return c, nil
}

// NewSourceDAV rechecks THIS source's saved origin, never a native/library
// approval. It permits normal verified TLS; HTTP retains its existing explicit
// local-only configuration restriction.
func NewSourceDAV(c DAVConfig, resolver Resolver) (*DAV, error) {
	u, err := url.Parse(c.Root)
	if err != nil || u.RawQuery != "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && (!c.InsecureLocal || len(c.Approval.Addresses) == 0)) {
		return nil, ErrPolicy
	}
	policy := Policy{Resolver: resolver, SourceEndpoints: true, SourcePrivateOnly: u.Scheme != "https", Approvals: []Approval{c.Approval}}
	if _, _, err := savedEndpointAddresses(c.Root, c.Approval); err != nil {
		return nil, err
	}
	return &DAV{config: c, policy: policy}, nil
}

type SetupRangeSupport string

const (
	SetupRangeUnknown     SetupRangeSupport = "unknown"
	SetupRangeSupported   SetupRangeSupport = "conditional"
	SetupRangeUnsupported SetupRangeSupport = "unsupported"
)

// DiagnoseSetupRanges is only for Configure/Test after the temporary serve has
// physically stopped. It does not open a runtime path or certify byte identity.
// The caller retains the exclusive hold, saved limits and current owner/config
// fence. A full pacing window separates the stopped serve's requests from these
// at-most-three direct origin requests; no independent concurrent pacer exists.
func (d *DAV) DiagnoseSetupRanges(ctx context.Context, file DAVEntry, test *ExclusiveTest, rps int, bps int64, check func(context.Context) error) (SetupRangeSupport, error) {
	return d.diagnoseSetupRanges(ctx, file, test, rps, bps, check, nil)
}

func (d *DAV) diagnoseSetupRanges(ctx context.Context, file DAVEntry, test *ExclusiveTest, rps int, bps int64, check func(context.Context) error, headers http.Header) (SetupRangeSupport, error) {
	if d == nil || !d.policy.SourceEndpoints || test == nil || test.Operations() == nil || check == nil || file.Directory || file.Deleted || file.Size < 1 || file.Relative == "" || rps < 1 || rps > 50 || bps < 0 || bps > 1_000_000_000 {
		return SetupRangeUnknown, ErrPolicy
	}
	ctx, release, err := test.Operations().enter(ctx)
	if err != nil {
		return SetupRangeUnknown, err
	}
	defer release()
	test.diagnosticMu.Lock()
	defer test.diagnosticMu.Unlock()
	if err = check(ctx); err != nil {
		return SetupRangeUnknown, err
	}
	locator, err := d.locator(file.Relative)
	if err != nil {
		return SetupRangeUnknown, err
	}
	if test.diagnosticNextRequest.IsZero() {
		test.diagnosticNextRequest = time.Now().Add(time.Second)
	}
	seen := map[int64]bool{}
	for _, start := range []int64{0, file.Size / 2, max(0, file.Size-16)} {
		if seen[start] {
			continue
		}
		seen[start] = true
		if test.diagnosticRequests >= 3 {
			return SetupRangeUnknown, nil
		}
		length := min(int64(16), file.Size-start)
		if err = waitDiagnostic(ctx, test.diagnosticNextRequest); err != nil {
			return SetupRangeUnknown, err
		}
		if err = check(ctx); err != nil {
			return SetupRangeUnknown, err
		}
		policy := d.policy
		policy.ManualRedirects = true
		client, err := NewAuthenticated(ctx, locator, policy, d.config.Username, d.config.Password)
		if err != nil {
			return SetupRangeUnknown, err
		}
		requestHeaders := headers.Clone()
		if requestHeaders == nil {
			requestHeaders = make(http.Header)
		}
		requestHeaders.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+length-1))
		test.diagnosticRequests++
		response, err := client.request(ctx, http.MethodGet, nil, requestHeaders, headers != nil)
		if err != nil {
			client.Close()
			return SetupRangeUnknown, err
		}
		support, err := readSetupRange(ctx, response, file.Size, start, length, bps, &test.diagnosticNextBytes)
		response.Body.Close()
		client.Close()
		if err != nil {
			return SetupRangeUnknown, err
		}
		if err = check(ctx); err != nil {
			return SetupRangeUnknown, err
		}
		test.diagnosticNextRequest = time.Now().Add(time.Second / time.Duration(rps))
		if support != SetupRangeSupported {
			return support, nil
		}
	}
	return SetupRangeSupported, nil
}

func waitDiagnostic(ctx context.Context, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := time.Until(at)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readSetupRange(ctx context.Context, response *http.Response, total, start, length, bps int64, nextBytes *time.Time) (SetupRangeSupport, error) {
	if err := ctx.Err(); err != nil {
		return SetupRangeUnknown, err
	}
	if response.StatusCode == http.StatusOK {
		return SetupRangeUnsupported, nil
	}
	if response.StatusCode != http.StatusPartialContent {
		return SetupRangeUnknown, serveResponseError(response)
	}
	expected := fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, total)
	if response.Header.Get("Content-Range") != expected || response.Header.Get("Content-Encoding") != "" || response.ContentLength >= 0 && response.ContentLength != length {
		return SetupRangeUnsupported, nil
	}
	// One bounded lookahead detects a falsely oversized/chunked body. The GET
	// itself requests at most sixteen bytes; an ignored 200 is never consumed.
	wanted := length + 1
	if bps > 0 {
		at := maxTime(time.Now(), *nextBytes)
		delay := time.Duration((wanted*int64(time.Second) + bps - 1) / bps)
		*nextBytes = at.Add(delay)
		if err := waitDiagnostic(ctx, *nextBytes); err != nil {
			return SetupRangeUnknown, err
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, wanted))
	defer clear(body)
	if ctx.Err() != nil {
		return SetupRangeUnknown, ctx.Err()
	}
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return SetupRangeUnknown, err
	}
	if err != nil || int64(len(body)) != length {
		return SetupRangeUnsupported, nil
	}
	return SetupRangeSupported, nil
}
func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
