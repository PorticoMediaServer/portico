package db

import (
	"context"
	"database/sql"
	"time"
)

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

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

// SetBusyFor lets a test see import batches return to normal without waiting seconds.
func SetBusyFor(busy time.Duration) (restore func()) {
	old := busyFor
	busyFor = busy
	return func() { busyFor = old }
}
