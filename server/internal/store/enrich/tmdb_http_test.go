package enrich

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// No server/listener, real credential or live provider is used in this file.
// The mocked RoundTripper explicitly simulates the actual request-write event.
type tmdbHTTPFixture func(*http.Request) (*http.Response, error)

func (f tmdbHTTPFixture) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type tmdbHTTPBody struct {
	io.Reader
	closed     atomic.Bool
	closeError error
	onClose    func()
}

func (b *tmdbHTTPBody) Close() error {
	b.closed.Store(true)
	if b.onClose != nil {
		b.onClose()
	}
	return b.closeError
}

type tmdbHTTPConn struct {
	net.Conn
	closed bool
}

func (c *tmdbHTTPConn) Close() error { c.closed = true; return nil }

func tmdbHTTPHarnessConfig() TMDBHTTPConfig {
	return TMDBHTTPConfig{
		BearerToken: "synthetic-noncredential", Authority: "credential-generation:1",
		MaxHeaderBytes: 4096, MaxMetadataBytes: 1024,
		CacheTTL: time.Hour, RequestTimeout: time.Second,
	}
}

func tmdbHTTPGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, err := z.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func tmdbHTTPWritten(r *http.Request) {
	trace := httptrace.ContextClientTrace(r.Context())
	trace.GotConn(httptrace.GotConnInfo{})
	trace.WroteRequest(httptrace.WroteRequestInfo{})
}

func TestTMDBHTTPDecodedObservationAndOriginalCacheMetadata(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip", "gzip-multimember"} {
		t.Run(encoding, func(t *testing.T) {
			wire := []byte(tmdbMovieFixture)
			header := http.Header{"Etag": {"etag:157"}, "Last-Modified": {"Thu, 05 Nov 2026 10:00:00 GMT"}}
			if encoding == "gzip" {
				wire = tmdbHTTPGzip(t, wire)
				header.Set("Content-Encoding", "gzip")
			}
			if encoding == "gzip-multimember" {
				wire = append(tmdbHTTPGzip(t, wire[:100]), tmdbHTTPGzip(t, wire[100:])...)
				header.Set("Content-Encoding", "gzip")
			}
			body := &tmdbHTTPBody{Reader: bytes.NewReader(wire)}
			var calls, clockCalls atomic.Int32
			tr, err := newTMDBHTTPTransport(tmdbMoviePolicy(t), admissionFunc(func(_ context.Context, hint int64) (func(), error) {
				if hint != 4194304+1024 {
					t.Error("metadata escaped existing admission", hint)
				}
				return func() {
					if !body.closed.Load() {
						t.Error("network custody retired before body close")
					}
				}, nil
			}), tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/movie/42" || r.URL.RawQuery != "append_to_response=credits&language=en-CA" || r.URL.User != nil || r.URL.Fragment != "" ||
					r.Header.Get("Authorization") != "Bearer synthetic-noncredential" || r.Header.Get("Accept-Encoding") != "gzip" || strings.Contains(r.URL.String(), "synthetic") {
					t.Error("credential/route/descriptor boundary changed")
				}
				tmdbHTTPWritten(r)
				return &http.Response{StatusCode: 200, Header: header, Body: body, ContentLength: 1}, nil
			}), func() time.Time { clockCalls.Add(1); return time.Date(2026, 11, 6, 10, 23, 19, 137, time.UTC) })
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			doc, err := tr.GetDocument(t.Context(), documentKey())
			wantMetadata := AcquisitionMetadata{"2026-11-06T10:23:19.000000137Z", "etag:157", "Thu, 05 Nov 2026 10:00:00 GMT", "https://api.themoviedb.org/3/movie/42"}
			if err != nil || string(doc.Body()) != tmdbMovieFixture || doc.Metadata() != wantMetadata {
				t.Fatal("decoded original observation", err, doc.Metadata())
			}
			observation := tmdbObservation()
			metadata := doc.Metadata()
			observation.ObservedAt, observation.ETag = metadata.ObservedAt, metadata.ETag
			observation.LastModified, observation.SourceURL = metadata.LastModified, metadata.SourceURL
			prepared, err := PrepareTMDBMovie(tmdbMoviePolicy(t), observation, doc.Body())
			if err != nil {
				t.Fatal(err)
			}
			// Independent known SHA256 over ALL474 decoded bytes, including the
			// excluded families. Never a compressed/selected/normalized digest.
			known, _ := hex.DecodeString("2df3a373feb6960b20f53ba541a43b2fcdba9c4e5c44a60d80e4763d0da8fd59")
			var digest [32]byte
			copy(digest[:], known)
			want, err := writer.NewEvidenceHeader(writer.EvidenceHeaderInput{
				ID: "evidence:149", SubjectID: 151, Provider: "tmdb", SubjectKind: "movie", ProviderID: "42",
				ParserVersion: TMDBMovieParserVersion, RequestShape: TMDBMovieRequestShape, Language: "en-CA", Region: "CA",
				ObservationDigest: digest, ObservedAt: wantMetadata.ObservedAt, SourceURL: wantMetadata.SourceURL, ETag: wantMetadata.ETag, LastModified: wantMetadata.LastModified,
				Title: "Title:101", OriginalTitle: "Original:103", Overview: "Overview é:107", Tagline: "Tagline:109", ReleaseDate: "2024-02-29", Year: 2024, LengthMillis: writer.EvidenceInteger{Value: 7260000, Present: true},
			})
			if err != nil || prepared.Header() != want {
				t.Fatal("decoded full-entity SHA or metadata substituted", err)
			}
			header.Set("Etag", "later")
			header.Set("Last-Modified", "later")
			reused, err := tr.GetDocument(t.Context(), documentKey())
			if err != nil || reused.Metadata() != wantMetadata || calls.Load() != 1 || clockCalls.Load() != 1 || tr.Stats().Reused != 1 {
				t.Fatal("cache invented a new observation", err)
			}
			if tr.Stats().CacheBytes > 16<<20 || tr.Stats().CacheMetadataBytes == 0 || tr.Stats().Bytes != 474 {
				t.Fatal("unbounded/additional metadata cache", tr.Stats())
			}
		})
	}
}

