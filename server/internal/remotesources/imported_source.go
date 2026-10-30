package remotesources

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Only the successful owner-save publication writes these approvals. Raw
// config/import/owner role/library policy cannot manufacture this record.
type importedSourceConfiguration struct {
	ConfigurationDigest string                 `json:"configurationDigest"`
	Approvals           []remotemedia.Approval `json:"approvals"`
}

func (s *Service) importedSourceConfig(stored []byte) (importedSourceConfiguration, error) {
	var out importedSourceConfiguration
	if s.mounts == nil {
		return out, storage.ErrRemoteConfig
	}
	raw, err := s.mounts.Open(stored)
	if err != nil {
		return out, err
	}
	defer clear(raw)
	if json.Unmarshal(raw, &out) != nil || len(out.Approvals) > 32 {
		return out, storage.ErrRemoteConfig
	}
	digest, err := hex.DecodeString(out.ConfigurationDigest)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != out.ConfigurationDigest {
		return out, storage.ErrRemoteConfig
	}
	return out, nil
}

func (s *Service) checkImportedApprovals(ctx context.Context, config importedSourceConfiguration) error {
	policy := remotemedia.Policy{Resolver: s.resolver}
	seen := map[string]bool{}
	for _, approval := range config.Approvals {
		if seen[approval.Root] {
			return storage.ErrRemoteConfig
		}
		seen[approval.Root] = true
		u, err := url.Parse(approval.Root)
		if err != nil {
			return storage.ErrRemoteConfig
		}
		if u.Scheme == "sftp" || u.Scheme == "smb" {
			port, err := strconv.Atoi(u.Port())
			if err != nil {
				return storage.ErrRemoteConfig
			}
			if _, err = policy.CheckNativeEndpoint(ctx, remotemedia.NativeEndpoint{Scheme: u.Scheme, Host: u.Hostname(), Port: port}, approval); err != nil {
				return err
			}
		} else if err = policy.CheckEndpointApproval(ctx, approval.Root, approval); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *Service) checkImportedMaterial(ctx context.Context, config importedSourceConfiguration, raw []byte, remote string) error {
	if config.ConfigurationDigest != hex.EncodeToString(sha256Sum(raw)) {
		return storage.ErrRemoteChanged
	}
	leaves, err := configuredBackends(raw, remote)
	if err != nil {
		return err
	}
	expected := map[string]bool{}
	for _, leaf := range leaves {
		if leaf.Native.Scheme != "" {
			root, err := leaf.Native.Root()
			if err != nil {
				return err
			}
			expected[root] = true
		}
		for _, endpoint := range leaf.Origins {
			// Extract the origin without observing/refreshing approval addresses.
			u, err := url.Parse(endpoint)
			if err != nil {
				return storage.ErrRemoteConfig
			}
			port := u.Port()
			if port == "" {
				port = "80"
				if u.Scheme == "https" {
					port = "443"
				}
			}
			root := u.Scheme + "://" + net.JoinHostPort(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), port) + "/"
			expected[root] = true
		}
	}
	for _, a := range config.Approvals {
		if !expected[a.Root] {
			return storage.ErrRemoteChanged
		}
		delete(expected, a.Root)
	}
	if len(expected) != 0 {
		return storage.ErrRemoteConfig
	}
	return s.checkImportedApprovals(ctx, config)
}
