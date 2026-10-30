package remotemedia

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServeEndpoint is private supervisor authority, never an owner-supplied URL or
// an Approval for an upstream provider. It admits one numeric loopback port.
// The source supervisor must independently enforce the provider's egress policy.
type ServeEndpoint struct {
	root          url.URL
	authorization string
	client        *http.Client
	budget        *OperationBudget
	rangesMu      sync.Mutex
	ranges        map[sharedRangeKey]*sharedRange
	coalesced     uint64
}

type serveReconnectKey struct{}
type serveListingReadKey struct{}

type serveAdmissionFailure struct{ cause error }

func (e *serveAdmissionFailure) Error() string { return "remote source admission refused" }
func (e *serveAdmissionFailure) Unwrap() error { return e.cause }

func NewServeEndpoint(port int, username, password string, budget *OperationBudget) (*ServeEndpoint, error) {
	if port < 1 || port > 65535 || budget == nil || username == "" || password == "" || len(username) > 1024 || len(password) > 8192 || strings.ContainsAny(username, ":\r\n\x00") || strings.ContainsAny(password, "\r\n\x00") {
		return nil, ErrPolicy
	}
	connections := budget.Counts().Capacity
	if budget.life == nil || connections < 1 || connections > 256 {
		return nil, ErrPolicy
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true,
		MaxConnsPerHost: connections, MaxIdleConns: connections, MaxIdleConnsPerHost: connections, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || target != address {
				return nil, ErrPolicy
			}
			check, ok := ctx.Value(serveReconnectKey{}).(func() error)
			if !ok {
				return nil, ErrPolicy
			}
			if err := check(); err != nil {
				return nil, &serveAdmissionFailure{cause: err}
			}
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", address)
			if err != nil {
				return nil, ErrUnavailable
			}
			if err := check(); err != nil {
				conn.Close()
				return nil, &serveAdmissionFailure{cause: err}
			}
			deadline := 15 * time.Second
			if ctx.Value(serveListingReadKey{}) == true {
				deadline = 30 * time.Minute
			}
			return &timedConn{Conn: conn, timeout: deadline}, nil
		},
	}
	return &ServeEndpoint{
		root:          url.URL{Scheme: "http", Host: address, Path: "/"},
		authorization: "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
		client:        &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		budget:        budget,
	}, nil
}

func (s *ServeEndpoint) Close() { s.client.CloseIdleConnections() }

// ReadRange never retries a partial body, follows redirects, or falls back to
// an unconditioned GET. Every transport reconnect checks the captured admission.
// A served ETag is only an additional precondition, not provider identity.
// A separate observation/authority fence must surround this operation.
func (s *ServeEndpoint) ReadRange(ctx context.Context, relative, etag string, size, offset, length int64, reconnect func(context.Context) error) ([]byte, error) {
	if reconnect == nil {
		return nil, ErrPolicy
	}
	if relative == "" || !cleanDAVRelative(relative) || strings.Contains(relative, ":") || !serveETag(etag) || size <= 0 || offset < 0 || length < 1 || length > 1<<20 || offset >= size || length > size-offset {
		return nil, ErrRepresentationRange
	}
	// Each viewer checks its own authority/observation before joining. The
	// shared transfer never supplies that permission to another viewer.
	if err := reconnect(ctx); err != nil {
		return nil, err
	}
	key := sharedRangeKey{relative, etag, size, offset, length}
	if shared := s.joinRange(key); shared != nil {
		return s.rangeResult(ctx, shared, reconnect)
	}
	parent := ctx
	ctx, release, err := s.budget.enter(parent)
	if err != nil {
		return nil, err
	}
	// A matching transfer may have started while this request waited for its
	// slot. Return the extra reservation before joining that one operation.
	shared, leader := s.startRange(key)
	if !leader {
		release()
		return s.rangeResult(parent, shared, reconnect)
	}
	data, err := s.readRange(ctx, relative, etag, size, offset, length, reconnect)
	// Release the provider operation before each viewer's post-read metadata
	// fence. Otherwise a one-slot source would wait on its own completed read.
	release()
	s.finishRange(key, shared, data, err)
	return s.rangeResult(parent, shared, reconnect)
}

func (s *ServeEndpoint) readRange(ctx context.Context, relative, etag string, size, offset, length int64, reconnect func(context.Context) error) ([]byte, error) {
	if err := reconnect(ctx); err != nil {
		return nil, err
	}
	requestContext := ctx
	ctx = context.WithValue(ctx, serveReconnectKey{}, func() error { return reconnect(requestContext) })
	target := s.root
	target.Path += relative
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, ErrPolicy
	}
	req.Header.Set("Authorization", s.authorization)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Portico/1")
	req.Header.Set("If-Match", etag)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrObservationChanged) {
			return nil, ErrObservationChanged
		}
		return nil, boundRequestError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusNotFound {
		return nil, ErrObservationChanged
	}
	if resp.StatusCode != http.StatusPartialContent {
		return nil, serveResponseError(resp)
	}
	start, end, total, err := parseRepresentationRange(resp.Header.Get("Content-Range"))
	if err != nil || start != offset || end != offset+length-1 || total != size || resp.ContentLength != length || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("ETag") != etag {
		return nil, ErrRepresentationRange
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, length+1))
	if err != nil || int64(len(data)) != length {
		clear(data)
		return nil, ErrRepresentationRange
	}
	// ReadRange closes this response and releases its operation before
	// rangeResult performs each caller's fresh post-read check. Checking again
	// here would repeat the same observation inside the just-completed body;
	// it supplies no additional I/O boundary and spends the source's TPS twice.
	return data, nil
}

func serveETag(tag string) bool {
	if len(tag) < 2 || len(tag) > 4096 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, c := range []byte(tag[1 : len(tag)-1]) {
		if c < 0x21 || c == '"' || c == 0x7f {
			return false
		}
	}
	return true
}
