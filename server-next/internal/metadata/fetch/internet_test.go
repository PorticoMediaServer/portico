package fetch

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A host the transport checks is connected to only at an address on the Internet: one that
// resolves to this machine is refused, and one it is told not to check (a test's stand-in) is
// dialled as it is.
func TestInternetTransportRefusesALocalAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	viaName := "http://localhost:" + u.Port() + "/"
	checked := &http.Client{Transport: InternetTransport(func(string) bool { return true })}
	if _, err := checked.Get(viaName); err == nil || !strings.Contains(err.Error(), "not on the Internet") {
		t.Fatalf("a checked host on this machine: %v; want it refused", err)
	}
	trusted := &http.Client{Transport: InternetTransport(func(host string) bool { return host != "localhost" })}
	res, err := trusted.Get(viaName)
	if err != nil {
		t.Fatalf("the stand-in, not checked: %v", err)
	}
	res.Body.Close()
}
