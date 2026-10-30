package apikit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

var errStorageFull = errors.New("storage refused the write")

func TestWriteErrorsAreDeclaredOnEveryChangingRouteAndTranslated(t *testing.T) {
	catalogue, err := apierror.New(apierror.Definition{Code: "queue_full", Status: 503, Message: "The server is busy. Try again shortly.", Retry: apierror.SameRequest})
	if err != nil {
		t.Fatal(err)
	}
	catalogue.Translate(func(err error) *apierror.Error {
		if errors.Is(err, errStorageFull) {
			return &apierror.Error{Code: "queue_full", RetryAfterSeconds: 4, Cause: err}
		}
		return nil
	})
	r := New(func(context.Context, *http.Request, Metadata) error { return nil }, catalogue)
	r.WriteErrors("queue_full")
	write := testMetadata()
	read := testMetadata()
	read.ID, read.Method, read.Path, read.BodyLimit = "read", "GET", "/v1/read", 0
	for _, m := range []Metadata{write, read} {
		if err := Register(r, Route[struct{}, testResponse]{Metadata: m, Handler: func(context.Context, *http.Request, struct{}) (testResponse, error) {
			return testResponse{}, errStorageFull
		}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range r.Definitions() {
		if got := slices.Contains(d.Errors, "queue_full"); got != (d.Method != "GET") {
			t.Fatalf("%s %s declares queue_full=%v", d.Method, d.Path, got)
		}
	}
	w := httptest.NewRecorder()
	q := httptest.NewRequest("POST", "/v1/test", strings.NewReader("{}"))
	q.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, q)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"queue_full"`) || w.Header().Get("Retry-After") != "4" {
		t.Fatalf("translated answer %d %s %q", w.Code, w.Body.String(), w.Header().Get("Retry-After"))
	}
	// An error the translator does not know still answers as internal.
	w = httptest.NewRecorder()
	catalogue.Write(w, "", errors.New("other"))
	if w.Code != 500 {
		t.Fatalf("unknown error answered %d", w.Code)
	}
}
