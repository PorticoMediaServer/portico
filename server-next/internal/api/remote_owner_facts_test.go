package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"encoding/json"
	"github.com/PorticoMediaServer/portico/server-next/internal/certclient"
	"net"
	"strings"
	"testing"
)

func TestRemoteOwnerReadsTheActualListenerWithoutPublicOrPrivateKeyFacts(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	socket := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 47981}
	s.ListeningOn(socket)
	socket.Port = 0 // The discovery/mapping path suppresses a loopback port; the socket is still real.
	out := get[RemoteAccessStatus](t, s, owner, "/v1/admin/remote-access")
	if out.Listener == nil || out.Listener.Address != "127.0.0.1" || out.Listener.Port != 47981 {
		t.Fatal(out.Listener)
	}
	if out.ManagedCertificate == nil || out.ManagedCertificate.State != "off" || out.ManagedCertificate.Ready {
		t.Fatal(out.ManagedCertificate)
	}
	raw, _ := json.Marshal(out.ManagedCertificate)
	if strings.Contains(string(raw), s.dataDir) || strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("certificate facts exposed private material")
	}
	public := call(t, s, "GET", "/v1/system", "", nil, nil)
	if strings.Contains(public.Body.String(), "managedCertificate") || strings.Contains(public.Body.String(), "listener") {
		t.Fatal("owner facts became public")
	}
	if w := call(t, s, "GET", "/v1/admin/remote-access", "", nil, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestRemoteOwnerReadsTheLiveCertificateRetryTime(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"busy","message":"Try again later."}}`))
	}))
	defer fake.Close()
	now := time.Now().UTC()
	s.portico = certclient.New(certclient.Options{Services: fake.URL, HTTP: fake.Client(), ServerID: s.id,
		Key: s.identity.key, Keys: t.TempDir(), Now: func() time.Time { return now }})
	s.portico.Linked(now)
	next := s.portico.Step(context.Background())
	out := get[RemoteAccessStatus](t, s, owner, "/v1/admin/remote-access").ManagedCertificate
	if out.NextTryAt == nil || !out.NextTryAt.Equal(next) || next.Sub(now) != 2*time.Minute || out.Problem != "Portico Services is busy." {
		t.Fatal("owner lost the retry schedule or read a remembered time in its problem", out)
	}
}