func TestTMDBHTTPDecodedLimitAndMalformedEncoding(t *testing.T) {
	for _, test := range []struct {
		name, encoding string
		decoded        int
		corrupt        bool
		oversize       bool
	}{
		{"exact", "gzip", 4194304, false, false}, {"decoded_over", "gzip", 4194305, false, true},
		{"identity_over", "", 4194305, false, true}, {"bad_crc", "gzip", 16, true, false}, {"unsupported", "br", 16, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := bytes.Repeat([]byte("x"), test.decoded)
			if test.encoding == "gzip" {
				wire = tmdbHTTPGzip(t, wire)
				if test.corrupt {
					wire[len(wire)-8] ^= 1
				}
			}
			body := &tmdbHTTPBody{Reader: bytes.NewReader(wire)}
			tr, err := newTMDBHTTPTransport(tmdbMoviePolicy(t), admissionFunc(allowed), tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(r *http.Request) (*http.Response, error) {
				tmdbHTTPWritten(r)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": {test.encoding}}, Body: body, ContentLength: 1}, nil
			}), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			doc, err := tr.GetDocument(t.Context(), documentKey())
			if !body.closed.Load() {
				t.Fatal("body not closed")
			}
			if test.name == "exact" {
				if err != nil || len(doc.Body()) != 4194304 {
					t.Fatal("exact decoded bound refused", err)
				}
				return
			}
			if err == nil || doc.Body() != nil || doc.Metadata() != (AcquisitionMetadata{}) || tr.Stats().CacheBytes != 0 {
				t.Fatal("partial/malformed response escaped", err)
			}
			if test.oversize {
				var size *ResponseSizeError
				if !errors.As(err, &size) || size.LimitBytes != 4194304 || size.ObservedBytes != 4194305 || !size.ObservedIsLowerBound {
					t.Fatal("decoded limit+1 diagnostic lost", err)
				}
			}
		})
	}
}

