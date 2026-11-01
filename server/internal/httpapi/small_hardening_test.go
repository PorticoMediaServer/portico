package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// The document anyone can read before signing in says how to sign in and the
// API level, and carries no exact build: no version, build id, source digest
// or build time (O-169). The owner reads that from the authenticated report.
func TestThePublicSystemDocumentDoesNotCarryTheBuildFingerprint(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	w := f.raw("GET", "/v1/system", "", nil, nil)
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for _, key := range []string{"id", "name", "setupRequired", "hostedConfigured", "hostedAttached", "apiLevel", "signIn"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the public system document lost %q", key)
		}
	}
	for _, key := range []string{"version", "buildId", "sourceDigest", "builtAt"} {
		if _, ok := doc[key]; ok {
			t.Errorf("the public system document still carries %q", key)
		}
	}
}

// Scripts may come from this server and from the Cast sender's own files, not
// from anywhere on the host that serves them; forms post only here.
func TestTheWebAppPolicyNamesOnlyTheCastSendersScripts(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/system", nil))
	policy := w.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Skip("this response carries no policy")
	}
	var scripts string
	for _, directive := range strings.Split(policy, ";") {
		if d := strings.TrimSpace(directive); strings.HasPrefix(d, "script-src ") {
			scripts = d
		}
	}
	for _, source := range strings.Fields(strings.TrimPrefix(scripts, "script-src ")) {
		if source == "https://www.gstatic.com" || source == "https://www.gstatic.com/" {
			t.Fatalf("scripts are allowed from all of www.gstatic.com: %q", scripts)
		}
	}
	for _, needed := range []string{"'self'", "https://www.gstatic.com/cv/js/sender/", "https://www.gstatic.com/cast/sdk/libs/sender/"} {
		if !strings.Contains(scripts, needed) {
			t.Errorf("the policy no longer allows %s: %q", needed, scripts)
		}
	}
	if !strings.Contains(policy, "form-action 'self'") {
		t.Errorf("forms may post anywhere: %q", policy)
	}
}

func TestADownloadsNameIsQuotedSafely(t *testing.T) {
	for _, c := range []struct{ readable, plain, want string }{
		{"", "Film (2020).mkv", `attachment; filename="Film (2020).mkv"`},
		{"", `He said "no".mkv`, `attachment; filename="He said _no_.mkv"; filename*=UTF-8''He%20said%20%22no%22.mkv`},
		{"", "a\\b/c.mkv", `attachment; filename="a_b_c.mkv"; filename*=UTF-8''a%5Cb%2Fc.mkv`},
		{"", "Amélie.mkv", `attachment; filename="Am_lie.mkv"; filename*=UTF-8''Am%C3%A9lie.mkv`},
		{"", "line\r\nbreak.mkv", `attachment; filename="line__break.mkv"; filename*=UTF-8''line%0D%0Abreak.mkv`},
		{"", "", `attachment; filename="download"`},
		// A readable name beside a plain one: the readable one is only ever encoded.
		{"Amélie (2001).mkv", "dl_abc.mkv", `attachment; filename="dl_abc.mkv"; filename*=UTF-8''Am%C3%A9lie%20%282001%29.mkv`},
		{"He said \"no\"\r\nX-Injected: 1", "dl\"\r\n.mkv", `attachment; filename="dl___.mkv"; filename*=UTF-8''He%20said%20%22no%22%0D%0AX-Injected%3A%201`},
		{"dl_abc.mkv", "dl_abc.mkv", `attachment; filename="dl_abc.mkv"`},
	} {
		if got := contentDisposition(c.readable, c.plain); got != c.want {
			t.Errorf("%q, %q:\n got  %s\n want %s", c.readable, c.plain, got, c.want)
		}
	}
}

// The retired Hosted routes are gone rather than answering "moved".
func TestRetiredHostedRoutesAreGone(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	for _, path := range []string{"/v1/hosted/attach", "/v1/hosted/restrictions", "/v1/hosted/profiles/offline-select"} {
		if w := f.raw("POST", path, "", nil, map[string]any{}); w.Code != 404 {
			t.Errorf("POST %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
