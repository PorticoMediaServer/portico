package remotesources

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSMBPinnedCandidateSavedAndSharedServeLaunches(t *testing.T) {
	for _, wrapper := range []bool{false, true} {
		name := "native"
		if wrapper {
			name = "alias"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f, managed, identities, owner, _, binary := nativeSaveFixture(t)
			f.s.resolver = egressResolver(func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("192.168.42.7")}, nil
			})
			authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }
			raw := "[native]\ntype=smb\nhost=nas.example\nfixture_expected_smb_host=192.168.42.7\n"
			remote := "native:/"
			if wrapper {
				raw += "[wrapper]\ntype=alias\nremote=native:/media\n"
				remote = "wrapper:/"
			}
			// The synthetic child refuses readiness unless it reads the numeric
			// host from its config. Exercise the actual owner-save candidate path.
			receipt, err := managed.Command(ctx, "actual-owner-fixture", "", mounts.Command{OperationID: nativeSaveOperation(86), Action: "create", Name: "SMB", Executable: binary, Remote: remote, Config: raw}, authorize)
			if err != nil {
				t.Fatal("candidate launch", err)
			}
			if err = f.s.RefreshMounts(ctx); err != nil {
				t.Fatal(err)
			}
			life, cancel := context.WithCancel(ctx)
			defer cancel()
			if err = f.s.startReadRuntime(life); err != nil {
				t.Fatal(err)
			}
			defer f.s.stopServeProcesses()
			if _, err = f.s.InspectRoot(life, receipt.Mount.MountPath); err != nil {
				t.Fatal("shared serve launch", err)
			}
			candidate, err := f.s.prepareSourceCandidate(ctx, mounts.SourceCandidateInput{Configuration: []byte(raw), Remote: remote, Current: receipt.Mount}, authorize)
			if err != nil {
				t.Fatal(err)
			}
			if err = candidate.Test(ctx, rcloneprogram.Program{Path: binary}, receipt.Mount.ExecutableDigest); err != nil {
				t.Fatal("saved source test launch", err)
			}
			material, err := managed.CaptureServeSource(ctx, receipt.MountID, "1")
			if err != nil {
				t.Fatal(err)
			}
			defer clear(material.Configuration)
			if string(material.Configuration) != raw {
				t.Fatal("launch pin changed retained digest material")
			}
		})
	}
}

