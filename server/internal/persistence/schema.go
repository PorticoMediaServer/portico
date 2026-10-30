package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The pre-release database has one directly edited baseline. SchemaVersion is
// a diagnostic format marker, not a migration number; the digest identifies SQL.
const schemaVersion = 1
const schemaVersionKey = "schema_version"
const baselineDigestKey = "schema_baseline_digest"

var ErrSchemaResetRequired = errors.New("This database does not match this pre-release Portico baseline. Stop Portico, move aside or reset its state directory, then start Portico again.")

// Open returns a handle to the state database. A database already at this
// binary's schema version is opened and returned without a single DDL statement.
func Open(path string) (*sql.DB, error) {
	db, handled, err := openFromTemplate(path)
	if !handled {
		db, err = OpenFresh(path)
	}
	if err != nil {
		return nil, err
	}
	if err = dropEmptyTableStatistics(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// OpenFresh opens a matching database or installs the baseline into an empty one, ignoring any
// schema template this process has cached. Tests that exercise the installers
// use it directly.
func OpenFresh(path string) (*sql.DB, error) {
	// The validated resource policy owns the pool size and every per-connection
	// pragma, and it carries them in the DSN so each pooled connection inherits
	// them. Reads run concurrently on a bounded set of WAL readers; write
	// ordering is the write gate's job, not the pool's. See internal/dbwork.
	db, err := dbwork.OpenHandle(path, dbwork.DefaultPolicy())
	if err != nil {
		return nil, err
	}
	if err = installBaseline(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	if err = dbwork.InstallWakeDependencies(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// baselinePlan is the whole schema in one transaction: an empty database is
// either fully installed or untouched.
var baselinePlan = dbwork.WritePlan{Name: "install-baseline", Why: "a fresh database installs its whole schema atomically before the server accepts any request"}

// installBaseline performs DDL only for an empty database. Existing databases
// must name this exact baseline; they are never upgraded, copied or reset here.
func installBaseline(ctx context.Context, db *sql.DB) error {
	gated, err := dbwork.Begin(dbwork.WithPlan(ctx, baselinePlan), db, dbwork.ClassMaintenance)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	var objects int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil {
		return err
	}
	if objects != 0 {
		return verifyBaseline(ctx, tx)
	}
	if _, err = tx.ExecContext(ctx, baselineSQL); err != nil {
		return fmt.Errorf("install baseline: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES(?,?),(?,?)`, schemaVersionKey, "1", baselineDigestKey, BaselineDigest()); err != nil {
		return err
	}
	return gated.Commit()
}

type baselineReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func verifyBaseline(ctx context.Context, db baselineReader) error {
	var configuration int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='configuration'`).Scan(&configuration); err != nil {
		return err
	}
	if configuration != 1 {
		return ErrSchemaResetRequired
	}
	var version, digest string
	if err := db.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key=?`, schemaVersionKey).Scan(&version); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaResetRequired, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key=?`, baselineDigestKey).Scan(&digest); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaResetRequired, err)
	}
	if version != "1" || digest != BaselineDigest() {
		return ErrSchemaResetRequired
	}
	return nil
}

// SchemaVersion reports the database format marker for backup diagnostics.
func SchemaVersion() int { return schemaVersion }
