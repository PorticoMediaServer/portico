package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
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
	// raw, when set, is run with the write connection's driver connection instead of fn in a
	// transaction: for a step of an online backup, which must run on the connection that
	// writes so that what it writes between steps reaches the copy.
	raw    func(driverConn any) error
	person bool      // a person's write, not background work
	queued time.Time // when it joined the queue
}

// A person's write that had to wait behind background work is a sign that someone is using
// the server while an import runs. For busyFor after the last such wait, background work is
// advised to keep each transaction to busyBatch, so the next tap waits about that long and
// not a whole batch. With nobody waiting, an import uses its full batches and its full speed.
var (
	busyFor   = 5 * time.Second
	busyBatch = 5 * time.Millisecond
)

// NextImportFiles advises how many files the next import transaction should hold, given how
// many the last one held and how long it took. It is normal, the caller's own batch size,
// unless a person's write has lately waited behind background work; then it is as many as
// fit in busyBatch.
func (d *DB) NextImportFiles(files, normal int, took time.Duration) int {
	if time.Since(time.Unix(0, d.personWaited.Load())) > busyFor || files <= 0 || took <= 0 {
		return normal
	}
	return min(max(int(float64(files)*float64(busyBatch)/float64(took)), 1), normal)
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
	req := writeRequest{ctx: ctx, fn: fn, after: after, done: make(chan error, 1), person: p == User, queued: time.Now()}
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
	var lastBackground time.Time // when background work last finished
	for {
		// While the WAL is being emptied, background work waits and the writer looks in on
		// the WAL whenever it has nothing else to do. See checkpoint.go.
		background := d.queues[Background]
		var lookIn <-chan time.Time
		if d.draining.Load() {
			background, lookIn = nil, time.After(walDrainInterval)
		}
		var req writeRequest
		if streak >= userStreak {
			streak = 0
			select {
			case req = <-background:
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
				case req = <-background:
					streak = 0
				case <-lookIn:
					d.emptyWAL(0)
					d.afterWrite()
					continue
				case <-d.closed:
					return
				}
			}
		}
		// A person's write that was already waiting when background work last finished has
		// waited behind it.
		if req.person && lastBackground.After(req.queued) {
			d.personWaited.Store(time.Now().UnixNano())
		}
		err := d.run(req)
		if !req.person {
			lastBackground = time.Now()
		}
		if err == nil && req.after != nil {
			req.after()
		}
		if err == nil {
			select {
			case d.changed <- struct{}{}:
			default:
			}
		}
		req.done <- err
		d.afterWrite()
	}
}

func (d *DB) run(req writeRequest) error {
	if err := req.ctx.Err(); err != nil {
		return err
	}
	if req.raw != nil {
		conn, err := d.writeConn.Conn(context.WithoutCancel(req.ctx))
		if err != nil {
			return err
		}
		defer conn.Close()
		return conn.Raw(req.raw)
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
