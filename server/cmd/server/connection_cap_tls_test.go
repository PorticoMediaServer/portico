package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestMainBoundedDirectListenerPreservesHTTP2TLSAndRefresh(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	issued, err := ident.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	var installation string
	if err = db.QueryRow(`SELECT installation_id FROM identity_devices WHERE id=?`, issued.DeviceID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := fixture.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	fixture.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := boundedDirectListener(raw, 4, func(inner net.Listener) (*networking.DirectListener, error) {
		return networking.NewDirectListenerWithTLSConfig(inner, &tls.Config{Certificates: []tls.Certificate{certificate}})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	routes := httpapi.New(httpapi.Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__tls_probe" {
			if r.TLS == nil {
				http.Error(w, "no TLS state", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		routes.ServeHTTP(w, r)
	})}
	defer server.Close()
	go func() { _ = server.Serve(direct) }()
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	base := "https://" + raw.Addr().String()
	probe, err := client.Get(base + "/__tls_probe")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, probe.Body)
	probe.Body.Close()
	if probe.StatusCode != http.StatusNoContent || probe.ProtoMajor != 2 || probe.TLS == nil || probe.TLS.NegotiatedProtocol != "h2" {
		t.Fatalf("bounded TLS lost h2 or request TLS state: %d %s %+v", probe.StatusCode, probe.Proto, probe.TLS)
	}
	payload, _ := json.Marshal(map[string]string{"refreshToken": issued.RefreshToken, "installationId": installation, "requestId": identity.Token()})
	refresh, err := client.Post(base+"/v1/auth/refresh", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer refresh.Body.Close()
	var rotated identity.Envelope
	if err = json.NewDecoder(refresh.Body).Decode(&rotated); err != nil {
		t.Fatal(err)
	}
	if refresh.StatusCode != http.StatusOK || refresh.ProtoMajor != 2 || refresh.TLS == nil || rotated.RefreshToken == "" || rotated.RefreshToken == issued.RefreshToken || rotated.DeviceID != issued.DeviceID {
		t.Fatalf("bounded TLS refresh failed: status=%d proto=%s device-match=%v", refresh.StatusCode, refresh.Proto, rotated.DeviceID == issued.DeviceID)
	}
}
