package db

import (
	"context"
	"database/sql"
	"time"
)

// The owner's list of tasks keeps the jobs that ended for 30 days, with their reason; the
// daily tidy-up removes them after that.

// EndedJobs lists the jobs that ended (done, failed or cancelled) at or after since, the
// latest end first. A page after the first starts below the last one of the page before,
// given by its end time and id.
func (d *DB) EndedJobs(ctx context.Context, since time.Time, afterAt, afterID int64, limit int) ([]Job, error) {
	where, args := `state IN ('done', 'failed', 'cancelled') AND finished_at >= ?`, []any{since.UnixMilli()}
	if afterID > 0 {
		where, args = where+` AND (finished_at, id) < (?, ?)`, append(args, afterAt, afterID)
	}
	rows, err := d.read.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE `+where+` ORDER BY finished_at DESC, id DESC LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(job.dest()...); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// RemoveEndedJobs removes the jobs that ended before a time, a page of short background
// writes at a time, and says how many went.
func (d *DB) RemoveEndedJobs(ctx context.Context, before time.Time) (int, error) {
	removed := 0
	for {
		var n int64
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			res, err := tx.Exec(`DELETE FROM jobs WHERE id IN (SELECT id FROM jobs WHERE state IN ('done', 'failed', 'cancelled')
				AND finished_at < ? LIMIT 500)`, before.UnixMilli())
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		removed += int(n)
		if err != nil || n < 500 {
			return removed, err
		}
	}
}
