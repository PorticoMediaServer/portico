package db

import (
	"context"
	"database/sql"
	"errors"
)

// Priority says whose write this is. A person's action goes ahead of background work.
type Priority int

const (
	User Priority = iota
	Background
)

// userStreak is how many user writes may run in a row while background work is waiting.
// It keeps a scan moving under constant user activity.
const userStreak = 4

var errClosed = errors.New("database is closed")

type writeRequest struct {
	ctx   context.Context
	fn    func(tx *sql.Tx) error
	after func()
	done  chan error
}

// write runs fn in one transaction on the single write connection and commits it.
// Every method of this package that changes data goes through here, so a request is one
// transaction and SQLite never sees two writers. Background work should commit in small
// batches (a few hundred rows) so user writes are never kept waiting long.
//
// ctx can abandon the wait in the queue. Once fn has started, the commit is never cancelled:
// a commit either happened or it did not, and the caller is told which.
func (d *DB) write(ctx context.Context, p Priority, fn func(tx *sql.Tx) error) error {
	return d.writeThen(ctx, p, fn, nil)
}

// writeThen is write, followed by after once the commit has happened and before the next
// write begins. It is for in-memory state that must change in step with what was committed:
// whatever the next transaction reads from memory already reflects this one.
func (d *DB) writeThen(ctx context.Context, p Priority, fn func(tx *sql.Tx) error, after func()) error {
	req := writeRequest{ctx: ctx, fn: fn, after: after, done: make(chan error, 1)}
	select {
	case d.queues[p] <- req:
		return <-req.done
	case <-ctx.Done():
		return ctx.Err()
	case <-d.closed:
		return errClosed
	}
}

func (d *DB) writer() {
	streak := 0
	for {
		var req writeRequest
		if streak >= userStreak {
			streak = 0
			select {
			case req = <-d.queues[Background]:
			default:
				continue
			}
		} else {
			select {
			case req = <-d.queues[User]:
				streak++
			default:
				select {
				case req = <-d.queues[User]:
					streak++
				case req = <-d.queues[Background]:
					streak = 0
				case <-d.closed:
					return
				}
			}
		}
		err := d.run(req)
		if err == nil && req.after != nil {
			req.after()
		}
		req.done <- err
		d.afterWrite()
	}
}

func (d *DB) run(req writeRequest) error {
	if err := req.ctx.Err(); err != nil {
		return err
	}
	tx, err := d.writeConn.BeginTx(context.WithoutCancel(req.ctx), nil)
	if err != nil {
		return err
	}
	if err := req.fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
