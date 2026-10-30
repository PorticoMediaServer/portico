package apikit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

func TestHTTPContractPreservesHandlerSemanticsAndScope(t *testing.T) {
	registry := New(func(context.Context, *http.Request, Metadata) error {
		t.Fatal("HTTP handler must retain its own access check")
		return nil
	}, nil)
	metadata := Metadata{Audience: Clients, ID: "http_contract", Method: "POST", Path: "/v1/upload/{id}", Summary: "Upload", Access: Owner, Requires: ScopeLibrary, Lane: Default, Cost: Constant, BodyLimit: 64, Status: 202}
	if err := RegisterHTTP[struct {
		Name string `json:"name"`
	}, struct {
		Accepted bool `json:"accepted"`
	}](registry, metadata); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	registry.GateScopes(func(q *http.Request, scope Scope) error {
		if scope != ScopeLibrary {
			t.Fatal("scope changed")
		}
		if q.Header.Get("Authorization") == "refused" {
			return &apierror.Error{Code: "not_permitted"}
		}
		return nil
	})
	called := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		called++
		if q.PathValue("id") != "file.png" {
			t.Error("mux parameter changed")
		}
		if q.URL.RawQuery != "custom=kept" {
			t.Error("handler query changed")
		}
		w.Header().Set("ETag", "original")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	})
	mux := http.NewServeMux()
	if err := registry.MountHTTP(mux, "POST /v1/upload/{id}", handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.MountHTTP(mux, "POST /v1/upload/{id}", handler); err == nil {
		t.Fatal("duplicate binding accepted")
	}
	if err := registry.MountHTTP(mux, "POST /v1/unknown", handler); err == nil {
		t.Fatal("undeclared handler accepted")
	}
	q := httptest.NewRequest("POST", "/v1/upload/file.png?custom=kept", strings.NewReader(`{"name":"file"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, q)
	if w.Code != 202 || w.Header().Get("ETag") != "original" || w.Body.String() != `{"accepted":true}` {
		t.Fatalf("response changed: %d %v %s", w.Code, w.Header(), w.Body)
	}
	q.Header.Set("Authorization", "refused")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, q)
	if w.Code != 403 || called != 1 {
		t.Fatalf("scope bypassed: %d, calls %d", w.Code, called)
	}
}
