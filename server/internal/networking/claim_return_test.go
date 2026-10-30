package networking

import (
	"net/http/httptest"
	"testing"
)

// A60: the approval page returns to the origin the browser used, including
// https behind a TLS-terminating proxy, and never to another host.
func TestReturnOriginFollowsTheBrowser(t *testing.T) {
	r := httptest.NewRequest("POST", "http://den.local:32500/v1/networking/claim/web-approval", nil)
	r.Host = "den.local:32500"
	r.Header.Set("Origin", "https://den.local:32500")
	if got := returnOrigin(r); got != "https://den.local:32500" {
		t.Fatal(got)
	}
	r.Header.Set("Origin", "https://evil.example")
	if got := returnOrigin(r); got != "http://den.local:32500" {
		t.Fatal(got)
	}
}
