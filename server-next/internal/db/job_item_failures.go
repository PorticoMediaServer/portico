package db

import (
	"context"
	"database/sql"
)

// JobItemFailure is one failed title, read only while the viewer may see it.
type JobItemFailure struct {
	ItemID   int64
	PublicID string
	Title    string
	Code     string
	Message  string
}

// JobItemFailures reads a page in item-id order. Visibility is applied before the limit,
// including for an owner or administrator reading another profile's job.
func (d *DB) JobItemFailures(ctx context.Context, v Viewer, jobID, after int64, limit int) ([]JobItemFailure, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	a, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id, i.public_id, i.title, f.code, f.message
		FROM job_item_outcomes f CROSS JOIN items i ON i.id = f.item_id
		WHERE f.job_id = @job AND f.code <> '' AND f.item_id > @after AND `+a.visible("i")+` ORDER BY f.item_id LIMIT @limit`,
		append(v.args(), sql.Named("job", jobID), sql.Named("after", after), sql.Named("limit", limit))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobItemFailure
	for rows.Next() {
		var f JobItemFailure
		if err := rows.Scan(&f.ItemID, &f.PublicID, &f.Title, &f.Code, &f.Message); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// JobHasItemFailures lets the summary say something failed without revealing a count.
func (d *DB) JobHasItemFailures(ctx context.Context, jobID int64) (bool, error) {
	var failed bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM job_item_outcomes WHERE job_id = ? AND code <> '')`, jobID).Scan(&failed)
	return failed, err
}

// jobItemOutcome is committed with the title's change. Only fixed messages belong here:
// errors may contain paths, provider replies or names the reader must never be given.
// A missing item stores nothing; a successful replay clears an earlier refusal.
func jobItemOutcome(tx *sql.Tx, jobID int64, itemID, code string) error {
	var message string
	switch code {
	case "":
	case "unsupported_state":
		message = "Music has play counts, not a watched state."
	case "edit_too_old":
		message = "This change is more than 35 days old. Make the change again."
	case "too_many_titles":
		message = "This title has too many playable titles to add at once. Add a smaller selection."
	case "invalid_metadata":
		message = "An edited metadata value is not allowed for this title. Check the fields and try again."
	case "metadata_list_full":
		message = "This edit would put too many entries in a metadata list. Remove entries and try again."
	case "metadata_changed":
		message = "This title changed while it was being saved. Try again."
	case "provider_rejected":
		message = "The metadata provider could not refresh this title. Try again later."
	case "metadata_file_unavailable":
		message = "This title's metadata could not be read from its files. Check that the files are available and try again."
	default:
		return invalid("unknown job failure code")
	}
	_, err := tx.Exec(`INSERT INTO job_item_outcomes (job_id, item_id, done, code, message)
		SELECT ?, id, 1, ?, ? FROM items WHERE public_id = ?
		ON CONFLICT (job_id, item_id) DO UPDATE SET done = 1, code = excluded.code, message = excluded.message`, jobID, code, message, itemID)
	return err
}
