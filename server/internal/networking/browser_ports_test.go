package networking

import (
	"context"
	"strings"
	"testing"
)

type browserPortMapper struct {
	requested []int
	granted   int
}

func (m *browserPortMapper) Prepare(_ context.Context, v Mapping) (Mapping, error) { return v, nil }
func (m *browserPortMapper) Map(_ context.Context, v Mapping, lifetime uint32) (Mapping, error) {
	m.requested = append(m.requested, v.ExternalPort)
	if m.granted != 0 {
		v.ExternalPort = m.granted
	}
	v.ExternalAddress = "188.68.34.120"
	return grantedMapping(v, lifetime, 1)
}
func TestAutomaticMappingsAvoidBrowserRestrictedPorts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, protocol := range []string{"pcp", "natpmp", "upnp"} {
		t.Run(protocol, func(t *testing.T) {
			_, m, topo, _, ctx := contactFixture(t)
			mapper := &browserPortMapper{}
			m.mapper = mapper
			c, _, authority, _, key, err := m.snapshot(ctx)
			clear(key)
			if err != nil {
				t.Fatal(err)
			}
			c.PublicPort = 6000
			c.PCP = protocol == "pcp"
			c.NATPMP = protocol == "natpmp"
			c.UPnP = protocol == "upnp"
			rows, _, err := m.reconcileMappings(ctx, c, authority, "topology", *topo, 32500, true)
			if err != nil || len(mapper.requested) != 1 || mapper.requested[0] != 32500 || len(rows) != 1 || rows[0].State != "mapped" {
				t.Fatalf("mapping %+v requests=%v %v", rows, mapper.requested, err)
			}
		})
	}
	_, m, topo, _, ctx := contactFixture(t)
	mapper := &browserPortMapper{granted: 6667}
	m.mapper = mapper
	c, _, authority, _, key, err := m.snapshot(ctx)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	c.PCP = true
	c.NATPMP = false
	c.UPnP = false
	rows, code, err := m.reconcileMappings(ctx, c, authority, "topology", *topo, 32500, true)
	if err != nil || code != "browser_restricted_port" || len(rows) != 1 || rows[0].State != "cleanup_pending" || rows[0].ExternalPort != 6667 {
		t.Fatalf("unsafe grant %+v %s %v", rows, code, err)
	}
}
func TestOwnerBrowserRestrictedPortsWarnWithoutRejecting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, _, ctx := accessURLsFixture(t, func(context.Context) []string {
		return []string{"http://owner.example.com:6667", "https://owner.example.com"}
	})
	if _, err := f.db.Exec(`UPDATE networking_remote_settings SET public_port=6000`); err != nil {
		t.Fatal(err)
	}
	useRemoteAccess(m, "off")
	captureAccessURLs(t, m, ctx)
	if err := m.step(ctx); err != nil {
		t.Fatal(err)
	}
	out := remoteStatusOf(t, f)
	if out.Config.PublicPort != 6000 || len(out.Warnings) != 2 || out.Warnings[0].Port != 6000 || out.Warnings[1].Port != 6667 || !strings.Contains(out.Warnings[0].Message, "The apps still can.") {
		t.Fatalf("warnings %+v", out)
	}
	if out.Warnings[1].BaseURL != "http://owner.example.com:6667" || len(out.Candidates) != 2 {
		t.Fatalf("owner address lost %+v", out)
	}
	if got := remoteWarnings(32500, nil); got == nil || len(got) != 0 {
		t.Fatal("empty warnings", got)
	}
}
