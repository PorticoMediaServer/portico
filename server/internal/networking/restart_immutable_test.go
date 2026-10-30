package networking

import (
	"context"
	"testing"
)

func TestRestartListenerGapPreservesBothFamiliesAndLANWithoutRepublish(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, _, ctx := contactFixture(t)
	cert := m.h.certificates
	installSyntheticCertificate(t, cert, ctx)
	cert.SetListening(32500, true)
	m.h.endpoints.pending = map[string]endpointPending{}
	counts := routeHostedFake(t, cert, ctx)
	_, _, _, _, key, err := m.snapshot(ctx)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE networking_remote_settings SET enabled=1,mapping=0`); err != nil {
		t.Fatal(err)
	}
	topo.Public = []string{"188.68.34.120", "2605:a144:2327:3137::1"}
	if err = m.step(ctx); err != nil {
		t.Fatal(err)
	}
	before := remoteStatusOf(t, f)
	if counts.publishes != 1 || counts.names != 2 || len(before.Candidates) != 3 {
		t.Fatalf("initial %+v pubs=%d proofs=%d", before, counts.publishes, counts.names)
	}
	cert.SetListening(32500, false)
	restarted := &RemoteManager{h: m.h, bind: m.bind, wan: m.wan, observe: func(context.Context, string, string, string) (Topology, error) { return *topo, nil }, wake: make(chan struct{}, 1)}
	if err = restarted.step(ctx); err != nil {
		t.Fatal(err)
	}
	during := remoteStatusOf(t, f)
	if counts.publishes != 1 || counts.names != 2 || during.Generation != before.Generation || len(during.Candidates) != 3 {
		t.Fatalf("listener gap withdrew %+v pubs=%d proofs=%d", during, counts.publishes, counts.names)
	}
	cert.SetListening(32500, true)
	if err = restarted.step(ctx); err != nil {
		t.Fatal(err)
	}
	if counts.publishes != 1 || counts.names != 2 {
		t.Fatal("immutable boot induced publication", counts)
	}
}
