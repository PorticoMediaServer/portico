package decoder

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
)

// outputName is one file a live HLS job writes: its playlist or a segment.
var outputName = regexp.MustCompile(`^(index\.m3u8|segment-[0-9]{9}\.ts)(\.tmp)?$`)

// proxyHandler is the sandbox's only way out: to the already-authorized
// numeric loopback gateway. Everything is GET or HEAD, except that a live HLS
// job (output != "") may PUT its playlist and segments to its own reserved
// output path, a single file name under it, with no query. The gateway still
// checks the grant and the path on every request.
func proxyHandler(target *url.URL, transport http.RoundTripper, output string) http.Handler {
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = target.Host
		r.Out.Header.Del("Forwarded")
		r.Out.Header.Del("X-Forwarded-For")
	}, Transport: transport, FlushInterval: -1, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "input unavailable", http.StatusBadGateway)
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" || r.Method == "HEAD":
		case r.Method == "PUT" && outputAllowed(output, r.URL):
		default:
			http.Error(w, "method unavailable", 405)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func outputAllowed(output string, u *url.URL) bool {
	if output == "" || !outputPath.MatchString(output) || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" {
		return false
	}
	name, ok := strings.CutPrefix(u.Path, output)
	return ok && outputName.MatchString(name)
}
