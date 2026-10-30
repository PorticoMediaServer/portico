package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
)

// Foreign-key enforcement is a per-connection setting, and this pool hands
// connections out and takes them back. A connection that leaves a helper with
// enforcement still off is a silent corruption vector for every later user of
// that connection: rows written through it can orphan children of tables
// declared ON DELETE CASCADE, which by definition should be impossible. A copy
// of the development state carries 2,419 such violations.
//
// Two rules follow, and this is where they are enforced rather than remembered:
// enforcement is only ever disabled on a pinned *sql.Conn, and if it cannot be
// restored the connection is destroyed instead of being returned to the pool.

// ErrForeignKeysNotRestored means a connection could not be returned to
// enforcing foreign keys, so it was discarded rather than reused.
var ErrForeignKeysNotRestored = errors.New("dbwork: foreign key enforcement could not be restored")

// WithForeignKeysOff runs fn on conn with enforcement disabled and restores it
// afterwards, on every path including a panic. Nothing else in the tree should
// write `PRAGMA foreign_keys=OFF`; a test fails the build when it does.
func WithForeignKeysOff(ctx context.Context, conn *sql.Conn, fn func() error) (err error) {
	if conn == nil {
		return errors.New("dbwork: nil connection")
	}
	var before int
	if err = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&before); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	var disabled int
	if err = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&disabled); err != nil || disabled != 0 {
		// It did not take. Restoring is still the right thing to do, and the
		// caller must not proceed believing enforcement is off.
		restoreForeignKeys(ctx, conn, before)
		if err == nil {
			err = errors.New("dbwork: foreign key enforcement could not be disabled")
		}
		return err
	}
	defer func() {
		// Restoration runs whatever happened inside fn, including a panic on its
		// way out. A connection that leaves here unenforcing is the bug.
		if restoreErr := restoreForeignKeys(ctx, conn, before); restoreErr != nil && err == nil {
			err = restoreErr
		}
	}()
	return fn()
}

// RestoreForeignKeys puts enforcement back on a pinned connection, destroying it
// rather than pooling it if it cannot. Use it in a defer beside a manual
// `PRAGMA foreign_keys=OFF` that WithForeignKeysOff cannot wrap.
func RestoreForeignKeys(ctx context.Context, conn *sql.Conn) error {
	return restoreForeignKeys(ctx, conn, 1)
}

// restoreForeignKeys puts enforcement back, and destroys the connection if it
// cannot. Returning a connection to the pool in this state would be worse than
// losing it: the pool opens another in its place, and nothing is lost but a
// handle.
func restoreForeignKeys(ctx context.Context, conn *sql.Conn, previous int) error {
	statement := `PRAGMA foreign_keys=ON`
	if previous == 0 {
		statement = `PRAGMA foreign_keys=OFF`
	}
	if _, err := conn.ExecContext(ctx, statement); err == nil {
		var current int
		if err = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&current); err == nil && current == previous {
			return nil
		}
	}
	// driver.ErrBadConn from inside Raw is how database/sql is told to discard a
	// connection rather than pool it.
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	return ErrForeignKeysNotRestored
}
