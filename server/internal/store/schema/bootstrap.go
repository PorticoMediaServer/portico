package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrNotEmpty            = errors.New("schema: bootstrap requires an empty database")
	ErrForeignKeysDisabled = errors.New("schema: bootstrap requires foreign_keys=ON")
)

// InitializeEmpty installs the complete DDL atomically on a caller-owned,
// exclusive connection before runtime writers or readers start. The caller
// configures the bundled SQLite driver, WAL and resource/durability policy.
// This is a fresh-file bootstrap, never an upgrade, seed or accepting command.
// Existing databases are refused, even when their schema matches this version.
func InitializeEmpty(ctx context.Context, conn *sql.Conn) error {
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("schema: check foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		return ErrForeignKeysDisabled
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("schema: begin bootstrap: %w", err)
	}
	defer tx.Rollback()
	var objects int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&objects); err != nil {
		return fmt.Errorf("schema: inspect empty database: %w", err)
	}
	if objects != 0 {
		return ErrNotEmpty
	}
	if _, err := tx.ExecContext(ctx, SQL()); err != nil {
		return fmt.Errorf("schema: install %s: %w", SHA256, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("schema: commit bootstrap: %w", err)
	}
	return nil
}
