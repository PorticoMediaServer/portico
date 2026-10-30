package remotemedia

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func closePolicyFixture(t *testing.T, p *PolicyProxy) {
	t.Helper()
	p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Wait(ctx); err != nil {
		t.Fatal("policy route did not retire", err)
	}
}

func TestPolicyProxyPinsPlainHTTPAndDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("private proxy credential reached provider")
		}
		if r.URL.Path == "/media/redirect" {
			http.Redirect(w, r, "http://169.254.169.254/latest/", 302)
			return
		}
		if r.URL.Path != "/media/book" {
			t.Error("escaped approved root")
		}
		if r.Header.Get("Authorization") != "Bearer fixture-provider" || r.Header.Get("Range") != "bytes=4-7" {
			t.Error("lost provider headers")
		}
		w.Header().Set("Content-Range", "bytes 4-7/8")
		w.WriteHeader(206)
		io.WriteString(w, "book")
	}))
	defer origin.Close()
	approval, err := (Policy{}).Approve(context.Background(), origin.URL+"/media/")
	if err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	proxy, err := StartPolicyProxy(context.Background(), Policy{Approvals: []Approval{approval}}, "private", "fixture", func(ctx context.Context) error {
		if revoked.Load() {
			return ErrDenied
		}
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closePolicyFixture(t, proxy)
	locator, _ := url.Parse(proxy.Locator())
	transport := &http.Transport{Proxy: http.ProxyURL(locator)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", origin.URL+"/media/book", nil)
	req.Header.Set("Authorization", "Bearer fixture-provider")
	req.Header.Set("Range", "bytes=4-7")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 206 || string(body) != "book" {
		t.Fatal(response.StatusCode, string(body), err)
	}
	response, err = client.Get(origin.URL + "/media/redirect")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 || reached.Load() != 2 {
		t.Fatal("proxy followed redirect", response.StatusCode, reached.Load())
	}
	for _, target := range []string{origin.URL + "/admin", "http://169.254.169.254/latest/"} {
		response, err = client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("unapproved destination reached", response.StatusCode)
		}
	}
	revoked.Store(true)
	response, err = client.Get(origin.URL + "/media/book")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 || reached.Load() != 2 {
		t.Fatal("revoked source reached provider", response.StatusCode, reached.Load())
	}
}

func TestPolicyProxyCONNECTIsOpaquePinnedAndCanceled(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	echoed := make(chan error, 1)
	payload := []byte{0x16, 0x03, 0x03, 0, 4, 0xde, 0xad, 0xbe, 0xef}
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			echoed <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		raw := make([]byte, len(payload))
		if _, err = io.ReadFull(conn, raw); err == nil {
			if !bytes.Equal(raw, payload) {
				err = fmt.Errorf("opaque payload was modified")
			} else {
				_, err = conn.Write(raw)
			}
		}
		echoed <- err
		// Stay live until the proxy closes its source/session context.
		var extra [1]byte
		_, _ = conn.Read(extra[:])
	}()
	target := "https://" + listener.Addr().String() + "/private-root/"
	approval, err := (Policy{}).Approve(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy, err := StartPolicyProxy(life, Policy{Approvals: []Approval{approval}}, "private", "fixture", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closePolicyFixture(t, proxy)
	locator, _ := url.Parse(proxy.Locator())
	conn, err := net.DialTimeout("tcp", locator.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", listener.Addr(), listener.Addr(), base64.StdEncoding.EncodeToString([]byte("private:fixture")))
	buffered := bufio.NewReader(conn)
	response, err := http.ReadResponse(buffered, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("CONNECT", response, err)
	}
	if _, err = conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, len(payload))
	if _, err = io.ReadFull(buffered, raw); err != nil || !bytes.Equal(raw, payload) {
		t.Fatal("TLS bytes changed", raw, err)
	}
	if err = <-echoed; err != nil {
		t.Fatal(err)
	}
	cancel()
	var extra [1]byte
	if _, err = buffered.Read(extra[:]); err == nil {
		t.Fatal("tunnel survived cancellation")
	}
}

func TestPolicyProxyRejectsRebindingAndUnauthenticatedRequests(t *testing.T) {
	var address atomic.Value
	address.Store("127.0.0.1")
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr(address.Load().(string))}, nil
	})
	policy := Policy{Resolver: resolver}
	approval, err := policy.Approve(context.Background(), "http://owner.test:12345/media/")
	if err != nil {
		t.Fatal(err)
	}
	policy.Approvals = []Approval{approval}
	proxy, err := StartPolicyProxy(context.Background(), policy, "private", "fixture", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closePolicyFixture(t, proxy)
	locator, _ := url.Parse(proxy.Locator())
	address.Store("169.254.169.254")
	transport := &http.Transport{Proxy: http.ProxyURL(locator)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Get("http://owner.test:12345/media/book")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("rebound address admitted", response.StatusCode)
	}
	locator.User = nil
	unauth := &http.Transport{Proxy: http.ProxyURL(locator)}
	defer unauth.CloseIdleConnections()
	response, err = (&http.Client{Transport: unauth}).Get("http://owner.test:12345/media/book")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 407 {
		t.Fatal("missing proxy auth accepted", response.StatusCode)
	}
	if strings.Contains(ErrPolicy.Error(), "169.254") {
		t.Fatal("private address in public error")
	}
}
