package networking

import "testing"

func TestLocalityPublicObservationsWhileRemotePublicationOff(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, _, ctx := contactFixture(t)
	topo.Public = []string{"188.68.34.120", "2605:a144:2327:3137::1"}
	if _, err := f.db.Exec(`UPDATE networking_certificate_settings SET public_address='8.8.8.8'`); err != nil {
		t.Fatal(err)
	}
	if err := m.step(ctx); err != nil {
		t.Fatal(err)
	}
	h := &ClaimHandler{remote: m}
	observed := h.PublicAddresses()
	if len(observed) != 2 || observed[0].String() != "188.68.34.120" || observed[1].String() != "2605:a144:2327:3137::1" {
		t.Fatal("fresh own addresses", observed)
	}
	topo.Public = []string{}
	if err := m.step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.PublicAddresses()) != 0 {
		t.Fatal("stale configured/previous public address retained", h.PublicAddresses())
	}
}
