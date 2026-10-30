package networking

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestConfiguredNetworkingControllerReportsUnconfiguredReadsBeforeClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, h, _ := handlerFixture(t)
	installNetworkingMigration(t, f.db)
	transport, err := NewHTTPTransport("https://hosted.example", f.store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewCertificateManager(fixtureClaimContext(t), f.store, h.runner, transport, f.dir, CertificateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.certificates = manager
	status, err := manager.Status(fixtureClaimContext(t))
	if err != nil || status.State != "unconfigured" || status.Configured {
		t.Fatalf("certificate before claim: %+v %v", status, err)
	}
	if err := h.ConfigureRemote(context.Background(), "127.0.0.1:1234", nil); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/networking/remote", nil)
	r.Header.Set("Origin", "https://web.example")
	r.Header.Set("Authorization", "Bearer current")
	w := httptest.NewRecorder()
	h.remoteHTTP(w, r)
	var remote RemoteStatus
	if err := json.Unmarshal(w.Body.Bytes(), &remote); err != nil || w.Code != 200 || remote.State != "unconfigured" {
		t.Fatalf("remote before claim: %d %+v %v", w.Code, remote, err)
	}
}
