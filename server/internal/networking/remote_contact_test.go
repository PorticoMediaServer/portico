package networking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A remote manager driven by a topology this test controls, against a Hosted stub that counts
// what it is actually asked to do.
type contactCounts struct{ publishes, names int }

func contactFixture(t *testing.T) (*sqlFixture, *RemoteManager, *Topology, *contactCounts, context.Context) {
	t.Helper()
	f, cert, ctx := certificateFixture(t)
	if e := seedRemoteSettings(ctx, f.db); e != nil {
		t.Fatal(e)
	}
	topo := &Topology{Bind: "0.0.0.0:32500", Gateway: "192.168.1.1", LocalAddress: "192.168.1.20", LAN: []string{"192.168.1.20"}, Public: []string{}}
	counts := &contactCounts{}
	m := &RemoteManager{
		h:       &ClaimHandler{store: f.store, certificates: cert, transport: cert.transport},
		bind:    "0.0.0.0:32500",
		wan:     &WANObserver{now: time.Now},
		observe: func(context.Context, string, string, string) (Topology, error) { return *topo, nil },
		wake:    make(chan struct{}, 1),
	}
	cert.transport.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/routes"):
			counts.publishes++
			var in RouteObservation
			raw, _ := io.ReadAll(r.Body)
			if json.Unmarshal(raw, &in) != nil {
				t.Error("unreadable route publication")
			}
			body, _ := json.Marshal(struct {
				Generation int64 `json:"generation,string"`
			}{in.Generation})
			return reply(http.StatusOK, string(body)), nil

		}
		t.Errorf("unexpected Hosted call %s", r.URL.Path)
		return reply(http.StatusNotFound, `{"code":"unexpected"}`), nil
	})
	return f, m, topo, counts, ctx
}

// Remote access is off in this fixture, so the published route set is the LAN candidate and
// nothing else. That is enough: the question is not what is published but whether an
// interface change that leaves the published set alone still costs a Hosted request.
//
// It used to. A changed topology digest cleared the retry deadline, so a renumbered gateway —
// or a TLS readiness flag flipping, or the bind address changing — forced a full route
// publication and a Hosted dial-back proof for facts Hosted already had.
func TestATopologyChangeThatChangesNoRouteDoesNotCallHosted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, counts, ctx := contactFixture(t)
	// Only addresses served over a trusted certificate are published, so the
	// fixture needs one for its LAN address to be part of the published set.
	counting := m.h.certificates.transport.client.Transport
	installSyntheticCertificate(t, m.h.certificates, ctx)
	m.h.certificates.SetListening(32500, true)
	m.h.certificates.transport.client.Transport = counting
	if _, _, _, _, _, e := m.snapshot(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`UPDATE networking_remote_settings SET lan_sharing=1`); e != nil {
		t.Fatal(e)
	}
	if e := m.step(ctx); e != nil {
		t.Fatal("first publication", e)
	}
	if counts.publishes != 1 {
		t.Fatal("the first pass did not publish once", counts.publishes)
	}
	// Nothing at all changed: the digest matches and the weekly deadline is far away.
	for range 5 {
		if e := m.step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if counts.publishes != 1 {
		t.Fatal("an unchanged pass republished", counts.publishes)
	}
	// The router is replaced and renumbers itself. The topology digest moves; the published
	// candidates do not.
	topo.Gateway = "192.168.1.254"
	for range 5 {
		if e := m.step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if counts.publishes != 1 {
		t.Fatal("a gateway change alone called Hosted", counts.publishes)
	}
	// A LAN address moving is a real change to what clients are told, so it publishes — once.
	topo.LAN = []string{"192.168.1.44"}
	for range 5 {
		if e := m.step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if counts.publishes != 2 {
		t.Fatal("a real route change did not publish exactly once", counts.publishes)
	}
}

// The gate that replaced the topology digest: a name is requested for an address that has
// none, and for nothing else. Without this, a new address would wait for the weekly attempt.
