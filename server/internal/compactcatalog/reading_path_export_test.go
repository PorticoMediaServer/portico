package compactcatalog

import (
	"context"
	"database/sql"
	"time"
)

// SetReadingPathClock replaces the quiet-gap clock until the returned restore.
func SetReadingPathClock(now func() time.Time) (restore func()) {
	old := readingPathNow
	readingPathNow = now
	return func() { readingPathNow = old }
}

// ReadingPathNextDue is the worker's Reading timer.
func ReadingPathNextDue(ctx context.Context, db *sql.DB) time.Duration {
	return readingPathNextDue(ctx, db)
}
