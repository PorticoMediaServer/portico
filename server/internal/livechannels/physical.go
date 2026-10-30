package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

// PhysicalLocks are private, inherited process locks, not PID files. A decoder
// inherits the same open file description; parent death cannot release capacity
// while that child still owns it. Never explicitly unlock a shared description.
type PhysicalLocks struct{ root string }

func NewPhysicalLocks(root string) (*PhysicalLocks, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrInvalid
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	p, e := filepath.EvalSymlinks(root)
	if e != nil || p != root {
		return nil, ErrInvalid
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() {
		return nil, ErrInvalid
	}
	return &PhysicalLocks{root}, nil
}
func (p *PhysicalLocks) Lock(a Allocation) (*os.File, error) {
	if !opaque(a.ID) || a.Generation < 1 {
		return nil, ErrInvalid
	}
	name := filepath.Join(p.root, fmt.Sprintf("%s.%d.lock", a.ID, a.Generation))
	f, e := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	entry, le := os.Lstat(name)
	if e != nil || le != nil || !info.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, entry) {
		f.Close()
		return nil, ErrInvalid
	}
	if e = lockPhysical(f); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}

const RetirementGrace = 10 * time.Second

// ReconcileExpired must run outside any caller transaction. File locking occurs
// outside SQLite. It rechecks exact token/generation/deadline while holding proof
// of physical retirement; expiry alone never recycles a tuner.
func (p *PhysicalLocks) ReconcileExpired(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	var cursor string
	if e := db.QueryRowContext(ctx, `SELECT cursor FROM live_allocation_reconcile_cursor WHERE singleton=1`).Scan(&cursor); e != nil {
		return 0, e
	}
	rows, e := db.QueryContext(ctx, `SELECT id,source_id,token,generation FROM live_allocations WHERE state!='released' AND lease_until_ms<? AND id>? ORDER BY id LIMIT 64`, now.Add(-RetirementGrace).UnixMilli(), cursor)
	if e != nil {
		return 0, e
	}
	list := []Allocation{}
	for rows.Next() {
		var a Allocation
		if e = rows.Scan(&a.ID, &a.SourceID, &a.Token, &a.Generation); e != nil {
			rows.Close()
			return 0, e
		}
		list = append(list, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	count := 0
	for _, a := range list {
		cursor = a.ID
		lock, e := p.Lock(a)
		if errors.Is(e, ErrPhysicalBusy) {
			continue
		}
		if e != nil {
			return count, e
		}
		gated, e := dbwork.Begin(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
		if e != nil {
			lock.Close()
			return count, e
		}
		tx := gated.Tx()
		var valid bool
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM live_allocations WHERE id=? AND token=? AND generation=? AND state!='released' AND lease_until_ms<?)`, a.ID, a.Token, a.Generation, now.Add(-RetirementGrace).UnixMilli()).Scan(&valid)
		if e == nil && valid {
			e = ReleaseAllocationTx(ctx, tx, a)
		}
		if e == nil {
			e = gated.Commit()
		} else {
			_ = gated.Rollback()
		}
		_ = lock.Close()
		if e != nil {
			return count, e
		}
		if valid {
			count++
		}
	}
	if len(list) < 64 {
		cursor = ""
	}
	// An unchanged cursor is not a publication. Rewriting the empty cursor on
	// every idle pass woke every commit-subscribed background lane once a second.
	if _, e = dbwork.ExecWrite(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `UPDATE live_allocation_reconcile_cursor SET cursor=? WHERE singleton=1 AND cursor IS NOT ?`, cursor, cursor); e != nil {
		return count, e
	}
	return count, nil
}
