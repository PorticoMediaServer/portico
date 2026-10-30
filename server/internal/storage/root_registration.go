package storage

import (
	"context"
	"os"
	"sync"
)

// RootRegistration owns a helper-acquired directory. Invalidating it fences
// borrows immediately; outstanding helpers retain their duplicate until Wait.
type RootRegistration struct {
	operations                         *OperationScope
	mu                                 sync.Mutex
	directory                          *os.File
	path, id, mountPath, mountIdentity string
	life                               context.Context
	cancel                             context.CancelFunc
	stopOwner                          func() bool
	closed                             bool
}

func (r *RootRegistration) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.cancel()
	if r.stopOwner != nil {
		r.stopOwner()
	}
	return r.directory.Close()
}

func (r *RootRegistration) Borrow(relative string) (*RootLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.life.Err() != nil {
		return nil, ErrRootLease
	}
	file, err := duplicateRoot(r.directory)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	lease := &RootLease{Operations: r.operations, Directory: file, RegistrationID: r.id, RootPath: r.path, RelativePath: relative, Lifetime: r.life, Release: func() { once.Do(func() { file.Close() }) }, MountPath: r.mountPath, MountIdentity: r.mountIdentity}
	if !validRootLease(lease) {
		lease.Close()
		return nil, ErrRootLease
	}
	return lease, nil
}

// Lifetime ends on explicit registry close or actual owner invalidation.
func (r *RootRegistration) Lifetime() context.Context { return r.life }

type registrationLifetime struct {
	context.Context
	owner context.Context
}

func (l *registrationLifetime) Err() error {
	if err := l.owner.Err(); err != nil {
		return err
	}
	return l.Context.Err()
}
