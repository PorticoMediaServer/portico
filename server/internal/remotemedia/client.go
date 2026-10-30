package remotemedia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	userAgent       string
	authorization   string
	manualRedirects bool
	url             string
	client          *http.Client
}

func New(ctx context.Context, locator string, policy Policy) (*Client, error) {
	u, scope, e := policy.admit(ctx, locator)
	if e != nil {
		return nil, e
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = 2
	transport.MaxIdleConns = 2
	transport.MaxIdleConnsPerHost = 2
	transport.IdleConnTimeout = 30 * time.Second
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, ErrPolicy
		}
		if scope.root != nil && net.JoinHostPort(strings.ToLower(host), port) != strings.TrimPrefix(origin(scope.root), scope.root.Scheme+"://") {
			return nil, ErrPolicy
		}
		ips, e := scope.resolve(ctx, host)
		if e != nil {
			return nil, e
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				timeout := policy.ReadTimeout
				if timeout <= 0 || timeout > 30*time.Second {
					timeout = 15 * time.Second
				}
				return &timedConn{Conn: conn, timeout: timeout}, nil
			}
			last = e
		}
		if last != nil {
			return nil, ErrUnavailable
		}
		return nil, ErrPolicy
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if policy.ManualRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) >= 5 {
			return ErrPolicy
		}
		if e := scope.checkURL(r.URL); e != nil {
			return e
		}
		if _, e := scope.resolve(r.Context(), r.URL.Hostname()); e != nil {
			return e
		}
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
			r.Header.Del(name)
		}
		return nil
	}}
	agent := policy.UserAgent
	if agent == "" {
		agent = "Portico/1"
	}
	if len(agent) > 200 || strings.ContainsAny(agent, "\x00\r\n") {
		transport.CloseIdleConnections()
		return nil, ErrPolicy
	}
	return &Client{url: u.String(), client: client, userAgent: agent, manualRedirects: policy.ManualRedirects}, nil
}
func (c *Client) Close() { c.client.CloseIdleConnections() }
func (c *Client) Open(ctx context.Context, method, rangeHeader string) (*http.Response, error) {
	if method != "GET" && method != "HEAD" {
		return nil, ErrPolicy
	}
	if len(rangeHeader) > 128 || (rangeHeader != "" && !validRange(rangeHeader)) {
		return nil, errors.New("only one byte range is supported")
	}
	r, e := http.NewRequestWithContext(ctx, method, c.url, nil)
	if e != nil {
		return nil, ErrPolicy
	}
	if c.authorization != "" {
		r.Header.Set("Authorization", c.authorization)
	}
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("User-Agent", c.userAgent)
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	resp, e := c.client.Do(r)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(e, ErrPolicy) {
			return nil, ErrPolicy
		}
		var timeout net.Error
		if errors.As(e, &timeout) && timeout.Timeout() {
			return nil, ErrStalled
		}
		return nil, ErrUnavailable
	}
	if c.manualRedirects && isRedirect(resp.StatusCode) {
		return resp, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, ErrDenied
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 && resp.StatusCode != 416 {
		resp.Body.Close()
		return nil, ErrUnavailable
	}
	if resp.StatusCode == 206 && !validContentRange(rangeHeader, resp) {
		resp.Body.Close()
		return nil, ErrUnavailable
	}
	if resp.StatusCode == 416 {
		resp.Body.Close()
		resp.Body = http.NoBody
		resp.Header.Del("Content-Length")
		if !regexp.MustCompile(`^bytes \*/[0-9]+$`).MatchString(resp.Header.Get("Content-Range")) {
			resp.Header.Del("Content-Range")
		}
	}
	return resp, nil
}

type timedConn struct {
	net.Conn
	timeout time.Duration
}

func (c *timedConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(p)
}
func CopyResponse(w http.ResponseWriter, resp *http.Response, reader io.Reader) error {
	return CopyResponseAs(w, resp, reader, "video/mp4")
}

// CopyResponseAs copies a relayed range response under a media type the
// caller determined; the origin's own Content-Type is never forwarded.
func CopyResponseAs(w http.ResponseWriter, resp *http.Response, reader io.Reader, contentType string) error {
	for _, name := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	if contentType == "" {
		contentType = "video/mp4"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(resp.StatusCode)
	_, e := io.Copy(w, reader)
	return e
}

var rangePattern = regexp.MustCompile(`^bytes=([0-9]*)-([0-9]*)$`)

func validRange(value string) bool {
	parts := rangePattern.FindStringSubmatch(value)
	if parts == nil || parts[1] == "" && parts[2] == "" {
		return false
	}
	var start, end int64
	var e error
	if parts[1] != "" {
		start, e = strconv.ParseInt(parts[1], 10, 64)
		if e != nil {
			return false
		}
	}
	if parts[2] != "" {
		end, e = strconv.ParseInt(parts[2], 10, 64)
		if e != nil {
			return false
		}
		if parts[1] == "" && end == 0 {
			return false
		}
		if parts[1] != "" && end < start {
			return false
		}
	}
	return true
}
func validContentRange(request string, response *http.Response) bool {
	var start, end, total int64
	if n, e := fmt.Sscanf(response.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); e != nil || n != 3 || start < 0 || end < start || total <= end {
		return false
	}
	if response.ContentLength >= 0 && response.ContentLength != end-start+1 {
		return false
	}
	parts := rangePattern.FindStringSubmatch(request)
	if parts == nil {
		return false
	}
	if parts[1] != "" {
		wanted, _ := strconv.ParseInt(parts[1], 10, 64)
		if start != wanted {
			return false
		}
		if parts[2] != "" {
			last, _ := strconv.ParseInt(parts[2], 10, 64)
			if end > last {
				return false
			}
		}
	} else {
		suffix, _ := strconv.ParseInt(parts[2], 10, 64)
		wanted := max(int64(0), total-suffix)
		if start != wanted || end != total-1 {
			return false
		}
	}
	return true
}

func isRedirect(status int) bool {
	return status == 301 || status == 302 || status == 303 || status == 307 || status == 308
}
