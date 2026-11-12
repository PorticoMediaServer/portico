package api

import (
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Which web pages, other than the ones this server serves itself, may read its answers.
//
// A browser lets any page send some requests anywhere, but lets a page read the answer from
// another address only when that address says so. This server says so to a short list of
// exact origins: Portico's own hosted web app, and the ones the owner adds in the network
// settings (a developer's local build of the web app, a web tool of their own).
//
// What an allowed origin gets: its pages may call the API from a person's browser and read
// what comes back. It gets nothing else. This server never signs anyone in by cookie, so a
// page has only the token a person got by signing in on that page; a sign-in made on any
// other page, this server's own included, is out of its reach.
//
// What every other page gets: nothing it did not have. It can still make the browser send a
// plain GET, or a POST with an empty body or one that is not JSON, and cannot read the
// answer. Every body that is not empty must be application/json, and a request with a JSON
// body or a token needs the browser to ask first (a preflight), to which the answer is no.
// So such a page can send nothing a route acts on; a test holds every route that needs no
// sign-in to that.
//
// Pages this server serves itself are the same origin as the API. A browser asks nothing
// for them, and nothing here applies to them.

// builtInWebOrigins are always allowed: Portico's hosted web app, which runs in a person's
// browser and talks straight to their own server.
var builtInWebOrigins = []string{"https://web.getportico.tv"}

const (
	maxWebOrigins = 16
	// What an allowed page may send, and what it may read besides the body. A page can
	// always send Accept and read Content-Type and Content-Length.
	corsMethods = "GET, HEAD, POST, PUT, PATCH, DELETE"
	corsHeaders = "Authorization, Content-Type, Idempotency-Key, If-Match, If-None-Match, Range, Last-Event-ID"
	corsExposed = "ETag, Retry-After, Location, Date, Content-Range, Accept-Ranges, Content-Disposition, X-Request-Id, X-Audio-Trim-Start, X-Audio-Trim-End, X-Audio-Frames"
	// How long a browser may remember that this origin may ask. The request itself is checked
	// against the list every time. Browsers keep it for less: two hours in Chromium.
	corsMaxAge = "86400"
)

// crossOrigin adds, to an answer under /v1/, what lets an allowed origin's page read it, and
// answers that page's preflight itself, before any sign-in check: a preflight never carries
// a token. It reports whether the request has been answered.
func (s *Server) crossOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/v1" && !strings.HasPrefix(r.URL.Path, "/v1/") {
		return false
	}
	// The answer depends on who asks, so nothing that keeps answers may hand one origin's to another.
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" || !s.originAllowed(origin) {
		return false
	}
	// The origin itself, never "*", and never Access-Control-Allow-Credentials: the browser
	// then sends no cookie and no saved password with these requests.
	w.Header().Set("Access-Control-Allow-Origin", origin)
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		w.Header().Set("Access-Control-Allow-Methods", corsMethods)
		w.Header().Set("Access-Control-Allow-Headers", corsHeaders)
		w.Header().Set("Access-Control-Max-Age", corsMaxAge)
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	w.Header().Set("Access-Control-Expose-Headers", corsExposed)
	return false
}

// originAllowed is an exact comparison with the list: no patterns, no "ends with".
func (s *Server) originAllowed(origin string) bool {
	if slices.Contains(builtInWebOrigins, origin) {
		return true
	}
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	return slices.Contains(s.network.stored.Origins, origin)
}

// webOrigin puts an origin the owner typed into the one form a browser sends: a lower-case
// scheme and host, and a port only when it is not the scheme's own. It reports false for
// anything that is not exactly one origin: "*", "null", an address with a path, a user name,
// a query, or a scheme other than http and https.
func webOrigin(typed string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(typed))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || strings.Contains(u.Host, "*") {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	// A browser sends a name with letters outside ASCII in its xn-- form, so the name as
	// typed would never match. The owner is asked for that form.
	for _, c := range host {
		if c > unicode.MaxASCII {
			return "", false
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		// A port a browser would write differently, or not at all, would never match either.
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", false
		}
		if !(u.Scheme == "https" && n == 443) && !(u.Scheme == "http" && n == 80) {
			host += ":" + port
		}
	}
	return u.Scheme + "://" + host, true
}

// plainOriginNotice warns of an allowed origin a browser reaches without encryption, unless
// it is on the person's own machine.
func plainOriginNotice(origin string) string {
	return "Pages at " + origin + " may use this server from a browser, and that address is not encrypted (http), so anyone on the network between " +
		"a person and it can put their own page there and read whatever that person can. Use an https address if there is one."
}

func plainOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host != "localhost" && host != "127.0.0.1" && host != "::1" && !strings.HasSuffix(host, ".localhost")
}

// unaskedPost refuses a POST to a route that needs no sign-in and takes a body unless it is
// sent as application/json. A page on any site can make a browser send a POST with no body, a
// form or plain text without asking the server first; it cannot send application/json without
// a preflight, and only an allowed origin's preflight is answered. Without this a route whose
// body is all optional would act on such a request: a page could start an owner reset. A
// signed-in route is safe already, since its token goes in a header a page cannot send
// unasked either. A route that takes no body at all is not refused here: each such route
// that needs no sign-in is listed, with why a bare request to it does nothing, in the test
// that holds this rule.
func unaskedPost(r *http.Request, route *Route) error {
	if route.Access != Public || r.Method != http.MethodPost || route.Request == reflect.TypeFor[None]() {
		return nil
	}
	if kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); kind != "application/json" {
		return refusal(http.StatusUnsupportedMediaType, "unsupported_media_type", "Send the request as application/json.", false)
	}
	return nil
}
