package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strings"
	"testing"
)

func TestDeviceRegistrationUsesVerifiedClientAddress(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	d.Console = operations.New(d.DB)
	ctx := context.Background()
	doc, err := d.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.TrustedProxies = []string{"127.0.0.1/32"}
	p, err := d.Identity.Authenticate(owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Console.ApplySettings(ctx, p, operations.AllowServerScope, operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "device-proxy-settings", Values: values}); err != nil {
		t.Fatal(err)
	}
	h := New(d)
	body := `{"installationId":"` + strings.Repeat("B", 32) + `","name":"TV","platform":"tvOS","app":"Portico","appVersion":"1"}`
	for _, tc := range []struct{ peer, xff, want string }{
		{"127.0.0.1:123", "203.0.113.9", "203.0.113.9"},
		{"127.0.0.1:124", "203.0.113.10", "203.0.113.10"},
		{"203.0.113.20:125", "127.0.0.1", "203.0.113.20"},
	} {
		r := httptest.NewRequest("POST", "http://backend/v1/devices", strings.NewReader(body))
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("register %d %s", w.Code, w.Body)
		}
		var device identity.Device
		if err := json.Unmarshal(w.Body.Bytes(), &device); err != nil {
			t.Fatal(err)
		}
		var persisted string
		if err := d.DB.QueryRow("SELECT ip FROM identity_devices WHERE id=?", device.ID).Scan(&persisted); err != nil {
			t.Fatal(err)
		}
		if device.IP != tc.want || persisted != tc.want {
			t.Fatalf("peer=%s: device ip=%s stored=%s want=%s", tc.peer, device.IP, persisted, tc.want)
		}
	}
}
