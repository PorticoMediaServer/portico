package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// identityProofFixture is the real identity-proof handler over a real database
// and protected key, as the server composes it.
func identityProofFixture(t *testing.T) (http.Handler, Dependencies) {
	t.Helper()
	state := t.TempDir()
	if e := os.Chmod(state, 0700); e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(filepath.Join(state, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	gate, e := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if e != nil {
		t.Fatal(e)
	}
	runner, e := networking.NewAuthorityRunner(func(ctx context.Context) (networking.LifecycleLease, error) { return gate.Acquire(ctx) })
	if e != nil {
		t.Fatal(e)
	}
	keys, e := networking.OpenCurrentKeys(context.Background(), db, state, runner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { keys.Close() })
	ident, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	proof := networking.NewRouteIdentityHandler(keys, runner, nil, nil)
	return proof, Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), RouteIdentity: proof}
}

// keepAliveClient sends every request over one reused connection, the way an
// app's URLSession does, and records which connection each request used.
type keepAliveClient struct {
	client *http.Client
	origin string
	mu     sync.Mutex
	conns  []string
}

func newKeepAliveClient(server *httptest.Server) *keepAliveClient {
	return &keepAliveClient{client: &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}}, origin: server.URL}
}
func (c *keepAliveClient) do(t *testing.T, method, path string, body []byte) (*http.Response, []byte, time.Duration) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, e := http.NewRequest(method, c.origin+path, reader)
	if e != nil {
		t.Fatal(e)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		c.mu.Lock()
		c.conns = append(c.conns, info.Conn.LocalAddr().String())
		c.mu.Unlock()
	}}))
	start := time.Now()
	resp, e := c.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, raw, time.Since(start)
}
func (c *keepAliveClient) prove(t *testing.T) (*http.Response, []byte, time.Duration) {
	t.Helper()
	nonce := make([]byte, 32)
	if _, e := rand.Read(nonce); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]string{"baseUrl": c.origin, "nonce": base64.RawURLEncoding.EncodeToString(nonce)})
	return c.do(t, http.MethodPost, "/v1/networking/identity-proof", body)
}
func (c *keepAliveClient) distinctConnections() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	for _, v := range c.conns {
		seen[v] = true
	}
	return len(seen)
}

// TestCanceledConnectionIsServedAndClosed covers any other way a keep-alive
// connection's context gets canceled while the socket stays usable: the next
// request is still served, and the connection is closed after it so the client
// moves to a fresh one.
func TestCanceledConnectionIsServedAndClosed(t *testing.T) {
	_, deps := identityProofFixture(t)
	router := New(deps)
	outer := http.NewServeMux()
	// Any handler that sets a read deadline on a bodyless request and outlives it.
	outer.HandleFunc("GET /poison", func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		<-r.Context().Done()
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		w.WriteHeader(204)
	})
	outer.Handle("/", router)
	server := httptest.NewServer(outer)
	defer server.Close()
	client := newKeepAliveClient(server)

	if resp, raw, _ := client.prove(t); resp.StatusCode != 200 {
		t.Fatalf("proof before the poisoning request: %d %s", resp.StatusCode, raw)
	}
	client.do(t, http.MethodGet, "/poison", nil)
	resp, raw, _ := client.prove(t)
	if resp.StatusCode != 200 {
		t.Fatalf("proof on the canceled connection: %d %s", resp.StatusCode, raw)
	}
	if !resp.Close && resp.Header.Get("Connection") != "close" {
		t.Fatal("the canceled connection was kept alive")
	}
	for range 3 {
		if resp, raw, _ := client.prove(t); resp.StatusCode != 200 || resp.Close {
			t.Fatalf("proof on the fresh connection: %d %s close=%v", resp.StatusCode, raw, resp.Close)
		}
	}
	if n := client.distinctConnections(); n != 2 {
		t.Fatalf("requests used %d connections, want the first and one fresh one", n)
	}
}

// A request served on a canceled connection has no cancellation left, and the
// ordinary lanes bound it with their budget instead. The long owner operations
// have no budget: there, such a request could run with nothing to end it, so
// it is turned away, retryable, and the client's retry arrives on a fresh
// connection whose disconnect can be seen.
func TestLongOwnerOperationIsNotStartedOnACanceledConnection(t *testing.T) {
	_, deps := identityProofFixture(t)
	router := New(deps)
	outer := http.NewServeMux()
	outer.HandleFunc("GET /poison", func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		<-r.Context().Done()
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		w.WriteHeader(204)
	})
	outer.Handle("/", router)
	server := httptest.NewServer(outer)
	defer server.Close()
	client := newKeepAliveClient(server)

	for _, path := range []string{"/v1/admin/diagnostics/integrity", "/v1/admin/diagnostics/integrity/repair", "/v1/admin/backups/restore"} {
		// On a healthy connection the request reaches the route, which asks who is calling.
		if resp, raw, _ := client.do(t, http.MethodPost, path, []byte(`{}`)); resp.StatusCode != 401 {
			t.Fatalf("%s on a healthy connection: %d %s", path, resp.StatusCode, raw)
		}
		client.do(t, http.MethodGet, "/poison", nil)
		resp, raw, _ := client.do(t, http.MethodPost, path, []byte(`{}`))
		if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" || !strings.Contains(string(raw), `"retryable":true`) {
			t.Fatalf("%s was started on a canceled connection: %d %s", path, resp.StatusCode, raw)
		}
		if !resp.Close && resp.Header.Get("Connection") != "close" {
			t.Fatalf("%s: the canceled connection was kept alive", path)
		}
		// The retry is on a fresh connection and is served normally.
		if resp, raw, _ := client.do(t, http.MethodPost, path, []byte(`{}`)); resp.StatusCode != 401 || resp.Close {
			t.Fatalf("%s retried on a fresh connection: %d %s close=%v", path, resp.StatusCode, raw, resp.Close)
		}
	}
}
