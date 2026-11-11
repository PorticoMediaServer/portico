package db

import (
	"context"
	"database/sql"
	"time"
)

// SetWALLimits lets a test reach the WAL policy's sizes with a small database, and its
// longest hold without waiting seconds. It returns a function that puts the real ones back.
func SetWALLimits(drain, insist int64, hold time.Duration) (restore func()) {
	oldDrain, oldInsist, oldHold := walDrainBytes, walInsistBytes, walHoldMax
	walDrainBytes, walInsistBytes, walHoldMax = drain, insist, hold
	return func() { walDrainBytes, walInsistBytes, walHoldMax = oldDrain, oldInsist, oldHold }
}

// HoldWriter runs a background write that does nothing until release is closed, so a test
// can have a person's write wait behind background work.
func HoldWriter(d *DB, release <-chan struct{}) error {
	return d.write(context.Background(), Background, func(*sql.Tx) error {
		<-release
		return nil
	})
}

// HoldWriterTelling is HoldWriter that closes holding once the background write has the writer.
func HoldWriterTelling(d *DB, holding chan<- struct{}, release <-chan struct{}) error {
	return d.write(context.Background(), Background, func(*sql.Tx) error {
		close(holding)
		<-release
		return nil
	})
}

// PersonWriteTelling is a person's write that changes nothing. It closes queued once the
// write has its place in time, which is what the writer compares with the end of the
// background work before it: a test that lets that work go only after queued is closed has a
// write that waited behind it, however slowly either goroutine runs.
func PersonWriteTelling(d *DB, queued chan<- struct{}) error {
	req := writeRequest{ctx: context.Background(), fn: func(*sql.Tx) error { return nil }, done: make(chan error, 1), person: true, queued: time.Now()}
	close(queued)
	d.queues[User] <- req
	return <-req.done
}

// SetBusyFor lets a test see import batches return to normal without waiting seconds.
func SetBusyFor(busy time.Duration) (restore func()) {
	old := busyFor
	busyFor = busy
	return func() { busyFor = old }
}
