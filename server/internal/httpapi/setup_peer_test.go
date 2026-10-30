package httpapi

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// setupPeerFixture is the small real database a live router decides locality
// from: the owner's saved document, with this fixture's home network and the
// one trusted proxy, saved through the same operations calls an installation
// uses. The runner's attached interfaces decide nothing here.
func setupPeerFixture(t *testing.T) Dependencies {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	console := operations.New(db)
	document, err := console.RuntimeSettings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := document.Requested
	values.LANNetworks = []string{"127.0.0.0/8", "::1/128", fixtureHomeNetwork}
	values.TrustedProxies = []string{"127.0.0.1/32"}
	if _, err = console.ApplySettings(ctx, identity.Principal{}, operations.AllowServerScope, operations.SettingsChange{
		ExpectedRevision: document.Revision,
		IdempotencyKey:   "setup-peer-boundary",
		Values:           values,
	}); err != nil {
		t.Fatal(err)
	}
	return Dependencies{DB: db, Console: console, connectionPolicy: &connectionPolicyCache{}}
}

func TestSetupProxyBoundary(t *testing.T) {
	proxies, err := ParseTrustedProxyCIDRs("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	// Locality is read from the saved document; TrustedProxies stays the
	// startup fallback a real server carries for that same policy.
	d := setupPeerFixture(t)
	d.TrustedProxies = proxies
	for _, tc := range []struct {
		peer, forwarded string
		private         bool
	}{
		{"127.0.0.1:1234", "", false},
		{"127.0.0.1:1234", "198.51.100.2", false},
		{"127.0.0.1:1234", "192.168.1.2", true},
		{"127.0.0.1:1234", "127.0.0.1, 198.51.100.2", false},
		{"198.51.100.2:1234", "127.0.0.1", false},
	} {
		r := httptest.NewRequest("POST", "http://localhost/v1/setup/browser", nil)
		r.RemoteAddr = tc.peer
		if tc.forwarded != "" {
			r.Header.Set("X-Forwarded-For", tc.forwarded)
		}
		if got := d.privateSetupPeer(r); got != tc.private {
			t.Errorf("%+v private=%v", tc, got)
		}

	}
	r := httptest.NewRequest("POST", "http://localhost/v1/setup/browser", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	if !(Dependencies{}).privateSetupPeer(r) {
		t.Fatal("untrusted forwarded header changed peer locality")
	}
	r.Header.Del("X-Forwarded-For")
	r.Header.Set("Forwarded", "for=127.0.0.1")
	if !(Dependencies{}).privateSetupPeer(r) {
		t.Fatal("untrusted Forwarded header changed peer locality")
	}
}
