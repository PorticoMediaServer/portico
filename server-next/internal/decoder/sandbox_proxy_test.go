package decoder

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The sandbox proxy forwards GET and HEAD, and PUT only to the job's own
// output path (a playlist or segment name, no query); everything else is 405
// before it reaches the gateway.
func TestSandboxProxyAllowsOutputPutOnly(t *testing.T) {
	var seen []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(204)
	}))
	defer gateway.Close()
	target, _ := url.Parse(gateway.URL)
	output := "/output/" + strings.Repeat("a", 64) + "/"
	handler := proxyHandler(target, http.DefaultTransport, output)
	try := func(method, path string) int {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader("bytes")))
		return w.Code
	}
	for _, ok := range []struct{ method, path string }{
		{"GET", "/input/x.ts"}, {"HEAD", "/input/x.ts"},
		{"PUT", output + "index.m3u8"}, {"PUT", output + "segment-000000001.ts"}, {"PUT", output + "index.m3u8.tmp"},
	} {
		if code := try(ok.method, ok.path); code != 204 {
			t.Errorf("%s %s: %d", ok.method, ok.path, code)
		}
	}
	before := len(seen)
	for _, bad := range []struct{ method, path string }{
		{"PUT", "/input/x.ts"}, {"PUT", "/output/" + strings.Repeat("b", 64) + "/index.m3u8"},
		{"PUT", output + "evil.sh"}, {"PUT", output + "sub/index.m3u8"}, {"PUT", output + "index.m3u8?x=1"},
		{"PUT", output + "../../etc/passwd"}, {"POST", output + "index.m3u8"}, {"DELETE", output + "index.m3u8"},
	} {
		if code := try(bad.method, bad.path); code != 405 {
			t.Errorf("%s %s: %d", bad.method, bad.path, code)
		}
	}
	if len(seen) != before {
		t.Fatalf("refused requests reached the gateway: %v", seen[before:])
	}
	// A decoder that isn't a live HLS job may never PUT.
	w := httptest.NewRecorder()
	proxyHandler(target, http.DefaultTransport, "").ServeHTTP(w, httptest.NewRequest("PUT", output+"index.m3u8", strings.NewReader("x")))
	if w.Code != 405 {
		t.Fatalf("PUT without an output path: %d", w.Code)
	}
}
