package remotesources

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// nativeSourceConfiguration is written by the actual owner save only after its
// connection test. The backend's retained auth remains in its existing store.
// No old generic rclone row, library approval or caller-supplied grant is used.
type nativeSourceConfiguration struct {
	Host                string               `json:"host"`
	Port                int                  `json:"port"`
	ConfigurationDigest string               `json:"configurationDigest"`
	Approval            remotemedia.Approval `json:"approval"`
}

type nativeSourceAuthority struct {
	endpoint remotemedia.NativeEndpoint
	config   nativeSourceConfiguration
}

func (s *Service) readNativeSourceAuthority(kind string, stored []byte) (*nativeSourceAuthority, error) {
	if kind != "sftp" && kind != "smb" || s.mounts == nil || len(stored) == 0 || len(stored) > 64<<10 {
		return nil, storage.ErrRemoteConfig
	}
	raw, err := s.mounts.Open(stored)
	if err != nil {
		return nil, storage.ErrRemoteConfig
	}
	defer clear(raw)
	var config nativeSourceConfiguration
	if json.Unmarshal(raw, &config) != nil || len(config.Approval.Addresses) > 32 {
		return nil, storage.ErrRemoteConfig
	}
	digest, err := hex.DecodeString(config.ConfigurationDigest)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != config.ConfigurationDigest {
		return nil, storage.ErrRemoteConfig
	}
	endpoint := remotemedia.NativeEndpoint{Scheme: kind, Host: config.Host, Port: config.Port}
	root, err := endpoint.Root()
	if err != nil || config.Approval.Root != root {
		return nil, storage.ErrRemoteConfig
	}
	return &nativeSourceAuthority{endpoint: endpoint, config: config}, nil
}

func (s *Service) checkNativeMaterial(ctx context.Context, authority *nativeSourceAuthority, backend, host, port string, configuration []byte) error {
	if authority == nil || authority.endpoint.Scheme != backend || hex.EncodeToString(sha256Sum(configuration)) != authority.config.ConfigurationDigest {
		return storage.ErrRemoteConfig
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return storage.ErrRemoteConfig
	}
	actual := remotemedia.NativeEndpoint{Scheme: backend, Host: host, Port: n}
	root, err := actual.Root()
	expected, e := authority.endpoint.Root()
	if err != nil || e != nil || root != expected {
		return storage.ErrRemoteChanged
	}
	_, err = (remotemedia.Policy{Resolver: s.resolver}).CheckNativeEndpoint(ctx, actual, authority.config.Approval)
	return err
}

func managedSourceKind(kind string) bool {
	return kind == "rclone" || kind == "sftp" || kind == "smb"
}
