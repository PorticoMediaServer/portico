package api

import (
	"encoding/json"
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
