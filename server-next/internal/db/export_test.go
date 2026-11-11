package db

import (
	"context"
	"database/sql"
)

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// ExecForTest runs one statement, for a test to put the database in a state that no code on
// this branch can yet (what the metadata service will set).
func (d *DB) ExecForTest(ctx context.Context, query string, args ...any) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	})
}