func TestRcloneSMBPinsEveryLeafInMixedGraph(t *testing.T) {
	ctx := context.Background()
	raw := []byte("[top]\ntype=combine\nupstreams=one=first:media two=second:media three=ssh:media\n[first]\ntype=smb\nhost=one.example\n[second]\ntype=smb\nhost=two.example\n[ssh]\ntype=sftp\nhost=ssh.example\nknown_hosts_file=/retained/hosts\n")
	policy := remotemedia.Policy{Resolver: egressResolver(func(_ context.Context, _, host string) ([]netip.Addr, error) {
		address := "192.168.42.7"
		if host == "two.example" {
			address = "192.168.42.8"
		}
		return []netip.Addr{netip.MustParseAddr(address)}, nil
	}), Approvals: []remotemedia.Approval{
		{Root: "smb://one.example:445/", Addresses: []string{"192.168.42.7"}},
		{Root: "smb://two.example:445/", Addresses: []string{"192.168.42.8"}},
		{Root: "sftp://ssh.example:22/", Addresses: []string{"192.168.42.7"}},
	}}
	egress, err := (&Service{}).startRcloneEgress(ctx, raw, "top:", policy, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer egress.Close()
	sections, err := parseServeConfiguration(egress.TransientConfiguration(raw))
	if err != nil || sections["first"]["host"] != "192.168.42.7" || sections["second"]["host"] != "192.168.42.8" || sections["ssh"]["host"] != "ssh.example" || sections["ssh"]["known_hosts_file"] != "/retained/hosts" || egress.SFTPProxy == "" {
		t.Fatal("mixed graph did not pin every SMB leaf and preserve SSH", sections, err)
	}
}

func TestRcloneSMBPinsCheckedLaunchAcrossWrappers(t *testing.T) {
	for _, graph := range []string{
		"[top]\ntype=alias\nremote=NAS.leaf-ä:media\n",
		"[top]\ntype=crypt\nremote=NAS.leaf-ä:media\npassword=fixture\n",
		"[top]\ntype=union\nupstreams=NAS.leaf-ä:media NAS.leaf-ä:other\n",
		"[top]\ntype=combine\nupstreams=one=NAS.leaf-ä:media two=NAS.leaf-ä:other\n",
		"[top]\ntype=smb\nhost=nas.example\nport=2445\n",
	} {
		t.Run(strings.Split(graph, "\n")[1], func(t *testing.T) {
			ctx := context.Background()
			raw := []byte(graph + "[NAS.leaf-ä]\ntype=smb\nhost=nas.example\nport=2445\nuser=owner\npass=opaque\n")
			original := bytes.Clone(raw)
			addresses := []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("192.168.42.8"), netip.MustParseAddr("192.168.42.7")}
			calls := 0
			policy := remotemedia.Policy{Resolver: egressResolver(func(context.Context, string, string) ([]netip.Addr, error) {
				calls++
				return addresses, nil
			}), Approvals: []remotemedia.Approval{{Root: "smb://nas.example:2445/", Addresses: []string{"192.168.42.7", "192.168.42.8"}}}}
			egress, err := (&Service{}).startRcloneEgress(ctx, raw, "top:", policy, func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer egress.Close()
			if !bytes.Equal(raw, original) || egress.SFTPProxy != "" || egress.CheckSMBPins == nil {
				t.Fatal("pinning changed retained material or SFTP route")
			}
			// DNS changes after the checked launch material was captured. Building
			// the child must not resolve it again or leave a hostname in the leaf.
			addresses = []netip.Addr{netip.MustParseAddr("192.168.42.8")}
			checkedCalls := calls
			t.Setenv("RCLONE_SMB_HOST", "unchecked.example")
			spec := rcloneprogram.ServeCommandSpec{SourceID: "fixture", SourceGeneration: "1", PrivateDirectory: t.TempDir(), Configuration: egress.TransientConfiguration(raw), Remote: "top:", Port: 24456, Username: "fixture", Password: "fixture", Connections: 1, RequestsPerSecond: 1, Enforce: func(*exec.Cmd) error { return nil }}
			prepared, err := rcloneprogram.PrepareServe(ctx, rcloneprogram.Program{Path: "/unused/rclone", Included: true}, spec)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Loaded()
			var childRaw []byte
			for i, arg := range prepared.Command.Args {
				if arg == "--config" {
					childRaw, err = os.ReadFile(prepared.Command.Args[i+1])
				}
			}
			if err != nil || len(childRaw) == 0 || calls != checkedCalls {
				t.Fatal("child lost the checked config or resolved again", err, calls)
			}
			sections, err := parseServeConfiguration(childRaw)
			if err != nil {
				t.Fatal(err)
			}
			leaf := "NAS.leaf-ä"
			if sections["top"]["type"] == "smb" {
				leaf = "top"
			}
			if sections[leaf]["host"] != "192.168.42.7" || sections[leaf]["port"] != "2445" {
				t.Fatal("child did not receive deterministic checked private pin", sections[leaf])
			}
			for _, env := range prepared.Command.Env {
				if strings.HasPrefix(env, "RCLONE_SMB_HOST=") {
					t.Fatal("ambient environment overrode pin")
				}
			}
			if err = egress.CheckSMBPins(ctx); !errors.Is(err, storage.ErrRemoteChanged) {
				t.Fatal("reconnect retained an address absent from the checked set", err)
			}
		})
	}
}

func TestRcloneSMBPinRejectsUnapprovedDNSAnswers(t *testing.T) {
	for _, answers := range [][]string{
		{"8.8.8.8", "192.168.42.7", "192.168.42.8"},
		{"192.168.42.8"},
		{"8.8.8.8", "127.0.0.1"},
	} {
		t.Run(strings.Join(answers, ","), func(t *testing.T) {
			policy := remotemedia.Policy{Resolver: egressResolver(func(context.Context, string, string) ([]netip.Addr, error) {
				var out []netip.Addr
				for _, answer := range answers {
					out = append(out, netip.MustParseAddr(answer))
				}
				return out, nil
			}), Approvals: []remotemedia.Approval{{Root: "smb://nas.example:445/", Addresses: []string{"192.168.42.7"}}}}
			egress, err := (&Service{}).startRcloneEgress(context.Background(), []byte("[nas]\ntype=smb\nhost=nas.example\n"), "nas:", policy, func(ctx context.Context) error { return ctx.Err() })
			if egress != nil {
				egress.Close()
				t.Fatal("unapproved answer created launch custody")
			}
			if !errors.Is(err, remotemedia.ErrPolicy) {
				t.Fatal("unapproved answer not refused", err)
			}
		})
	}
}

func TestRcloneSMBPinPreservesAuthenticationIdentity(t *testing.T) {
	for _, tc := range []struct{ options, spn string }{
		{"", ""},
		{"spn=cifs/owner-cluster:1020\n", "cifs/owner-cluster:1020"},
		{"use_kerberos=true\n", "cifs/NAS.Example."},
		{"use_kerberos=true\nspn=\n", "cifs/NAS.Example."},
		{"use_kerberos=true\nspn=cifs/owner-cluster\n", "cifs/owner-cluster"},
	} {
		t.Run(tc.options, func(t *testing.T) {
			raw := []byte("[NAS.leaf-ä]\ntype=smb\nhost=NAS.Example.\nport=1020\nuser=owner\npass=opaque\ndomain=DOMAIN\n" + tc.options + "[ssh]\ntype=sftp\nhost=ssh.example\n")
			original := bytes.Clone(raw)
			pinned, err := pinSMBConfiguration(raw, map[string]netip.Addr{"NAS.leaf-ä": netip.MustParseAddr("fd12::7")})
			if err != nil {
				t.Fatal(err)
			}
			sections, err := parseServeConfiguration(pinned)
			if err != nil {
				t.Fatal(err)
			}
			fields := sections["NAS.leaf-ä"]
			if !bytes.Equal(raw, original) || fields["host"] != "[fd12::7]" || fields["spn"] != tc.spn || fields["port"] != "1020" || fields["user"] != "owner" || fields["pass"] != "opaque" || fields["domain"] != "DOMAIN" || sections["ssh"]["host"] != "ssh.example" {
				t.Fatal("pin changed authentication identity or another leaf", sections)
			}
		})
	}
}

func TestSourceProcessLeaseRetiresSMBPinAfterDNSChange(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	addresses := []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")}
	policy := remotemedia.Policy{Resolver: egressResolver(func(context.Context, string, string) ([]netip.Addr, error) { return addresses, nil }), Approvals: []remotemedia.Approval{{Root: "smb://nas.example:445/"}}}
	egress, err := f.s.startRcloneEgress(ctx, []byte("[nas]\ntype=smb\nhost=nas.example\n"), "nas:", policy, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer egress.Close()
	lease, err := f.s.reserveSourceProcess(ctx, "remote", "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	lease.egress = egress
	if err = lease.Check(ctx); err != nil {
		t.Fatal(err)
	}
	addresses = []netip.Addr{netip.MustParseAddr("8.8.8.8")}
	if err = lease.Check(ctx); !errors.Is(err, storage.ErrRemoteChanged) || lease.CheckLocal() == nil {
		t.Fatal("disappearing pin did not retire the process lease", err)
	}
}