func TestTMDBHTTPNoRedirectRetryOrCredentialErrorEcho(t *testing.T) {
	for _, test := range []struct {
		name                       string
		status                     int
		transportError, closeError bool
	}{
		{"redirect", 302, false, false}, {"driver_error", 200, true, false}, {"close_error", 200, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &tmdbHTTPBody{Reader: strings.NewReader(tmdbMovieFixture)}
			if test.closeError {
				body.closeError = errors.New("synthetic-noncredential")
			}
			var calls atomic.Int32
			tr, err := newTMDBHTTPTransport(tmdbMoviePolicy(t), admissionFunc(allowed), tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				tmdbHTTPWritten(r)
				response := &http.Response{StatusCode: test.status, Header: http.Header{"Location": {"https://elsewhere.invalid"}}, Body: body}
				if test.transportError {
					return response, errors.New("Authorization: Bearer synthetic-noncredential")
				}
				return response, nil
			}), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			doc, err := tr.GetDocument(t.Context(), documentKey())
			if err == nil || strings.Contains(err.Error(), "synthetic") || calls.Load() != 1 || !body.closed.Load() || doc.Body() != nil || tr.Stats().CacheBytes != 0 {
				t.Fatal("redirect/retry/credential leak", err)
			}
		})
	}
}

func TestTMDBHTTPCooldownSurvivesCancellationAndCloseFailure(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		body := &tmdbHTTPBody{Reader: strings.NewReader("discard"), closeError: errors.New("synthetic-noncredential")}
		cancelScope := func() {}
		tr, err := newTMDBHTTPTransport(tmdbMoviePolicy(t), admissionFunc(allowed), tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(r *http.Request) (*http.Response, error) {
			tmdbHTTPWritten(r)
			if cancelRequest {
				cancelScope()
			}
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"2", "172800"}}, Body: body}, nil
		}), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		original := tr.fetch
		tr.fetch = func(ctx context.Context, k RequestKey, n int64, d Dispatch) (Response, error) {
			child, cancel := context.WithCancel(ctx)
			cancelScope = cancel
			defer cancel()
			return original(child, k, n, d)
		}
		doc, err := tr.GetDocument(t.Context(), documentKey())
		var provider *ProviderError
		cause := ErrTMDBHTTP
		if cancelRequest {
			cause = context.Canceled
		}
		if !errors.As(err, &provider) || !provider.Suspended || provider.RetryAfter < 48*time.Hour-time.Second || !errors.Is(err, cause) || !body.closed.Load() || doc.Body() != nil {
			t.Fatal("provider floor lost to local cancellation/close", err)
		}
		tr.Close()
	}
}

func TestTMDBHTTPPolicyPoolsAndClosedScope(t *testing.T) {
	for _, mode := range []TMDBMode{TMDBOneWorker80MiB, TMDBTwoWorkers128MiB} {
		policy, _ := NewTMDBPolicy(mode)
		cfg := tmdbHTTPHarnessConfig()
		tr, err := NewTMDBHTTPTransport(policy, admissionFunc(allowed), cfg) // config only, no call
		if err != nil {
			t.Fatal(err)
		}
		tr.Close()
		limits, _ := policy.Limits()
		if tr.cfg.MaxInFlight != limits.Workers || tr.cfg.MaxPending != limits.Workers || tr.cfg.MaxWaiters != limits.Workers || tr.cfg.CacheBytes != 16<<20 || tr.cfg.MaxMetadataBytes != 1024 || tr.cfg.MaxResponseBytes != 4194304 {
			t.Fatal("unbudgeted pools", tr.cfg)
		}
		rt := tmdbHTTPRoundTripper(cfg, limits.Workers)
		if rt.Proxy != nil || !rt.DisableKeepAlives || !rt.DisableCompression || !rt.Protocols.HTTP1() || rt.Protocols.HTTP2() || rt.MaxConnsPerHost != limits.Workers || rt.MaxResponseHeaderBytes != 4096 {
			t.Fatal("implicit retry/proxy/decode/header pool")
		}
		rt.CloseIdleConnections()
	}
	for _, mutate := range []func(*TMDBHTTPConfig){
		func(c *TMDBHTTPConfig) { c.BearerToken = "" }, func(c *TMDBHTTPConfig) { c.BearerToken = "bad\r\nheader" },
		func(c *TMDBHTTPConfig) { c.MaxMetadataBytes = 0 }, func(c *TMDBHTTPConfig) { c.MaxHeaderBytes = 16 << 20 },
	} {
		cfg := tmdbHTTPHarnessConfig()
		mutate(&cfg)
		if tr, err := NewTMDBHTTPTransport(tmdbMoviePolicy(t), admissionFunc(allowed), cfg); err == nil || tr != nil {
			if tr != nil {
				tr.Close()
			}
			t.Fatal("missing/over-budget background config accepted")
		}
	}
	for _, mutate := range []func(*RequestKey){
		func(k *RequestKey) { k.Authority = "different-generation" }, func(k *RequestKey) { k.ExternalID = "42/../43" },
		func(k *RequestKey) { k.Shape = "different-family" },
	} {
		tr, err := newTMDBHTTPTransport(tmdbMoviePolicy(t), admissionFunc(allowed), tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(*http.Request) (*http.Response, error) {
			t.Error("invalid scope dispatched")
			return nil, ErrTMDBHTTP
		}), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		key := documentKey()
		mutate(&key)
		if doc, err := tr.GetDocument(t.Context(), key); err == nil || doc.Body() != nil || tr.Stats().Attempts != 0 {
			t.Error("invalid scope accepted", err)
		}
		tr.Close()
	}
}

