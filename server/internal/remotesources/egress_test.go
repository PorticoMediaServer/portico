package remotesources

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type egressResolver func(context.Context, string, string) ([]netip.Addr, error)

func (f egressResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

func TestRcloneSFTPRoutePreservesHostKeyIdentityThroughWrappers(t *testing.T) {
	for _, remote := range []string{"native:/media", "encrypted:/"} {
		raw := []byte("[encrypted]\ntype=crypt\nremote=native:/media\npassword=fixture\n[native]\ntype=sftp\nhost=nas.example\nport=2222\nknown_hosts_file=/retained/original-hosts\n")
		before := append([]byte(nil), raw...)
		service := &Service{}
		policy := remotemedia.Policy{Resolver: egressResolver(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("192.168.42.7")}, nil
		}), Approvals: []remotemedia.Approval{{Root: "sftp://nas.example:2222/", Addresses: []string{"192.168.42.7"}}}}
		egress, err := service.startRcloneEgress(context.Background(), raw, remote, policy, func(ctx context.Context) error { return ctx.Err() })
		if err != nil {
			t.Fatal(err)
		}
		if egress.HTTPProxy == "" || egress.SFTPProxy == "" || egress.HTTPProxy == egress.SFTPProxy || !bytes.Equal(before, raw) || !bytes.Equal(before, egress.TransientConfiguration(raw)) || egress.CheckSMBPins != nil {
			egress.Close()
			t.Fatal("SFTP route replaced host-key configuration or reused HTTP authority")
		}
		backend, host, port, err := serveTransport(raw, remote)
		egress.Close()
		if err != nil || backend != "sftp" || host != "nas.example" || port != "2222" {
			t.Fatal("original native identity changed", backend, host, port, err)
		}
		policy.Approvals = []remotemedia.Approval{{Root: "https://nas.example:2222/", Addresses: []string{"192.168.42.7"}}}
		if egress, err = service.startRcloneEgress(context.Background(), raw, remote, policy, func(ctx context.Context) error { return ctx.Err() }); err == nil {
			egress.Close()
			t.Fatal("native source borrowed HTTPS approval")
		}
	}
}

func TestMountPolicyRouteFencesTheSavedGeneration(t *testing.T) {
	ctx := context.Background()
	f, managed, identities, owner, _, binary := nativeSaveFixture(t)
	managed.PrepareSourceEgress = f.s.prepareMountEgress
	receipt, err := managed.Command(ctx, "actual-owner-fixture", "", mounts.Command{OperationID: nativeSaveOperation(85), Action: "create", Name: "HTTP source", Executable: binary, Remote: "remote:/", Config: "[remote]\ntype=s3\nprovider=AWS\n"}, func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) })
	if err != nil {
		t.Fatal(err)
	}
	material, err := managed.CaptureServeSource(ctx, receipt.MountID, "1")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(material.Configuration)
	egress, err := f.s.prepareMountEgress(ctx, material)
	if err != nil {
		t.Fatal(err)
	}
	defer egress.Close()
	proxy, err := url.Parse(egress.HTTPProxy)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	// This metadata target is rejected without an upstream network request.
	response, err := client.Get("http://169.254.169.254/latest/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("legacy source route admitted metadata target", response.StatusCode)
	}
	if _, err = f.s.db.Exec(`UPDATE mount_backend_configs SET generation=generation+1 WHERE mount_id=?`, receipt.MountID); err != nil {
		t.Fatal(err)
	}
	response, err = client.Get("http://169.254.169.254/latest/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("retired mount generation retained proxy authority", response.StatusCode)
	}
	if next, err := f.s.prepareMountEgress(ctx, material); !errors.Is(err, storage.ErrRemoteChanged) {
		if next != nil {
			next.Close()
		}
		t.Fatal("old config reopened a proxy", err)
	}
}
