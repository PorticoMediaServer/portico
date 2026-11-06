package enrich

import (
	"compress/gzip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var ErrTMDBHTTP = errors.New("TMDB HTTP acquisition failed")

// TMDBHTTPConfig is trusted background composition, never a provider/job input.
// BearerToken comes from the credential owner; it is not an API-key query.
// Authority is its NON-SECRET generation, matching the broker RequestKey.
// Header/metadata limits are explicit resource allocations, not content limits.
// Missing policy refuses construction of background acquisition, not startup.
type TMDBHTTPConfig struct {
	BearerToken, Authority           string
	MaxHeaderBytes, MaxMetadataBytes int64
	CacheTTL, RequestTimeout         time.Duration
}

// NewTMDBHTTPTransport constructs the actual fixed-origin, single-attempt HTTP
// adapter INSIDE the existing TMDB broker pools. Construction does no network
// I/O. Source staging/sealing/authority remain separate writer operations.
func NewTMDBHTTPTransport(policy TMDBPolicy, admission NetworkAdmission, cfg TMDBHTTPConfig) (*Transport, error) {
	return newTMDBHTTPTransport(policy, admission, cfg, nil, time.Now)
}

// The RoundTripper/clock injection is private and used ONLY by mocked tests.
// Production owns its transport: no external client, proxy, redirects or retry
// middleware can redirect credentials or bypass broker dispatch accounting.
func newTMDBHTTPTransport(policy TMDBPolicy, admission NetworkAdmission, input TMDBHTTPConfig, roundTripper http.RoundTripper, now func() time.Time) (*Transport, error) {
	cfg, err := tmdbTransportConfig(policy, input.CacheTTL, input.RequestTimeout)
	if err != nil {
		return nil, err
	}
	// Reserve conservative header/object/metadata scratch and credential copies
	// inside 8 MiB of the existing 16 MiB bookkeeping line. Keep the other 8 MiB
	// for broker keys/maps/entries and headroom, never another retained pool.
	// This is an admission forecast, not physical RAM qualification.
	const bookkeeping = int64(16 << 20)
	if now == nil || input.MaxHeaderBytes <= 0 || input.MaxHeaderBytes > bookkeeping || input.MaxMetadataBytes <= 0 || input.MaxMetadataBytes > bookkeeping ||
		len(input.BearerToken) > int(bookkeeping) ||
		int64(cfg.MaxInFlight)*(16*input.MaxHeaderBytes+4*input.MaxMetadataBytes+16<<10)+4*cacheBodyCharge(len(input.BearerToken))+cacheBodyCharge(len(input.Authority)) > bookkeeping/2 ||
		!validTMDBBearer(input.BearerToken) || input.Authority == "" || len(input.Authority) > 1024 ||
		!utf8.ValidString(input.Authority) || strings.IndexByte(input.Authority, 0) >= 0 || strings.Contains(input.Authority, input.BearerToken) {
		return nil, ErrTMDBPolicy
	}
	cfg.MaxMetadataBytes = input.MaxMetadataBytes
	input.BearerToken, input.Authority = strings.Clone(input.BearerToken), strings.Clone(input.Authority)
	if roundTripper == nil {
		roundTripper = tmdbHTTPRoundTripper(input, cfg.MaxInFlight)
	}
	return NewTransport(cfg, admission, tmdbHTTPFetch(input, roundTripper, now))
}

func validTMDBBearer(token string) bool {
	if token == "" {
		return false
	}
	for i := range token {
		b := token[i]
		if (b < 'a' || b > 'z') && (b < 'A' || b > 'Z') && (b < '0' || b > '9') && !strings.ContainsRune("-._~+/=", rune(b)) {
			return false
		}
	}
	return true
}

func tmdbHTTPRoundTripper(cfg TMDBHTTPConfig, workers int) *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	return &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: cfg.RequestTimeout}).DialContext,
		Protocols: protocols, DisableKeepAlives: true, DisableCompression: true,
		MaxConnsPerHost: workers, MaxResponseHeaderBytes: cfg.MaxHeaderBytes,
		TLSHandshakeTimeout: cfg.RequestTimeout, ResponseHeaderTimeout: cfg.RequestTimeout,
	}
}

