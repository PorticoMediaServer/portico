package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnyOriginMayCallWithAKeyButNotASession(t *testing.T) {
	d := Dependencies{Origins: []string{"https://web.getportico.tv"}}
	request := func(method, target, origin string, change func(*http.Request)) *http.Request {
		r := httptest.NewRequest(method, target, nil)
		r.Header.Set("Origin", origin)
		if change != nil {
			change(r)
		}
		return r
	}
	token := func(r *http.Request) { r.Header.Set("Authorization", "Bearer pk_id.secret") }
	session := func(r *http.Request) { r.Header.Set("Authorization", "Bearer sessiontoken") }
	mixed := func(r *http.Request) {
		r.Header.Add("Authorization", "Bearer pk_id.secret")
		r.Header.Add("Authorization", "Bearer sessiontoken")
	}
	cases := []struct {
		name    string
		r       *http.Request
		allowed bool
	}{
		{"own address", request("POST", "http://192.168.1.20:32500/v1/auth/login", "http://192.168.1.20:32500", nil), true},
		{"own .local name", request("POST", "http://portico.local:32500/v1/auth/login", "http://portico.local:32500", nil), true},
		{"own IPv6 address", request("POST", "http://[fd00::20]:32500/v1/auth/login", "http://[fd00::20]:32500", nil), true},
		{"built-in page", request("POST", "http://192.168.1.20:32500/v1/auth/login", "https://web.getportico.tv", nil), true},
		{"proxy that rewrites Host, browser says same origin", request("POST", "http://127.0.0.1:32500/v1/auth/login", "https://media.example", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }), true},
		{"other site without a token", request("POST", "http://192.168.1.20:32500/v1/auth/login", "https://tool.example", nil), false},
		{"other site, cross-site fetch metadata", request("GET", "http://192.168.1.20:32500/v1/system", "https://tool.example", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }), false},
		{"other site with a token", request("PATCH", "http://192.168.1.20:32500/v1/me/preferences", "https://tool.example", token), true},
		{"other site, opaque origin, with a token", request("GET", "http://192.168.1.20:32500/v1/items", "null", token), true},
		{"other site with a session token", request("GET", "http://192.168.1.20:32500/v1/items", "https://tool.example", session), false},
		{"other site, opaque origin, with a session token", request("GET", "http://192.168.1.20:32500/v1/items", "null", session), false},
		{"other site with a key and a session token", request("GET", "http://192.168.1.20:32500/v1/items", "https://tool.example", mixed), false},
		{"built-in page with a session token", request("GET", "https://c-x.ptc-y.direct.getportico.tv:32500/v1/items", "https://web.getportico.tv", session), true},
		{"own address with a session token", request("GET", "http://192.168.1.20:32500/v1/items", "http://192.168.1.20:32500", session), true},
		{"other site preflight", request("OPTIONS", "http://192.168.1.20:32500/v1/items", "https://tool.example", nil), true},
		{"other site media grant", request("GET", "http://192.168.1.20:32500/v1/media/grant/segment.m4s", "https://tool.example", nil), true},
		{"other site media grant, not a read", request("POST", "http://192.168.1.20:32500/v1/media/grant", "https://tool.example", nil), false},
	}
	for _, c := range cases {
		if got := d.browserOriginAllowed(c.r, c.r.Header.Get("Origin")); got != c.allowed {
			t.Errorf("%s: allowed %v, want %v", c.name, got, c.allowed)
		}
	}
}

func TestAnotherOriginNeverSendsACookie(t *testing.T) {
	d := Dependencies{}
	r := httptest.NewRequest("GET", "http://192.168.1.20:32500/v1/items", nil)
	r.Header.Set("Origin", "https://tool.example")
	r.Header.Set("Authorization", "Bearer pk_id.secret")
	r.Header.Set("Cookie", "portico_session=abc")
	if !d.browserOriginAllowed(r, r.Header.Get("Origin")) || r.Header.Get("Cookie") != "" {
		t.Fatal("a cookie travelled with another origin's request")
	}
	own := httptest.NewRequest("GET", "http://192.168.1.20:32500/v1/items", nil)
	own.Header.Set("Origin", "http://192.168.1.20:32500")
	own.Header.Set("Cookie", "portico_session=abc")
	if !d.browserOriginAllowed(own, own.Header.Get("Origin")) || own.Header.Get("Cookie") == "" {
		t.Fatal("the server's own page lost its cookie")
	}
}
