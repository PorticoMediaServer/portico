package remotemedia

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// PolicyProxy is the private upstream-rclone policy route. HTTPS is an opaque
// CONNECT tunnel: no TLS interception, certificates or response/throttle claim.
// The separate SFTP mode permits only CONNECT to exact native approvals and
// preserves the hostname rclone uses for SSH host-key verification. SMB uses
// numeric hosts in its transient configuration instead of this proxy. Source
// limits belong to the one rclone process.
type PolicyProxy struct {
	policy        Policy
	check         func(context.Context) error
	authorization string
	locator       string
	life          context.Context
	cancel        context.CancelFunc
	server        *http.Server
	listener      net.Listener
	done          chan struct{}
	mu            sync.Mutex
	tunnels       map[net.Conn]bool
	closed        bool
	handlers      sync.WaitGroup
	sftp          map[string]Approval
}

func StartPolicyProxy(ctx context.Context, policy Policy, username, password string, check func(context.Context) error) (*PolicyProxy, error) {
	return startPolicyProxy(ctx, policy, username, password, check, nil)
}

// StartSFTPPolicyProxy supplies rclone's sftp http_proxy option. It is not an
// HTTP approval alias: only the saved SFTP scheme/host/port can use its tunnel.
func StartSFTPPolicyProxy(ctx context.Context, policy Policy, username, password string, check func(context.Context) error) (*PolicyProxy, error) {
	saved := map[string]Approval{}
	for _, approval := range policy.Approvals {
		u, err := url.Parse(approval.Root)
		if err != nil || u.Scheme != "sftp" {
			return nil, ErrPolicy
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			return nil, ErrPolicy
		}
		root, err := (NativeEndpoint{Scheme: "sftp", Host: u.Hostname(), Port: port}).Root()
		if err != nil || root != approval.Root || saved[root].Root != "" {
			return nil, ErrPolicy
		}
		approval.Addresses = append([]string(nil), approval.Addresses...)
		saved[root] = approval
	}
	if len(saved) == 0 || len(saved) > 32 {
		return nil, ErrPolicy
	}
	return startPolicyProxy(ctx, policy, username, password, check, saved)
}

func startPolicyProxy(ctx context.Context, policy Policy, username, password string, check func(context.Context) error, sftp map[string]Approval) (*PolicyProxy, error) {
	if username == "" || password == "" || strings.ContainsAny(username, ":\r\n\x00") || strings.ContainsAny(password, "\r\n\x00") || check == nil {
		return nil, ErrPolicy
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	policy.Approvals = append([]Approval(nil), policy.Approvals...)
	for i := range policy.Approvals {
		policy.Approvals[i].Addresses = append([]string(nil), policy.Approvals[i].Addresses...)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, ErrUnavailable
	}
	life, cancel := context.WithCancel(ctx)
	address := ln.Addr().String()
	locator := url.URL{Scheme: "http", Host: address, User: url.UserPassword(username, password)}
	p := &PolicyProxy{policy: policy, check: check, authorization: "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password)), locator: locator.String(), life: life, cancel: cancel, listener: ln, done: make(chan struct{}), tunnels: make(map[net.Conn]bool), sftp: sftp}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return life }}
	supervise.Go("remotemedia.policy-proxy", func() {
		_ = p.server.Serve(ln)
		p.Close()
		p.handlers.Wait()
		close(p.done)
	})
	context.AfterFunc(life, func() { p.Close() })
	return p, nil
}

// Locator is private command environment material. Never put it in argv,
// observations, API documents, logs or error strings.
func (p *PolicyProxy) Locator() string { return p.locator }
func (p *PolicyProxy) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.life.Err(); err != nil {
		return err
	}
	return p.check(ctx)
}
func (p *PolicyProxy) Close() error {
	p.cancel()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	for conn := range p.tunnels {
		_ = conn.Close()
	}
	p.mu.Unlock()
	_ = p.server.Close()
	return nil
}
func (p *PolicyProxy) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return nil
	}
}
func (p *PolicyProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	p.handlers.Add(1)
	p.mu.Unlock()
	defer p.handlers.Done()
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(p.authorization)) != 1 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Portico"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	if err := p.Check(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	// Only absolute plain HTTP requests belong on the forward route. Redirects
	// return to rclone, whose next request must pass policy independently.
	if p.sftp != nil || r.URL == nil || r.URL.Scheme != "http" || r.URL.Host == "" || r.Host != r.URL.Host || r.URL.User != nil || r.URL.Fragment != "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	c, err := New(r.Context(), r.URL.String(), p.policy)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	defer c.Close()
	request := r.Clone(r.Context())
	request.RequestURI = ""
	if request.Body != nil && request.Body != http.NoBody {
		request.Body = http.MaxBytesReader(w, request.Body, 1<<20)
	}
	stripProxyHopHeaders(request.Header)
	response, err := c.client.Transport.RoundTrip(request)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if err = p.Check(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	stripProxyHopHeaders(response.Header)
	for name, values := range response.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	// No body logging, materialization, synthesized retry time or provider state.
	_, _ = io.Copy(w, response.Body)
}

