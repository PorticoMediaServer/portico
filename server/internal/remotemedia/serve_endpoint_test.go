package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type serveRoundTrip func(*http.Request) (*http.Response, error)

func (f serveRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServeEndpointRangeContract(t *testing.T) {
	for _, failure := range []string{"", "200", "redirect", "etag", "length", "short", "extra", "encoding", "extent", "changed"} {
		t.Run(failure, func(t *testing.T) {
			s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			calls := 0
			s.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "127.0.0.1:12345" || r.URL.Path != "/disc/large.iso" || r.Header.Get("Range") != "bytes=4294967300-4294967303" || r.Header.Get("If-Match") != `"old"` || r.Header.Get("Authorization") == "" || r.Header.Get("Accept-Encoding") != "identity" {
					t.Fatal("lost endpoint, credentials, range or condition")
				}
				resp := &http.Response{StatusCode: 206, Header: http.Header{"Etag": {`"old"`}, "Content-Range": {"bytes 4294967300-4294967303/5000000000"}}, ContentLength: 4, Body: io.NopCloser(strings.NewReader("old!"))}
				switch failure {
				case "200":
					resp.StatusCode = 200
				case "redirect":
					resp.StatusCode = 302
					resp.Header.Set("Location", "http://169.254.169.254/")
				case "etag":
					resp.Header.Set("ETag", `"new"`)
				case "length":
					resp.ContentLength = -1
				case "short":
					resp.Body = io.NopCloser(strings.NewReader("old"))
				case "extra":
					resp.Body = io.NopCloser(strings.NewReader("old!!"))
				case "encoding":
					resp.Header.Set("Content-Encoding", "gzip")
				case "extent":
					resp.Header.Set("Content-Range", "bytes 0-3/5000000000")
				case "changed":
					resp.StatusCode = 412
				}
				return resp, nil
			})
			got, err := s.ReadRange(context.Background(), "disc/large.iso", `"old"`, 5000000000, 4294967300, 4, func(context.Context) error { return nil })
			if failure == "" && (err != nil || string(got) != "old!") {
				t.Fatalf("range: %q %v", got, err)
			}
			if failure != "" && (err == nil || len(got) != 0) {
				t.Fatalf("accepted %s: %q %v", failure, got, err)
			}
			if failure == "changed" && !errors.Is(err, ErrObservationChanged) {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("reconnected %d times", calls)
			}
		})
	}
}

func TestServeEndpointRejectsMembersBeforeDispatch(t *testing.T) {
	s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.client.Transport = serveRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid member dispatched")
		return nil, ErrPolicy
	})
	for _, member := range []string{"", "/absolute", "../escape", "disc/../escape", "https://host/file", "disc/%2e%2e/file", "disc\\file"} {
		if _, err := s.ReadRange(context.Background(), member, `"old"`, 10, 0, 1, func(context.Context) error { return nil }); err == nil {
			t.Fatal(member)
		}
	}
	if _, err := s.ReadRange(context.Background(), "file", `"old"`, 1<<40, 1<<39, 1<<20+1, func(context.Context) error { return nil }); err == nil {
		t.Fatal("unbounded read")
	}
}

type servedChangingBody struct {
	io.Reader
	closed *bool
}

func (b servedChangingBody) Close() error { *b.closed = true; return nil }

func TestServeRangeDiscardsChangeAfterBodyCloseWithCapacityReleased(t *testing.T) {
	endpoint, err := NewServeEndpoint(12345, "fixture", "private", NewOperationBudget(context.Background(), 1))
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	closed, postChecked := false, false
	endpoint.client.Transport = serveRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 206, Header: http.Header{"Etag": {`"old"`}, "Content-Range": {"bytes 0-3/16"}}, ContentLength: 4, Body: servedChangingBody{strings.NewReader("old!"), &closed}}, nil
	})
	data, err := endpoint.ReadRange(context.Background(), "file", `"old"`, 16, 0, 4, func(context.Context) error {
		if closed {
			postChecked = true
			if counts := endpoint.budget.Counts(); counts.Active != 0 {
				t.Fatal("post-read observer retained the completed body slot", counts)
			}
			return ErrObservationChanged
		}
		return nil
	})
	if !closed || !postChecked || !errors.Is(err, ErrObservationChanged) || len(data) != 0 {
		t.Fatal("closed-body change exposed bytes", data, err, closed, postChecked)
	}
}

func TestResponseFailureKeepsOnlySuppliedDeadline(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for _, raw := range []string{"50400", now.Add(14 * time.Hour).Format(http.TimeFormat)} {
		resp := &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {raw}}}
		var failure *ResponseFailure
		if !errors.As(responseFailure(resp, now), &failure) || failure.Kind != "throttled" || !failure.RetryAt.Equal(now.Add(14*time.Hour)) {
			t.Fatal(failure)
		}
	}
	for _, raw := range []string{"", "tomorrow", "-1", "9999999999999999999999999999"} {
		if !retryAt(raw, now).IsZero() {
			t.Fatalf("invented deadline for %q", raw)
		}
	}
	var quota *ResponseFailure
	if !errors.As(responseFailure(&http.Response{StatusCode: 507, Header: http.Header{}}, now), &quota) || quota.Kind != "quota" || !quota.RetryAt.IsZero() {
		t.Fatal(quota)
	}
}

func TestAuthenticatedRequestDoesNotHideRetryOrWait(t *testing.T) {
	calls := 0
	c := &Client{url: "http://127.0.0.1:12345/file", client: &http.Client{Transport: serveRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"50400"}}, Body: http.NoBody}, nil
	})}}
	resp, err := c.Request(context.Background(), "PROPFIND", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls != 1 || resp.Header.Get("Retry-After") != "50400" {
		t.Fatalf("calls=%d headers=%v", calls, resp.Header)
	}
}

func TestServeDialChecksExactOriginAndReconnectBeforeNetwork(t *testing.T) {
	s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	transport := s.client.Transport.(*http.Transport)
	calls := 0
	ctx := context.WithValue(context.Background(), serveReconnectKey{}, func() error { calls++; return ErrObservationChanged })
	for _, target := range []string{"127.0.0.1:12346", "[::1]:12345", "169.254.169.254:12345", "metadata.google.internal:12345"} {
		if conn, err := transport.DialContext(ctx, "tcp", target); conn != nil || !errors.Is(err, ErrPolicy) {
			t.Fatal(target, err)
		}
	}
	if calls != 0 {
		t.Fatal("unapproved target reached source callback")
	}
	if conn, err := transport.DialContext(ctx, "tcp", "127.0.0.1:12345"); conn != nil || !errors.Is(err, ErrObservationChanged) || calls != 1 {
		t.Fatal("reconnect bypassed observation", err, calls)
	}
}
