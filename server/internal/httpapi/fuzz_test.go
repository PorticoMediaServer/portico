package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// A table of hostile inputs covers what somebody thought of. Fuzzing covers
// what nobody did, which is the half that actually takes servers down.
//
// These targets are the decoders a request reaches before any authorization or
// database work: the browse expression, which is a recursive parser over
// client-supplied JSON and therefore the one place in this surface where a
// stack overflow is plausible; the request-body decoder every handler shares;
// and the two functions that turn raw header bytes into the identity and
// address the whole of admission is keyed on.
//
// Run them with, for example:
//
//	go test ./internal/httpapi -run xxx -fuzz FuzzBrowseExpression -fuzztime 60s
//
// The corpus is seeded with the shapes the table test uses, so a fuzz run
// starts from the interesting places rather than from "".

// The browse expression is the deepest parser on the surface: it is recursive,
// it is driven entirely by the client, and it runs before the query it produces
// is costed. A panic here is a request that kills the process.
func FuzzBrowseExpression(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"variant":"group","all":[]}`,
		`{"variant":"group","any":[{"field":"year","is":2020}]}`,
		`{"variant":"group","not":{"field":"title","contains":"x"}}`,
		`{"field":"year","between":[1900,2000]}`,
		`{"field":"genre","in":["a","b"]}`,
		strings.Repeat(`{"not":`, 500) + `{}` + strings.Repeat(`}`, 500),
		strings.Repeat(`{"variant":"group","all":[`, 200) + `{}` + strings.Repeat(`]}`, 200),
		`{"variant":"group","all":[{"variant":"group","any":[{"variant":"group","not":{"field":"","is":null}}]}]}`,
		`{"field":"year","is":1e400}`,
		"{\"field\":\"\\u0000\",\"contains\":\"\\uffff\"}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		// The contract is: any input, any answer, no panic and no unbounded
		// recursion. The parser owns its own depth limit; this is where that is
		// proven rather than asserted.
		node, err := catalog.ParseBrowseQuery(json.RawMessage(raw), "query")
		if err != nil && node != nil {
			t.Fatalf("a rejected expression still produced a node: %q", raw)
		}
	})
}

// Every handler that takes a body goes through this, so it is the single place
// a malformed body can do damage to all of them at once.
func FuzzRequestBodyDecoder(f *testing.F) {
	for _, seed := range []string{
		``, `{}`, `null`, `[]`, `{"name":"x"}`, `{"name":"x"}{"name":"y"}`,
		`{"name":` + strings.Repeat("[", 1000), `{"unknown":1}`,
		"{\"name\":\"\x00\"}", `{"name":1e400}`,
		strings.Repeat(`{"a":`, 3000) + `1` + strings.Repeat(`}`, 3000),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		var into struct {
			Name  string `json:"name"`
			Limit int    `json:"limit"`
			Tags  []string
		}
		r := httptest.NewRequest("POST", "/v1/libraries", strings.NewReader(body))
		w := httptest.NewRecorder()
		_ = decode(w, r, &into)
	})
}

// Admission keys every request on these two before any handler runs, so a
// pathological header must cost no more than a valid one and must never panic.
func FuzzFairnessKey(f *testing.F) {
	for _, seed := range []struct{ authorization, path, remote string }{
		{"Bearer abc", "/v1/home", "192.168.1.5:900"},
		{"", "/v1/media/grant/segment.ts", "[2001:db8::1]:900"},
		{"Basic", "/", "not-an-address"},
		{strings.Repeat("A", 5000), "/v1/media//", "1.2.3.4:0"},
		{"Bearer \x00", "/v1/media/", "::1"},
	} {
		f.Add(seed.authorization, seed.path, seed.remote)
	}
	f.Fuzz(func(t *testing.T, authorization, path, remote string) {
		// The request is built by hand rather than through httptest, which
		// validates its arguments and would reject exactly the inputs worth
		// trying. A real server hands these fields to the handler already parsed,
		// so this is the shape the function actually sees.
		r := &http.Request{
			Method:     "GET",
			URL:        &url.URL{Path: path},
			Header:     http.Header{},
			RemoteAddr: remote,
		}
		if authorization != "" {
			r.Header["Authorization"] = []string{authorization}
		}
		if key := fairnessKey(r, nil); key == "" {
			t.Fatal("a request produced no fairness key; every request must have one")
		}
	})
}

// The address a rate limit and a fairness key are counted against is derived
// from headers a client controls whenever a proxy is trusted.
func FuzzClientAddress(f *testing.F) {
	for _, seed := range []struct{ remote, forwarded string }{
		{"127.0.0.1:1000", "198.51.100.2"},
		{"127.0.0.1:1000", strings.Repeat("1.2.3.4,", 500)},
		{"garbage", ""},
		{"[::1]:1000", "2001:db8::2"},
		{"1.2.3.4:5", "%%%"},
	} {
		f.Add(seed.remote, seed.forwarded)
	}
	trusted, _ := ParseTrustedProxyCIDRs("127.0.0.1/32,::1/128")
	f.Fuzz(func(t *testing.T, remote, forwarded string) {
		r := &http.Request{
			Method:     "POST",
			URL:        &url.URL{Path: "/v1/direct/sign-in"},
			Header:     http.Header{},
			RemoteAddr: remote,
		}
		if forwarded != "" {
			r.Header["X-Forwarded-For"] = []string{forwarded}
		}
		address := clientAddress(r, trusted)
		if address == "" {
			t.Fatal("a request produced no client address")
		}
		// Whatever comes out must be usable as a map key and as a network,
		// because both tables are keyed on it.
		_ = clientNetwork(address)
	})
}

// The trusted-proxy list is operator configuration, but it is parsed at
// startup from an environment variable and a bad value must be a refusal, not a
// crash or an accidentally permissive list.
func FuzzTrustedProxyList(f *testing.F) {
	for _, seed := range []string{"", "127.0.0.1/32", "0.0.0.0/0", "::/0", ",,,", strings.Repeat("1.2.3.4/32,", 100), "::ffff:0:0/96"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		prefixes, err := ParseTrustedProxyCIDRs(value)
		if err != nil {
			if prefixes != nil {
				t.Fatalf("a rejected list still returned %d prefixes", len(prefixes))
			}
			return
		}
		for _, prefix := range prefixes {
			// A zero-bit prefix trusts the whole internet, which would make any
			// X-Forwarded-For authoritative. It must never survive parsing.
			if prefix.Bits() == 0 {
				t.Fatalf("%q produced a prefix that trusts everything", value)
			}
		}
	})
}
