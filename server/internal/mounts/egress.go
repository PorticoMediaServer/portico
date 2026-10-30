package mounts

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SourceEgress is private process custody returned by the remote-source owner.
// Proxy credentials travel only through helper stdin/environment. Close is
// retained until physical process retirement, including failed startup.
type SourceEgress struct {
	HTTPProxy, SFTPProxy string
	// Configuration is an optional launch-only copy with checked SMB hosts.
	// It is never material for a retained-configuration digest check.
	Configuration []byte
	// CheckSMBPins fences the process lease when DNS drops its launch address.
	CheckSMBPins func(context.Context) error
	Close        func()
}

func (e *SourceEgress) TransientConfiguration(original []byte) []byte {
	if e.Configuration != nil {
		return e.Configuration
	}
	return original
}

func validPolicyProxy(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User == nil || u.User.Username() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	password, ok := u.User.Password()
	port, err := strconv.Atoi(u.Port())
	return ok && password != "" && err == nil && port > 0 && port <= 65535
}

// prepareBackendEgress captures the same bytes that were checked and places
// only its launch-specific transient copy in the helper. The retained
// configuration is never rewritten to add credentials or transport options.
func (s *Service) prepareBackendEgress(ctx context.Context, id, generation string) (string, *SourceEgress, error) {
	if s.PrepareSourceEgress == nil {
		return "", nil, storage.ErrRemoteConfig
	}
	material, err := s.CaptureServeSource(ctx, id, generation)
	if err != nil {
		return "", nil, err
	}
	defer clear(material.Configuration)
	egress, err := s.PrepareSourceEgress(ctx, material)
	if err != nil {
		return "", nil, err
	}
	if egress == nil || egress.Close == nil || !validPolicyProxy(egress.HTTPProxy) || egress.SFTPProxy != "" && !validPolicyProxy(egress.SFTPProxy) {
		if egress != nil && egress.Close != nil {
			egress.Close()
		}
		return "", nil, storage.ErrRemoteConfig
	}
	file, err := s.candidateFile(egress.TransientConfiguration(material.Configuration))
	if err != nil {
		egress.Close()
		return "", nil, err
	}
	closeEgress := egress.Close
	var once sync.Once
	egress.Close = func() { once.Do(func() { closeEgress(); _ = os.Remove(file) }) }
	return file, egress, nil
}
