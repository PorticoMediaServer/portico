package apikit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type refList struct {
	IDs []string `json:"ids" ref:"title"`
}
type refBody struct {
	ItemID  string   `json:"itemId"`
	Note    string   `json:"note"`
	Items   *refList `json:"items,omitempty"`
	Records []struct {
		ItemID string `json:"itemId"`
	} `json:"records,omitempty"`
}

func TestRefsReachTypedBodiesHTTPBodiesAndQueries(t *testing.T) {
	r := New(nil, nil)
	calls := 0
	r.RewriteRefs(Refs{Names: []string{"itemId", "showId"}, Resolve: func(_ *http.Request, values []string) map[string]string {
		calls++
		out := map[string]string{}
		for _, v := range values {
			if v == "old" {
				out[v] = "new"
			}
		}
		return out
	}})
	var got refBody
	m := testMetadata()
	m.ID, m.Method, m.Path, m.Access, m.Requires, m.BodyLimit, m.Status = "start", "POST", "/v1/start", Public, ScopeOpen, 4096, 200
	if err := Register(r, Route[refBody, testResponse]{Metadata: m, Handler: func(_ context.Context, _ *http.Request, in refBody) (testResponse, error) {
		got = in
		return testResponse{"ok", []string{}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	h := testMetadata()
	h.ID, h.Method, h.Path, h.Access, h.Requires, h.BodyLimit, h.Query = "workspace", "POST", "/v1/workspace", Public, ScopeOpen, 4096, []string{"showId"}
	if err := RegisterHTTP[refBody, testResponse](r, h); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var raw, show string
	if err := r.MountHTTP(mux, "POST /v1/workspace", http.HandlerFunc(func(_ http.ResponseWriter, q *http.Request) {
		b, _ := io.ReadAll(q.Body)
		raw, show = string(b), q.URL.Query().Get("showId")
	})); err != nil {
		t.Fatal(err)
	}

	body := `{"itemId":"old","note":"old","items":{"ids":["a","old"]},"records":[{"itemId":"old"}]}`
	q := httptest.NewRequest("POST", "/v1/start", strings.NewReader(body))
	q.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	if w.Code != 200 || got.ItemID != "new" || got.Note != "old" || got.Items.IDs[1] != "new" || got.Items.IDs[0] != "a" || got.Records[0].ItemID != "new" {
		t.Fatalf("typed body not rewritten: %d %+v", w.Code, got)
	}

	q = httptest.NewRequest("POST", "/v1/workspace?showId=old", strings.NewReader(body))
	q.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(httptest.NewRecorder(), q)
	if show != "new" || !strings.Contains(raw, `"itemId":"new"`) || !strings.Contains(raw, `"note":"old"`) {
		t.Fatalf("HTTP request not rewritten: %q %s", show, raw)
	}

	// A body that names no id is passed through without asking.
	calls = 0
	unchanged := `{"note":"old"}`
	q = httptest.NewRequest("POST", "/v1/workspace", strings.NewReader(unchanged))
	q.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(httptest.NewRecorder(), q)
	if calls != 0 || raw != unchanged {
		t.Fatalf("untouched body was resolved or changed: %d %s", calls, raw)
	}
	// A duplicate member is left for the strict decoder to refuse.
	dup := `{"itemId":"old","itemId":"x"}`
	q = httptest.NewRequest("POST", "/v1/workspace", strings.NewReader(dup))
	q.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(httptest.NewRecorder(), q)
	if raw != dup {
		t.Fatalf("duplicate member document was rewritten: %s", raw)
	}
}

type refUnreadBody struct{ t *testing.T }

func (b refUnreadBody) Read([]byte) (int, error) {
	b.t.Error("body read before the handler asked for it")
	return 0, io.EOF
}

// An HTTP handler that refuses or waits before reading (permission, a busy
// mutation slot) never has its body read on its behalf.
func TestRefsLeaveAnUnreadHTTPBodyUnread(t *testing.T) {
	r := New(nil, nil)
	r.RewriteRefs(Refs{Names: []string{"itemId"}, Resolve: func(_ *http.Request, values []string) map[string]string {
		return map[string]string{"old": "new"}
	}})
	h := testMetadata()
	h.ID, h.Method, h.Path, h.Access, h.Requires, h.BodyLimit = "refuse", "POST", "/v1/refuse", Public, ScopeOpen, 4096
	if err := RegisterHTTP[refBody, testResponse](r, h); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := r.MountHTTP(mux, "POST /v1/refuse", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})); err != nil {
		t.Fatal(err)
	}
	q := httptest.NewRequest("POST", "/v1/refuse", refUnreadBody{t})
	q.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, q)
	if w.Code != http.StatusForbidden {
		t.Fatalf("handler not reached: %d", w.Code)
	}
}
