package db

import (
	"context"
	"database/sql"
)

// JobVisibleCounts describes the original outcomes still visible to the current reader.
type JobVisibleCounts struct{ Done, Added, Already int64 }

func (d *DB) VisibleJobCounts(ctx context.Context, v Viewer, jobID int64) (JobVisibleCounts, error) {
	var out JobVisibleCounts
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	a, err := loadAccess(ctx, tx, v)
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT ifnull(sum(o.done), 0), ifnull(sum(o.added), 0), ifnull(sum(o.already), 0)
		FROM job_item_outcomes o CROSS JOIN items i ON i.id = o.item_id WHERE o.job_id = @job AND `+a.visible("i"),
		append(v.args(), sql.Named("job", jobID))...).Scan(&out.Done, &out.Added, &out.Already)
	return out, err
}

// listJobAddition records actual expanded children, atomically with their insertion.
func listJobAddition(tx *sql.Tx, jobID, itemID int64, added bool) error {
	if jobID == 0 {
		return nil
	}
	a, held := 0, 1
	if added {
		a, held = 1, 0
	}
	_, err := tx.Exec(`INSERT INTO job_item_outcomes (job_id, item_id, added, already) VALUES (?, ?, ?, ?)
		ON CONFLICT (job_id, item_id) DO UPDATE SET added = added + excluded.added, already = already + excluded.already`, jobID, itemID, a, held)
	return err
}
