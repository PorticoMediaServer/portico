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

// ShareListForTest shares a list with a profile, to view or to edit, until sharing has its own route.
func (d *DB) ShareListForTest(ctx context.Context, list string, profile int64, canEdit bool) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO list_shares (list_id, profile_id, can_edit, created_at) SELECT id, ?, ?, 0 FROM lists WHERE public_id = ?`,
			profile, canEdit, list)
		return err
	})
}
