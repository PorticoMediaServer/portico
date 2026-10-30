//go:build devtrust && !release

package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestDevE2ELoopbackUsesOnlySocketPeer(t *testing.T) {
	for _, row := range []struct {
		peer    string
		allowed bool
	}{
		{"127.0.0.1:1234", true}, {"[::1]:1234", true}, {"[::ffff:127.0.0.1]:1234", true},
		{"192.0.2.1:1234", false}, {"[2001:db8::1]:1234", false}, {"localhost:1234", false}, {"", false}, {"127.0.0.1", false},
	} {
		t.Run(row.peer, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/dev/e2e", nil)
			r.RemoteAddr = row.peer
			r.Header.Set("Forwarded", "for=127.0.0.1")
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			if got := devE2ELoopbackPeer(r); got != row.allowed {
				t.Fatalf("peer %q: got %v", row.peer, got)
			}
		})
	}
}

func TestDevE2ERoutesHaveTypedContracts(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"GET /v1/dev/e2e", "POST /v1/dev/e2e/sign-in"} {
		metadata, ok := registry.MetadataForPattern(pattern)
		if !ok || metadata.Admission != laneAuth || metadata.Requires != "session" {
			t.Fatalf("missing development contract: %s", pattern)
		}
	}
}
