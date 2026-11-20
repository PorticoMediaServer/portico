package decoder

import (
	"context"
	"io"
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

func TestSandboxProxyFinishesOutputAfterDecoderDisconnects(t *testing.T) {
	const playlist = "#EXTM3U\n"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != playlist {
			http.Error(w, "incomplete playlist", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer gateway.Close()
	target, err := url.Parse(gateway.URL)
	if err != nil {
		t.Fatal(err)
	}
	output := "/output/" + strings.Repeat("a", 64) + "/"
	ctx, disconnect := context.WithCancel(t.Context())
	r := httptest.NewRequestWithContext(ctx, http.MethodPut, output+"index.m3u8", strings.NewReader(playlist))
	// FFmpeg closes the connection after sending the upload, without waiting for a reply.
	disconnect()
	w := httptest.NewRecorder()
	proxyHandler(target, http.DefaultTransport, output).ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("complete playlist after decoder disconnect answered %d: %s", w.Code, w.Body)
	}
}
