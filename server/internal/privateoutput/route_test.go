package privateoutput

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateOutputExactCapability(t *testing.T) {
	calls := 0
	route, e := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "index.m3u8" {
			t.Errorf("unexpected child: %s", r.URL.Path)
		}
		w.WriteHeader(201)
	}))
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		method, path string
		status       int
	}{
		{"PUT", route.Prefix + "index.m3u8", 201},
		{"GET", route.Prefix + "index.m3u8", 404},
		{"PUT", "/output/" + strings.Repeat("0", 64) + "/index.m3u8", 404},
		{"PUT", route.Prefix + "../index.m3u8", 404},
		{"PUT", route.Prefix + "index.m3u8?secret=1", 404},
		{"PUT", route.Prefix + "%69ndex.m3u8", 404},
		{"PUT", route.Prefix, 404},
	} {
		r := httptest.NewRequest(v.method, v.path, nil)
		w := httptest.NewRecorder()
		route.ServeHTTP(w, r)
		if w.Code != v.status {
			t.Errorf("%s %s got %d", v.method, v.path, w.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("unauthorized handler calls: %d", calls)
	}
}
