package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strings"
	"testing"
)

func TestSecureConnectionsRoundTripAndRouterEnforcement(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	doc, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil || doc.Effective.SecureConnections != "preferred" {
		t.Fatalf("default %+v %v", doc.Effective, err)
	}
	values := doc.Effective
	values.SecureConnections = "required"
	values.TrustedProxies = []string{"203.0.113.9/32"}
	change := operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "secure-required", Values: values}
	raw, _ := json.Marshal(change)
	if w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(raw)); w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	stored, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil || stored.Effective.SecureConnections != "required" {
		t.Fatalf("stored %+v %v", stored.Effective, err)
	}
	for _, path := range []string{"/v1/networking/connection", "/v1/me", "/", "/missing", "/v1/networking/endpoint-proof"} {
		r := httptest.NewRequest("GET", "http://media.example"+path, nil)
		r.RemoteAddr = "198.51.100.1:42"
		r.Header.Set("Authorization", "Bearer "+f.owner)
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != http.StatusUpgradeRequired || !strings.Contains(w.Body.String(), `"code":"secure_connection_required"`) {
			t.Fatalf("HTTP %s %d %s", path, w.Code, w.Body)
		}
	}
	for _, trustedProxy := range []bool{false, true} {
		r := httptest.NewRequest("GET", "https://media.example/v1/networking/connection", nil)
		if trustedProxy {
			r.TLS = nil
			r.RemoteAddr = "203.0.113.9:42"
			r.Header.Set("X-Forwarded-Proto", "https")
		} else {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("TLS/proxy %v %d %s", trustedProxy, w.Code, w.Body)
		}
		var out ConnectionFacts
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.Secure || out.SecureConnections != "required" {
			t.Fatalf("connection %+v %v", out, err)
		}
		// The home networks and advertised addresses are owner facts; the same
		// connection still reports them behind the owner's authority (O-169).
		r = httptest.NewRequest("GET", "https://media.example/v1/admin/connectivity/status", nil)
		if trustedProxy {
			r.TLS = nil
			r.RemoteAddr = "203.0.113.9:42"
			r.Header.Set("X-Forwarded-Proto", "https")
		} else {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Authorization", "Bearer "+f.owner)
		w = httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		var owner struct {
			Connection ConnectionDocument `json:"connection"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &owner); err != nil || w.Code != 200 || owner.Connection.LANNetworks == nil {
			t.Fatalf("owner connection %d %s %v", w.Code, w.Body, err)
		}
	}
}
