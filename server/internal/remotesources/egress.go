package remotesources

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func (s *Service) startRcloneEgress(ctx context.Context, raw []byte, remote string, policy remotemedia.Policy, check func(context.Context) error) (*mounts.SourceEgress, error) {
	leaves, err := configuredBackends(raw, remote)
	if err != nil {
		return nil, err
	}
	sftpPolicy := remotemedia.Policy{Resolver: policy.Resolver}
	seen := map[string]bool{}
	hosts := map[string]netip.Addr{}
	var pins []smbPin
	for _, leaf := range leaves {
		if leaf.Native.Scheme == "" {
			continue
		}
		root, err := leaf.Native.Root()
		if err != nil {
			return nil, err
		}
		var saved remotemedia.Approval
		for _, approval := range policy.Approvals {
			if approval.Root == root {
				saved = approval
				break
			}
		}
		checked, err := policy.CheckNativeEndpoint(ctx, leaf.Native, saved)
		if err != nil {
			return nil, err
		}
		if leaf.Native.Scheme == "smb" {
			name, _, _ := strings.Cut(leaf.Remote, ":")
			if _, pinned := hosts[name]; !pinned {
				address, err := chooseSMBPin(checked, saved)
				if err != nil {
					return nil, err
				}
				hosts[name] = address
				pins = append(pins, smbPin{endpoint: leaf.Native, approval: saved, address: address})
			}
		}
		if leaf.Native.Scheme == "sftp" && !seen[root] {
			seen[root] = true
			sftpPolicy.Approvals = append(sftpPolicy.Approvals, saved)
		}
	}
	var configuration []byte
	if len(pins) != 0 {
		configuration, err = pinSMBConfiguration(raw, hosts)
		if err != nil {
			return nil, err
		}
	}
	keepConfiguration := false
	defer func() {
		if !keepConfiguration {
			clear(configuration)
		}
	}()
	httpProxy, err := remotemedia.StartPolicyProxy(ctx, policy, identity.Token(), identity.Token(), check)
	if err != nil {
		return nil, err
	}
	var sftpProxy *remotemedia.PolicyProxy
	var once sync.Once
	egress := &mounts.SourceEgress{HTTPProxy: httpProxy.Locator(), Configuration: configuration}
	if len(pins) != 0 {
		egress.CheckSMBPins = func(ctx context.Context) error { return checkSMBPins(ctx, policy, pins) }
	}
	egress.Close = func() {
		once.Do(func() {
			clear(configuration)
			httpProxy.Close()
			if sftpProxy != nil {
				sftpProxy.Close()
				_ = sftpProxy.Wait(context.Background())
			}
			_ = httpProxy.Wait(context.Background())
		})
	}
	if len(sftpPolicy.Approvals) != 0 {
		sftpProxy, err = remotemedia.StartSFTPPolicyProxy(ctx, sftpPolicy, identity.Token(), identity.Token(), check)
		if err != nil {
			egress.Close()
			return nil, err
		}
		egress.SFTPProxy = sftpProxy.Locator()
	}
	keepConfiguration = true
	return egress, nil
}

// prepareMountEgress gives legacy mount/lsjson/cat children the same saved
// source approval as serve. Source/config generations and policy are rechecked
// on every proxy request; raw configuration never becomes an approval.
func (s *Service) prepareMountEgress(ctx context.Context, material mounts.ServeSource) (*mounts.SourceEgress, error) {
	read := func(ctx context.Context) (sourceNetwork, error) {
		var out sourceNetwork
		tx, close, err := dbwork.BeginRead(ctx, s.db)
		if err != nil {
			return out, err
		}
		defer close()
		var source, root, remote string
		var generation int64
		err = tx.QueryRowContext(ctx, `SELECT r.id,r.root,m.remote,b.generation FROM remote_sources r JOIN managed_mounts m ON m.id=r.mount_id JOIN mount_backend_configs b ON b.mount_id=m.id JOIN mount_controls c ON c.mount_id=m.id WHERE r.mount_id=? AND r.removed=0 AND c.removal_pending=0`, material.MountID).Scan(&source, &root, &remote, &generation)
		if err != nil {
			return out, err
		}
		if strconv.FormatInt(generation, 10) != material.Generation || remote != material.Remote {
			return out, storage.ErrRemoteChanged
		}
		return s.sourceNetworkTx(ctx, tx, source, root)
	}
	network, err := read(ctx)
	if err != nil {
		return nil, err
	}
	backend, host, port, err := serveTransport(material.Configuration, material.Remote)
	if err != nil {
		return nil, err
	}
	if network.Imported != nil {
		if err = s.checkImportedMaterial(ctx, *network.Imported, material.Configuration, material.Remote); err != nil {
			return nil, err
		}
	} else if err = s.checkNativeMaterial(ctx, network.Native, backend, host, port, material.Configuration); err != nil {
		return nil, err
	}
	check := func(ctx context.Context) error {
		current, err := read(ctx)
		if err != nil {
			return err
		}
		if current.Fingerprint != network.Fingerprint {
			return storage.ErrRemoteChanged
		}
		return ctx.Err()
	}
	return s.startRcloneEgress(ctx, material.Configuration, material.Remote, network.Policy, check)
}
