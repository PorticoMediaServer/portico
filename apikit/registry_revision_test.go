package apikit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

type fencedChange struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Name             string `json:"name"`
}

// A change to something that exists names the revision it was made against.
// Leaving the revision out, or sending null, is refused before the handler
// runs: it must never be read as revision 0. An explicit 0 is a revision. A
// POST shares its body with creations and is not held to this.
func TestTypedChangesRequireASentRevision(t *testing.T) {
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
	ran := map[string]int{}
	for _, method := range []string{"PUT", "PATCH", "DELETE", "POST"} {
		m := Metadata{Audience: Clients, ID: "change_" + strings.ToLower(method), Method: method, Path: "/v1/things/" + strings.ToLower(method), Summary: "Change a thing", Access: Device, Requires: ScopeRead, Lane: Default, Cost: Constant, BodyLimit: 256, Status: 200, Errors: []string{"invalid_request"}}
		if err := Register(r, Route[fencedChange, testResponse]{Metadata: m, Handler: func(_ context.Context, q *http.Request, in fencedChange) (testResponse, error) {
			ran[q.Method]++
			return testResponse{Name: in.Name, Items: []string{}}, nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, body string) *httptest.ResponseRecorder {
		q := httptest.NewRequest(method, "/v1/things/"+strings.ToLower(method), strings.NewReader(body))
		q.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		for _, body := range []string{`{"name":"x"}`, `{"expectedRevision":null,"name":"x"}`} {
			if w := call(method, body); w.Code != 428 || !strings.Contains(w.Body.String(), `"code":"revision_required"`) {
				t.Fatalf("%s %s: %d %s", method, body, w.Code, w.Body.String())
			}
		}
		if ran[method] != 0 {
			t.Fatalf("%s reached its handler without a revision", method)
		}
		for _, body := range []string{`{"expectedRevision":0,"name":"x"}`, `{"expectedRevision":7,"name":"x"}`} {
			if w := call(method, body); w.Code != 200 {
				t.Fatalf("%s %s: %d %s", method, body, w.Code, w.Body.String())
			}
		}
		if w := call(method, `{"expectedRevision":"7","name":"x"}`); w.Code != 400 {
			t.Fatalf("%s accepted a revision that is not a number: %d", method, w.Code)
		}
	}
	if w := call("POST", `{"name":"x"}`); w.Code != 200 || ran["POST"] != 1 {
		t.Fatalf("a creation was held to a revision: %d %s", w.Code, w.Body.String())
	}
	// The refusal is part of each such route's published contract.
	for _, d := range r.Definitions() {
		if declared := slices.Contains(d.Errors, "revision_required"); declared != (d.Method != "POST") {
			t.Fatalf("%s declares revision_required: %v", d.Method, declared)
		}
	}
}
