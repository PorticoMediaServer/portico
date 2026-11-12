package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	"modernc.org/sqlite"
)

// An online backup of the database while it is in use. It runs on the write connection, a few
// hundred pages a step, each step queued as background work: people's writes go first, every
// read it makes lasts milliseconds (so the WAL's ceiling holds), and what the server writes
// between steps is carried into the copy by SQLite itself, so the copy is the database as it
// was when the last step ran, and never has to start again.

// backupPagesPerStep is how much one step copies: 256 pages of 4 KiB is 1 MiB.
const backupPagesPerStep = 256

type backuper interface {
	NewBackup(dstURI string) (*sqlite.Backup, error)
}

// BackupTo copies the database to a new file at path. progress, when set, is told how many
// pages are left after each step. On failure the partial copy is the caller's to remove.
func (d *DB) BackupTo(ctx context.Context, path string, progress func(left int)) error {
	if !d.backingUp.CompareAndSwap(false, true) {
		return errors.New("a backup is already being made")
	}
	defer d.backingUp.Store(false)
	var b *sqlite.Backup
	step := func(fn func(driverConn any) error) error {
		req := writeRequest{ctx: ctx, raw: fn, done: make(chan error, 1)}
		select {
		case d.queues[Background] <- req:
			return <-req.done
		case <-ctx.Done():
			return ctx.Err()
		case <-d.closed:
			return errClosed
		}
	}
	err := step(func(dc any) error {
		conn, ok := dc.(backuper)
		if !ok {
			return errors.New("this SQLite driver cannot make an online backup")
		}
		var err error
		b, err = conn.NewBackup("file:" + url.PathEscape(path))
		return err
	})
	if err != nil {
		return fmt.Errorf("starting the backup: %w", err)
	}
	finish := func() error { return step(func(any) error { return b.Finish() }) }
	for {
		var more bool
		var left int
		err := step(func(any) error {
			var err error
			more, err = b.Step(backupPagesPerStep)
			left = b.Remaining()
			return err
		})
		if err != nil {
			// The backup object is the write connection's: it is finished there even when the
			// caller has given up.
			req := writeRequest{ctx: context.Background(), raw: func(any) error { return b.Finish() }, done: make(chan error, 1)}
			select {
			case d.queues[Background] <- req:
				<-req.done
			case <-d.closed:
			}
			return err
		}
		if progress != nil {
			progress(left)
		}
		if !more {
			return finish()
		}
	}
}

// RecordBackup writes a backup made, deleted or restored by a person to the activity record.
func (d *DB) RecordBackup(ctx context.Context, actorID int64, action, backupID string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error { return audit(tx, actorID, action, backupID, "", now) })
}

// ConfirmRestore settles that the owner asking for a restore showed their current password
// (and code, with two-step sign-in), counting a wrong one like any other, and records the
// restore in the activity record. A wrong password is ErrBadCredentials.
func (d *DB) ConfirmRestore(ctx context.Context, p PasswordProof, backupID string, now time.Time) error {
	var refused error
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		refused = nil
		ok, err := proven(tx, now, p, &refused)
		if err != nil || !ok {
			return err
		}
		return audit(tx, p.UserID, "backup.restore", backupID, "staged; made at the next start", now)
	})
	if err == nil {
		err = refused
	}
	return err
}

// RecordRestore records in the activity record a restore whose owner was proven some other
// way than a password: by their Portico Account's fresh approval.
func (d *DB) RecordRestore(ctx context.Context, actorID int64, backupID string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		return audit(tx, actorID, "backup.restore", backupID, "staged; made at the next start", now)
	})
}

// EndEverySignIn ends every sign-in, after a restore: the restored database's sessions are
// sign-ins from before it. API keys and personal tokens are not sign-ins and keep working.
func (d *DB) EndEverySignIn(ctx context.Context, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		return revoke(tx, c, now, "restored", `1 = 1`)
	})
}