// Fresh HTTP/1 connections deliberately prevent net/http's hidden retry on a
// reused connection or HTTP/2 stream. RoundTrip performs no redirect following.
// Connection reuse/throughput needs separate qualification before replacing
// this single-dispatch boundary; no 32–40 req/s qualification is claimed here.
func tmdbHTTPFetch(cfg TMDBHTTPConfig, roundTripper http.RoundTripper, now func() time.Time) Fetch {
	return func(ctx context.Context, key RequestKey, limit int64, dispatch Dispatch) (out Response, err error) {
		if ctx == nil || !key.valid() || key.Provider != "tmdb" || key.Kind != "movie" || key.Shape != TMDBMovieRequestShape || key.Authority != cfg.Authority ||
			!utf8.ValidString(key.Locale) || strings.IndexByte(key.Locale, 0) >= 0 || limit != 4<<20 {
			return out, ErrTMDBHTTP
		}
		safe, err := SafeTMDBSourceURL("https://api.themoviedb.org/3/movie/"+key.ExternalID, key.ExternalID)
		if err != nil {
			return out, ErrTMDBHTTP
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		// Callback code can run on net/http's write goroutine. Contain failures
		// INSIDE that callback, with no external calls under the state mutex.
		var mu sync.Mutex
		var boundaryErr error
		wrote := false
		acknowledged := make(chan struct{})
		fail := func(cause error) {
			mu.Lock()
			if boundaryErr == nil {
				boundaryErr = cause
			}
			mu.Unlock()
			cancel()
		}
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			// A provider cooldown can arrive while DNS/TLS is in progress.
			// Recheck at the acquired fresh connection BEFORE HTTP bytes. Closing
			// that exclusive HTTP/1 connection makes refusal effective even if
			// net/http's writer has not yet noticed context cancellation.
			refuse := func(err error) {
				if info.Conn != nil {
					_ = info.Conn.Close()
				}
				fail(err)
			}
			defer func() {
				if recover() != nil {
					refuse(ErrWorkerPanic)
				}
			}()
			if info.Reused {
				refuse(ErrDispatch)
			} else if err := dispatch.Check(); err != nil {
				refuse(err)
			}
		}, WroteRequest: func(info httptrace.WroteRequestInfo) {
			mu.Lock()
			duplicate := wrote
			wrote = true
			mu.Unlock()
			if duplicate {
				fail(ErrDispatch)
				return
			}
			defer close(acknowledged)
			defer func() {
				if recover() != nil {
					fail(ErrWorkerPanic)
				}
			}()
			// A possibly partial request still consumes a pacing slot.
			if err := dispatch.Started(); err != nil {
				fail(err)
			} else if info.Err != nil {
				fail(ErrTMDBHTTP)
			}
		}}
		query := url.Values{"append_to_response": {"credits"}, "language": {key.Locale}}
		request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, safe+"?"+query.Encode(), nil)
		if err != nil {
			return out, ErrTMDBHTTP
		}
		request.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Accept-Encoding", "gzip")
		if err := dispatch.Check(); err != nil {
			return out, err
		}
		response, transportErr := roundTripper.RoundTrip(request)
		// Any nonnil response body is ours even on a transport failure. Close it
		// BEFORE broker admission retires; never drain an unbounded error body.
		if response != nil && response.Body != nil {
			defer func() {
				if response.Body.Close() != nil && err == nil {
					err = ErrTMDBHTTP
				}
				if err == nil {
					err = ctx.Err()
				}
				if err != nil {
					out.Body, out.Metadata = nil, AcquisitionMetadata{}
				}
			}()
		}
		if response == nil {
			mu.Lock()
			dispatchErr := boundaryErr
			mu.Unlock()
			if dispatchErr != nil {
				return out, dispatchErr
			}
			return out, safeHTTPError(ctx, transportErr)
		}
		out.Status = response.StatusCode
		observed := now()
		if out.Status == http.StatusTooManyRequests || out.Status >= 500 {
			for _, value := range response.Header.Values("Retry-After") {
				out.RetryAfter = max(out.RetryAfter, ParseRetryAfter(value, observed))
			}
		}
		mu.Lock()
		dispatched := wrote
		mu.Unlock()
		if dispatched {
			select {
			case <-acknowledged:
			case <-ctx.Done():
			}
		}
		mu.Lock()
		dispatchErr := boundaryErr
		mu.Unlock()
		if dispatchErr != nil {
			return out, dispatchErr
		}
		if transportErr != nil || response.Body == nil {
			return out, safeHTTPError(ctx, transportErr)
		}
		if !dispatched {
			return out, ErrDispatch
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if out.Status != http.StatusOK {
			return out, nil
		}
		// Real net/http bounds headers during parsing. Check again before
		// retaining values, including the injected component transport boundary.
		if !tmdbHTTPHeadersFit(response.Header, cfg.MaxHeaderBytes) || len(response.Header.Values("ETag")) > 1 || len(response.Header.Values("Last-Modified")) > 1 {
			return out, ErrTMDBHTTP
		}
		metadata, _, err := freezeAcquisitionMetadata(key, AcquisitionMetadata{
			ObservedAt: observed.UTC().Format(time.RFC3339Nano), ETag: response.Header.Get("ETag"),
			LastModified: response.Header.Get("Last-Modified"), SourceURL: safe,
		}, cfg.MaxMetadataBytes)
		if err != nil {
			return out, err
		}
		reader := response.Body
		encodings := response.Header.Values("Content-Encoding")
		if len(encodings) > 1 {
			return out, ErrTMDBHTTP
		}
		encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
		switch encoding {
		case "", "identity":
		case "gzip":
			decoded, err := gzip.NewReader(response.Body)
			if err != nil {
				return out, ErrTMDBHTTP
			}
			defer decoded.Close()
			reader = decoded
		default:
			return out, ErrTMDBHTTP
		}
		body, err := readTMDBResponse(reader, limit)
		if err != nil {
			var size *ResponseSizeError
			if errors.As(err, &size) {
				return out, size
			}
			return out, safeHTTPError(ctx, err)
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Body, out.Metadata = body, metadata
		return out, nil
	}
}

func safeHTTPError(ctx context.Context, cause error) error {
	// Never expose a driver/url/body error that could echo an Authorization
	// header, provider document or credential. Cancellation remains Is-able.
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrTMDBHTTP, err)
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(cause, err) {
			return errors.Join(ErrTMDBHTTP, err)
		}
	}
	return ErrTMDBHTTP
}

func tmdbHTTPHeadersFit(headers http.Header, limit int64) bool {
	var size int64
	for key, values := range headers {
		for _, value := range values {
			cost := int64(len(key)) + int64(len(value)) + 4
			if cost > limit-size {
				return false
			}
			size += cost
		}
	}
	return true
}
