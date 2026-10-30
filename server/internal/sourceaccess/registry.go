// Package sourceaccess owns physical root registrations. Catalog authorization
// remains with the caller; no pathname or filesystem observation grants access.
package sourceaccess

import (
	"context"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"sync"
)

var ErrAuthority = errors.New("source registration authority is unavailable")

// Revision includes the root incarnation and revision, never revision alone.
// OwnerID identifies the exact attempt whose live authority owns Lifetime.
// Lifetime is the real attempt owner, independent of its preparation deadline. Validate compares the exact selected
// asset, item and association snapshot against current authorized catalog state.
type Authority struct {
	OwnerID, RootID, RootPath, RelativePath, Revision string
	Lifetime                                          context.Context
	Validate                                          func(context.Context) error
}
type mountOwner interface {
	LeaseFor(string) (*mounts.RuntimeLease, error)
}
type Registry struct {
	operations *storage.OperationScope
	mu         sync.Mutex
	storage    *storage.Client
	mounts     mountOwner
	entries    map[string]*storage.RootRegistration
	closed     bool
}

func New(client *storage.Client, mounts mountOwner) *Registry {
	scope := storage.NewOperationScope()
	var scoped *storage.Client
	if client != nil {
		copy := *client
		copy.SourceOperations = scope
		scoped = &copy
	}
	return &Registry{operations: scope, storage: scoped, mounts: mounts, entries: map[string]*storage.RootRegistration{}}
}

// Borrow runs no source filesystem calls in the parent and holds no lock across
// authorization or registration IO. Each slow acquisition is revalidated before
// adoption; canceled owners cannot revive a registration by requesting it again.
func (r *Registry) Borrow(ctx context.Context, a Authority) (*storage.RootLease, error) {
	if a.OwnerID == "" || a.RootID == "" || a.Revision == "" || !filepath.IsAbs(a.RootPath) || !filepath.IsLocal(a.RelativePath) || a.RelativePath == "." || a.Lifetime == nil || a.Validate == nil || a.Lifetime.Err() != nil {
		return nil, ErrAuthority
	}
	if err := a.Validate(ctx); err != nil {
		return nil, err
	}
	target := filepath.Join(a.RootPath, a.RelativePath)
	var mount *mounts.RuntimeLease
	var err error
	if r.mounts != nil {
		mount, err = r.mounts.LeaseFor(target)
		if err != nil {
			return nil, err
		}
	}
	path, relative, mountPath, mountID, mountIdentity := a.RootPath, a.RelativePath, "", "", ""
	owners := []context.Context{a.Lifetime}
	if mount != nil {
		if mount.Lifetime == nil || mount.Lifetime.Err() != nil || mount.ID == "" {
			return nil, ErrAuthority
		}
		mountPath, mountID, mountIdentity = mount.Path, mount.ID, mount.Identity
		owners = append(owners, mount.Lifetime)
		// When a library contains a nested managed mount, register the actual mount
		// root itself. A descriptor above the overlay cannot establish its identity.
		if rel, e := filepath.Rel(a.RootPath, mount.Path); e == nil && filepath.IsLocal(rel) {
			path = mount.Path
			relative, err = filepath.Rel(path, target)
			if err != nil {
				return nil, err
			}
		}
	}
	key := a.OwnerID + "\x00" + a.RootID + "\x00" + a.Revision + "\x00" + path + "\x00" + mountID
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrAuthority
	}
	registered := r.entries[key]
	r.mu.Unlock()
	if registered == nil {
		life, cancel := context.WithCancel(context.Background())
		combined := &ownerLifetime{Context: life, owners: owners}
		stops := make([]func() bool, 0, len(owners))
		for _, owner := range owners {
			stops = append(stops, context.AfterFunc(owner, cancel))
		}
		// The registry retains these callbacks with its registration. On invalidation
		// cancel releases them; no background callback retains an immortal owner.
		context.AfterFunc(life, func() {
			for _, stop := range stops {
				stop()
			}
		})
		candidate, e := r.storage.RegisterRoot(ctx, path, mountPath, mountIdentity, combined)
		if e != nil {
			cancel()
			return nil, e
		}
		context.AfterFunc(candidate.Lifetime(), cancel)
		if e = a.Validate(ctx); e != nil || combined.Err() != nil {
			candidate.Close()
			cancel()
			if e != nil {
				return nil, e
			}
			return nil, ErrAuthority
		}
		r.mu.Lock()
		if r.closed || combined.Err() != nil {
			r.mu.Unlock()
			candidate.Close()
			cancel()
			return nil, ErrAuthority
		}
		if registered = r.entries[key]; registered == nil {
			registered = candidate
			r.entries[key] = registered
			context.AfterFunc(combined, func() {
				r.mu.Lock()
				if r.entries[key] == candidate {
					delete(r.entries, key)
				}
				r.mu.Unlock()
				candidate.Close()
				cancel()
			})
			r.mu.Unlock()
		} else {
			r.mu.Unlock()
			candidate.Close()
			cancel()
		}
	}
	if err = a.Validate(ctx); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || a.Lifetime.Err() != nil {
		return nil, ErrAuthority
	}
	lease, err := registered.Borrow(relative)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil || a.Lifetime.Err() != nil || lease.Lifetime.Err() != nil {
		lease.Close()
		return nil, ErrAuthority
	}
	return lease, nil
}
func (r *Registry) Close() error {
	r.operations.Close()
	r.mu.Lock()
	r.closed = true
	entries := r.entries
	r.entries = map[string]*storage.RootRegistration{}
	r.mu.Unlock()
	for _, entry := range entries {
		entry.Close()
	}
	return nil
}

// Err observes actual owners directly; callbacks only wake Done waiters.
type ownerLifetime struct {
	context.Context
	owners []context.Context
}

func (l *ownerLifetime) Err() error {
	for _, owner := range l.owners {
		if err := owner.Err(); err != nil {
			return err
		}
	}
	return l.Context.Err()
}

// Shutdown waits only this registry runtime's physical helpers, including
// canceled setup attempts that never reached entries. Call outside DB locks.
func (r *Registry) Shutdown(ctx context.Context) error { r.Close(); return r.operations.Wait(ctx) }
