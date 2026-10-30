package mounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
	"time"
)

var ErrAllocationCapacity = errors.New("Private storage allocations need recovery before another configuration can be created.")

func syncDirectory(path string) error { return dirsync.Sync(path) }

// Caller holds the ownership mutex through allocation and acceptance. The durable
// reservation is committed before either filesystem path is created.
func (s *Service) reserveAllocation(id string, encrypted []byte) error {
	var count int
	if e := s.db.QueryRow(`SELECT count(*) FROM mount_allocations`).Scan(&count); e != nil {
		return e
	}
	if count >= 8 {
		return ErrAllocationCapacity
	}
	// Bound namespace inspection without loading arbitrary directory inventories.
	var bytes int64
	for _, path := range []string{s.root, s.private} {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		entries, e := f.ReadDir(33)
		f.Close()
		if e != nil && !errors.Is(e, io.EOF) {
			return e
		}
		if len(entries) > 32 {
			return ErrAllocationCapacity
		}
		for _, entry := range entries {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if !info.IsDir() {
				bytes += info.Size()
				if bytes > 2<<20 {
					return ErrAllocationCapacity
				}
			}
		}
	}
	hash := sha256.Sum256(encrypted)
	_, e := dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive), `INSERT INTO mount_allocations(id,config_hash,created_at) VALUES(?,?,?)`, id, hex.EncodeToString(hash[:]), time.Now().Unix())
	return e
}

// Two reservations per tick, with quarantine retained against the allocation cap.
// Only this process's private namespace is considered, never arbitrary files.
func (s *Service) recoverAllocations(ctx context.Context) {
	unlock, e := s.allocationLock()
	if e != nil {
		return
	}
	defer unlock()
	rows, e := s.db.Query(`SELECT id,directory_identity,config_hash FROM mount_allocations WHERE status='reserved' ORDER BY created_at,id LIMIT 2`)
	if e != nil {
		return
	}
	type allocation struct{ id, identity, hash string }
	list := []allocation{}
	for rows.Next() {
		var a allocation
		if rows.Scan(&a.id, &a.identity, &a.hash) != nil {
			rows.Close()
			return
		}
		list = append(list, a)
	}
	rows.Close()
	if len(list) == 0 {
		return
	}
	// Reconciliation also runs after startup. Cover removal through the final
	// allocation-row cleanup, without perturbing idle backup generations.
	defer authorityfiles.Process.Admit()()
	for _, a := range list {
		var references int
		e = s.db.QueryRow(`SELECT count(*) FROM managed_mounts WHERE id=? OR mount_path=?`, a.id, filepath.Join(s.root, a.id)).Scan(&references)
		if e != nil {
			continue
		}
		if references > 0 || s.active[a.id] != nil {
			continue
		}
		// Never interpret malformed manifest IDs as paths.
		if a.id == "" || filepath.Base(a.id) != a.id || a.id == "." || a.id == ".." {
			s.quarantineAllocation(a.id)
			continue
		}
		config := filepath.Join(s.private, a.id+".conf")
		info, e := os.Lstat(config)
		if e == nil {
			if !info.Mode().IsRegular() || info.Size() > 96<<10 {
				s.quarantineAllocation(a.id)
				continue
			}
			file, e := os.Open(config)
			if e != nil {
				continue
			}
			raw, e := io.ReadAll(io.LimitReader(file, (96<<10)+1))
			file.Close()
			if e != nil {
				continue
			}
			hash := sha256.Sum256(raw)
			if hex.EncodeToString(hash[:]) != a.hash {
				s.quarantineAllocation(a.id)
				continue
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			continue
		}
		path := filepath.Join(s.root, a.id)
		if a.identity == "" {
			if _, e = os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				s.quarantineAllocation(a.id)
				continue
			}
		} else {
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			e = s.storage.RemoveOwnedMountRoot(check, path, a.identity)
			cancel()
			if e != nil {
				s.quarantineAllocation(a.id)
				continue
			}
		}
		// Config digest was checked before empty-root removal; same-user OS mutation
		// is outside the service authority boundary, but never follow symlinks.
		if e = os.Remove(config); e != nil && !errors.Is(e, os.ErrNotExist) {
			continue
		}
		if syncDirectory(s.root) != nil || syncDirectory(s.private) != nil {
			continue
		}
		_, _ = dbwork.ExecWrite(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `DELETE FROM mount_allocations WHERE id=?`, a.id)
	}
}
func (s *Service) quarantineAllocation(id string) {
	_, _ = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive), `UPDATE mount_allocations SET status='quarantined' WHERE id=?`, id)
}
