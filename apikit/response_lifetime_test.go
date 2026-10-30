package apikit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type lifetimeWriter struct {
	*httptest.ResponseRecorder
	t        *testing.T
	released *int
}

func (w lifetimeWriter) Write(b []byte) (int, error) {
	if *w.released != 0 {
		w.t.Fatal("delivery released before write")
	}
	return w.ResponseRecorder.Write(b)
}

func TestAfterResponseRetainsDeliveryThroughWriteAndFailure(t *testing.T) {
	if AfterResponse(context.Background(), func() { t.Fatal("unexpected ownership") }) {
		t.Fatal("accepted outside typed handler")
	}
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "error"}[failure], func(t *testing.T) {
			released := 0
			r := New(func(context.Context, *http.Request, Metadata) error { return nil }, nil)
			err := Register(r, Route[testRequest, testResponse]{Metadata: testMetadata(), Handler: func(ctx context.Context, _ *http.Request, _ testRequest) (testResponse, error) {
				if !AfterResponse(ctx, func() { released++ }) {
					t.Fatal("missing typed lifetime")
				}
				if failure {
					return testResponse{}, errors.New("expected failure")
				}
				return testResponse{Name: "ready", Items: []string{}}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			w := lifetimeWriter{httptest.NewRecorder(), t, &released}
			q := httptest.NewRequest("POST", "/v1/test", strings.NewReader(`{"name":"test"}`))
			q.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, q)
			if released != 1 {
				t.Fatalf("released %d times", released)
			}
			if !failure && w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
