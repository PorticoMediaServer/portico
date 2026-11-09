package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s, err := New(context.Background(), d, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// call sends one request and decodes the JSON reply into out when out is not nil.
func call(t *testing.T, s *Server, method, path, token string, body, out any) *httptest.ResponseRecorder {
	t.Helper()
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
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
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
