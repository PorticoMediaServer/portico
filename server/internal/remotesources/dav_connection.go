package remotesources

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type DAVConnectionInput struct {
	Name          string `json:"name"`
	Root          string `json:"root"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	InsecureLocal bool   `json:"insecureLocal"`
}

type DAVConnectionResult struct {
	Origin       string                        `json:"origin"`
	RangeSupport remotemedia.SetupRangeSupport `json:"rangeSupport"`
}

func validDAVFields(name, root, username, password string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= 100 && len(root) <= 8192 && len(username) <= 1024 && len(password) <= 8192
}

// TestDAVConnection is the new-source wizard's unsaved connection test. Its
// temporary endpoint approval and credentials are never published. Saving the
// source still performs its own current, fingerprint-bound test; this response
// is neither a saved source nor a permission to browse or read one.
func (s *Service) TestDAVConnection(ctx context.Context, in DAVConnectionInput, authorize func(*sql.Tx) error) (DAVConnectionResult, error) {
	unknown := DAVConnectionResult{RangeSupport: remotemedia.SetupRangeUnknown}
	if s == nil || s.db == nil || s.mounts == nil {
		return unknown, storage.ErrRemoteConfig
	}
	s.commands.Lock()
	defer s.commands.Unlock()
	if err := s.authorize(ctx, authorize); err != nil {
		return unknown, err
	}
	if !validDAVFields(in.Name, in.Root, in.Username, in.Password) {
		return unknown, storage.ErrRemoteConfig
	}
	config, err := remotemedia.ValidateSourceDAVConfig(ctx, remotemedia.DAVConfig{Root: in.Root, Username: in.Username, Password: in.Password, InsecureLocal: in.InsecureLocal}, s.resolver)
	if err != nil {
		return unknown, mapError(err)
	}
	id := identity.Token()
	candidate := Source{ID: id, Kind: SourceWebDAV, Name: strings.TrimSpace(in.Name), RootPath: filepath.Join(s.private, id), Generation: 1}
	support, check, err := s.testDAVSetup(ctx, "", candidate, config, authorize)
	if err != nil {
		return unknown, mapError(err)
	}
	if err = s.authorize(ctx, check); err != nil {
		return unknown, err
	}
	return DAVConnectionResult{Origin: safeOrigin(config.Root), RangeSupport: support}, nil
}