func stripProxyHopHeaders(header http.Header) {
	for _, name := range strings.Split(header.Get("Connection"), ",") {
		header.Del(strings.TrimSpace(name))
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func (p *PolicyProxy) connectAddresses(ctx context.Context, host, port string) ([]netip.Addr, error) {
	if p.sftp != nil {
		numeric, err := strconv.Atoi(port)
		if err != nil {
			return nil, ErrPolicy
		}
		endpoint := NativeEndpoint{Scheme: "sftp", Host: host, Port: numeric}
		root, err := endpoint.Root()
		if err != nil {
			return nil, err
		}
		approval, ok := p.sftp[root]
		if !ok {
			return nil, ErrPolicy
		}
		return p.policy.CheckNativeEndpoint(ctx, endpoint, approval)
	}
	target := url.URL{Scheme: "https", Host: net.JoinHostPort(host, port), Path: "/"}
	// CONNECT can enforce host/port/address policy only. An owner-confirmed HTTPS
	// root's path is opaque; it never becomes an immutable-byte or content grant.
	policy := p.policy
	policy.Approvals = append([]Approval(nil), policy.Approvals...)
	for i, a := range policy.Approvals {
		root, e := url.Parse(a.Root)
		if e == nil && root.Scheme == "https" && origin(root) == origin(&target) {
			root.Path = "/"
			root.RawPath = ""
			policy.Approvals[i].Root = root.String()
		}
	}
	_, scope, err := policy.admit(ctx, target.String())
	if err != nil {
		return nil, err
	}
	return scope.resolve(ctx, host)
}

func (p *PolicyProxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	numeric, e := strconv.Atoi(port)
	if err != nil || e != nil || numeric < 1 || numeric > 65535 || host == "" || strings.ContainsAny(host, "/@\\\x00\r\n") || r.URL == nil || r.URL.Host != r.Host || r.URL.Path != "" || r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ips, err := p.connectAddresses(r.Context(), host, port)
	if err != nil || p.Check(r.Context()) != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var upstream net.Conn
	for _, ip := range ips {
		upstream, err = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(r.Context(), "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			break
		}
	}
	if err != nil || upstream == nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if p.Check(r.Context()) != nil {
		upstream.Close()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	downstream, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		downstream.Close()
		upstream.Close()
		return
	}
	p.tunnels[downstream], p.tunnels[upstream] = true, true
	p.mu.Unlock()
	defer func() {
		downstream.Close()
		upstream.Close()
		p.mu.Lock()
		delete(p.tunnels, downstream)
		delete(p.tunnels, upstream)
		p.mu.Unlock()
	}()
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	// Preserve bytes that arrived with CONNECT. No parsing or modification of TLS.
	done := make(chan struct{}, 1)
	supervise.Go("remotemedia.policy-connect", func() {
		_, _ = io.Copy(&timedConn{Conn: upstream, timeout: 30 * time.Second}, buffered)
		_ = upstream.Close()
		done <- struct{}{}
	})
	_, _ = io.Copy(&timedConn{Conn: downstream, timeout: 30 * time.Second}, &timedConn{Conn: upstream, timeout: 30 * time.Second})
	_ = downstream.Close()
	_ = upstream.Close()
	<-done
}
