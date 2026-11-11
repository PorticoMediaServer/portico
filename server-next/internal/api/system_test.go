package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return openTestServer(t, filepath.Join(t.TempDir(), "main.db"))
}

// openTestServer opens the database at path with cheap password hashing. The server's
// clock is the real one until a test calls setClock. The server stops, as the program stops
// it, before its database closes: when the test ends, or when the test restarts it.
func openTestServer(t *testing.T, path string) *Server {
	t.Helper()
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(d) })
	ctx, cancel := context.WithCancel(context.Background())
	s, err := New(ctx, d, filepath.Dir(path), "test", slog.New(slog.NewTextHandler(testLog(), nil)))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stopWhenDone(t, s, cancel)
	s.passwords = auth.NewPasswords(bcrypt.MinCost, bcrypt.MinCost)
	return s
}

// stops holds how to stop each test server.
var stops sync.Map

// stopWhenDone stops a server as the program does, by ending its context and waiting for its
// background work, when the test ends or when stopServer is called, whichever is first.
func stopWhenDone(t *testing.T, s *Server, cancel context.CancelFunc) {
	if s == nil {
		cancel()
		return
	}
	stops.Store(s, func() {
		cancel()
		s.Wait(context.Background())
	})
	t.Cleanup(func() { stopServer(s) })
}

// stopServer stops a test server's background work and waits for it, once.
func stopServer(s *Server) {
	if stop, ok := stops.LoadAndDelete(s); ok {
		stop.(func())()
	}
}

var closed sync.Map

// closeDB closes a test database once, whether a test restarts its server or just ends.
func closeDB(d *db.DB) {
	if _, done := closed.LoadOrStore(d, true); !done {
		d.Close()
	}
}

func newRequest(method, path, token string, body any) *http.Request {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	r := httptest.NewRequest(method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func jsonInto(w *httptest.ResponseRecorder, out any) error {
	return json.Unmarshal(w.Body.Bytes(), out)
}

// call sends one request and decodes the JSON reply into out when out is not nil.
func call(t *testing.T, s *Server, method, path, token string, body, out any) *httptest.ResponseRecorder {
	t.Helper()
	w := serve(s, newRequest(method, path, token, body))
	if out != nil {
		if err := jsonInto(w, out); err != nil {
			t.Fatalf("%s %s answered %d with a body that is not the expected JSON: %s", method, path, w.Code, w.Body)
		}
	}
	return w
}

// A fresh server says it needs setup, and every refusal, including the router's own, uses
// the one error format with the request id that is also in the header.
func TestSystemAndTheErrorFormat(t *testing.T) {
	s := newTestServer(t)
	var system System
	if w := call(t, s, "GET", "/v1/system", "", nil, &system); w.Code != 200 {
		t.Fatalf("system answered %d", w.Code)
	}
	if !system.SetupRequired || system.APIMajor != 1 || system.Capabilities == nil || !strings.HasPrefix(system.ID, "srv_") {
		t.Fatalf("unexpected system answer: %+v", system)
	}
	for _, c := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"GET", "/v1/nothing-here", "", 404, "not_found"},
		{"POST", "/v1/system", "", 405, "method_not_allowed"},
		{"GET", "/v1/system", `{"a":1}`, 400, "invalid_request"},
	} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var got errorBody
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s %s: body is not the error format: %s", c.method, c.path, w.Body)
		}
		if w.Code != c.status || got.Error.Code != c.code || got.Error.RequestID == "" || got.Error.RequestID != w.Header().Get("X-Request-Id") {
			t.Errorf("%s %s: got %d %+v, want %d %s", c.method, c.path, w.Code, got.Error, c.status, c.code)
		}
	}
	if w := call(t, s, "GET", "/v1/health/ready", "", nil, nil); w.Code != 200 {
		t.Errorf("ready answered %d", w.Code)
	}
}

// A route that needs a sign-in refuses a missing or made-up token before reading the body.
func TestSignedInRoutesRefuseWithoutAToken(t *testing.T) {
	s := newTestServer(t)
	Handle(s, "POST /v1/test/private", SignedIn, func(r *http.Request, _ None) (None, error) { return None{}, nil })
	for _, token := range []string{"", "made-up"} {
		if w := call(t, s, "POST", "/v1/test/private", token, nil, nil); w.Code != 401 {
			t.Errorf("token %q: got %d, want 401", token, w.Code)
		}
	}
}

// testLog is where a test server logs: nowhere, unless PORTICO_TEST_LOG is set.
func testLog() io.Writer {
	if os.Getenv("PORTICO_TEST_LOG") != "" {
		return os.Stderr
	}
	return io.Discard
}