func TestTMDBHTTPCallbackPanicAndMissingDispatchFailClosed(t *testing.T) {
	for _, panicCallback := range []bool{false, true} {
		body := &tmdbHTTPBody{Reader: strings.NewReader(tmdbMovieFixture)}
		fetch := tmdbHTTPFetch(tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(r *http.Request) (*http.Response, error) {
			if panicCallback {
				tmdbHTTPWritten(r)
			}
			return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
		}), time.Now)
		out, err := fetch(t.Context(), documentKey(), 4194304, Dispatch{check: func() error { return nil }, started: func() error { panic("synthetic callback failure") }})
		if err == nil || out.Body != nil || out.Metadata != (AcquisitionMetadata{}) || !body.closed.Load() || (panicCallback && !errors.Is(err, ErrWorkerPanic)) {
			t.Fatal("callback panic/missing ack escaped", err)
		}
	}
}

func TestTMDBHTTPLateCooldownClosesConnectionBeforeWrite(t *testing.T) {
	conn := &tmdbHTTPConn{}
	checks, writes := 0, 0
	floor := &ProviderError{Status: 429, RetryAfter: 48 * time.Hour, Suspended: true}
	fetch := tmdbHTTPFetch(tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(r *http.Request) (*http.Response, error) {
		httptrace.ContextClientTrace(r.Context()).GotConn(httptrace.GotConnInfo{Conn: conn})
		if !conn.closed || r.Context().Err() == nil {
			t.Error("cooldown did not stop the acquired connection")
		}
		return nil, r.Context().Err()
	}), time.Now)
	out, err := fetch(t.Context(), documentKey(), 4194304, Dispatch{check: func() error {
		checks++
		if checks == 2 {
			return floor
		}
		return nil
	}, started: func() error { writes++; return nil }})
	var provider *ProviderError
	if !errors.As(err, &provider) || provider.RetryAfter != 48*time.Hour || checks != 2 || writes != 0 || out.Body != nil {
		t.Fatal("late floor lost or request written", err)
	}
}

func TestTMDBHTTPLateCloseCancellationZeroesResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := &tmdbHTTPBody{Reader: strings.NewReader(tmdbMovieFixture), onClose: cancel}
	fetch := tmdbHTTPFetch(tmdbHTTPHarnessConfig(), tmdbHTTPFixture(func(r *http.Request) (*http.Response, error) {
		tmdbHTTPWritten(r)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	}), time.Now)
	out, err := fetch(ctx, documentKey(), 4194304, Dispatch{check: func() error { return nil }, started: func() error { return nil }})
	if !errors.Is(err, context.Canceled) || out.Body != nil || out.Metadata != (AcquisitionMetadata{}) || !body.closed.Load() {
		t.Fatal("late close cancellation leaked a response", err)
	}
}
