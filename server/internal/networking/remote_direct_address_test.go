package networking

import (
	"net/netip"
	"testing"
)

func TestDirectCertificateIPv4ObservationIsBoundedAndUnambiguous(t *testing.T) {
	for _, test := range []struct {
		name           string
		topo           Topology
		retained, want string
	}{
		{"single", Topology{Public: []string{"188.68.34.120", "2a03:4000:6:8::1"}}, "", "188.68.34.120"},
		{"ambiguous", Topology{Public: []string{"188.68.34.120", "188.68.34.121"}}, "", ""},
		{"retain second", Topology{Public: []string{"188.68.34.120", "188.68.34.121"}}, "188.68.34.121", "188.68.34.121"},
		{"duplicate", Topology{Public: []string{"188.68.34.120", "188.68.34.120"}}, "", "188.68.34.120"},
		{"incomplete", Topology{Incomplete: true, Public: []string{"188.68.34.120"}}, "188.68.34.120", ""},
		{"not public", Topology{Public: []string{"127.0.0.1", "192.168.1.20", "100.120.50.24", "invalid", "::ffff:188.68.34.120"}}, "", ""},
		{"retained alone insufficient", Topology{}, "188.68.34.120", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := directCertificateIPv4(test.topo, test.retained)
			if test.want == "" {
				if got.IsValid() {
					t.Fatal("unobserved or ambiguous address selected")
				}
				return
			}
			if got != netip.MustParseAddr(test.want) {
				t.Fatal("current direct address lost")
			}
		})
	}
}

func TestRemoteDirectAddressInitializesOnlyCertificateAddressHint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote off", true: "remote on"}[enabled], func(t *testing.T) {
			f, m, topo, counts, ctx := contactFixture(t)
			m.h.runner = m.h.certificates.runner
			// The fixture installs and ACKs a real claim, but creates no certificate.
			_, _, _, _, key, err := m.snapshot(ctx)
			clear(key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`UPDATE networking_remote_settings SET mapping=0`); err != nil {
				t.Fatal(err)
			}
			useRemoteAccess(m, map[bool]string{false: "off", true: "on"}[enabled])
			topo.Public = []string{"188.68.34.120"}
			topo.LAN = nil
			topo.LocalAddress = "188.68.34.120"
			topo.Gateway = ""
			if err = m.step(ctx); err != nil {
				t.Fatal(err)
			}
			status, err := m.h.certificates.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if enabled {
				want = "188.68.34.120"
			}
			if status.Config.PublicAddress != want || status.TLSReady || status.ListenerBound || status.PubliclyTrusted {
				t.Fatal("address observation fabricated serving readiness")
			}
			origin, err := m.h.CurrentManagedHTTPS(ctx)
			if err != nil || origin != "" {
				t.Fatal("address observation granted managed return origin", err)
			}
			var material int
			if err = f.db.QueryRow(`SELECT count(*) FROM networking_certificates`).Scan(&material); err != nil || material != 0 {
				t.Fatal("address observation created material", err)
			}
			if counts.names != 0 {
				t.Fatal("unnamed non-serving address published as ready")
			}
		})
	}
}
