package mounts

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
)

// RuntimeLease identifies one actual guardian child incarnation. A database
// restart generation alone cannot identify the physical process that owns IO.
type RuntimeLease struct {
	ID, Path, Identity string
	Lifetime           context.Context
}

func newRuntimeChild(generation int64) *child {
	life, cancel := context.WithCancel(context.Background())
	return &child{restartGeneration: generation, runtimeID: identity.Token(), lifetime: life, invalidate: cancel}
}
func (c *child) invalidateRuntime() {
	if c != nil && c.invalidate != nil {
		c.invalidate()
	}
}

// LeaseFor uses the complete selected target and the deepest enclosing managed
// root. All operations are memory-only; filesystem inspection remains in helpers.
func (s *Service) LeaseFor(path string) (*RuntimeLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := ""
	selectedPath := ""
	for id, root := range s.paths {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && len(root) > len(selectedPath) {
			selected = id
			selectedPath = root
		}
	}
	if selected == "" {
		return nil, nil
	}
	c := s.active[selected]
	if !s.available[selected] || c == nil || c.stopping || c.lifetime == nil || c.mountIdentity == "" || c.lifetime.Err() != nil {
		return nil, ErrUnavailable
	}
	return &RuntimeLease{ID: c.runtimeID, Path: selectedPath, Identity: c.mountIdentity, Lifetime: c.lifetime}, nil
}

// bindMountIdentity is called under service ownership after a physical helper
// observation. An existing child never adopts a replacement filesystem.
func (c *child) bindMountIdentity(physical string) bool {
	if c.lifetime == nil || c.lifetime.Err() != nil || physical == "" {
		return false
	}
	if c.mountIdentity != "" && c.mountIdentity != physical {
		c.invalidateRuntime()
		return false
	}
	c.mountIdentity = physical
	return true
}
