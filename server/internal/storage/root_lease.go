package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

var ErrRootLease = errors.New("source root registration lease required")
var ErrObservedSourceLost = errors.New("observed source acquisition continuity lost")
var ErrObservedSourceUnsupported = errors.New("observed source platform unsupported")

// RootLease is supplied by the actual root registration owner. Directory is its
// borrowed duplicated descriptor, never opened or statted in the source parent.
// Release is idempotent and owns that descriptor. Lifetime is independent of
// setup cancellation and ends when the registration or actual mount owner ends.
// Catalog origin revisions and this physical registration are distinct evidence.
type RootLease struct {
	Operations                             *OperationScope
	Directory                              *os.File
	RegistrationID, RootPath, RelativePath string
	MountPath, MountIdentity               string
	Lifetime                               context.Context
	Release                                func()
}

func validRootLease(l *RootLease) bool {
	return l != nil && l.Directory != nil && l.RegistrationID != "" && len(l.RegistrationID) <= 4096 && filepath.IsAbs(l.RootPath) && filepath.IsLocal(l.RelativePath) && l.RelativePath != "." && l.Lifetime != nil && l.Release != nil
}

// Invalid provider results still dispose any explicitly transferred descriptor.
// A missing Release cannot acknowledge the provider's own accounting; its owner
// must reconcile malformed registrations independently.
func releaseRootLease(l *RootLease) {
	if l == nil {
		return
	}
	if l.Release != nil {
		l.Release()
	} else if l.Directory != nil {
		l.Directory.Close()
	}
}

// Close disposes a resolver result that was never transferred to a helper.
func (l *RootLease) Close() error { releaseRootLease(l); return nil }
