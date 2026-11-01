package compactcatalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// RatingClassificationBatch bounds both new and corrected spelling writes.
// Each spelling also advances policy epochs and queues deferred visibility work.
const RatingClassificationBatch = 8

// ClassifyPendingRatings publishes the minimum age of up to eight content-rating
// spellings a trigger queued in content_rating_pending (a rating arrived that
// content_rating_ages does not classify), in one gated write, and reports
// whether more are queued. An unrecognised spelling is classified as unrated
// (-1) rather than silently treated as suitable. Until its spelling is
// classified, visibility treats a title as unrated. Classification is
// idempotent, so overlapping callers only repeat work. age classifies one
// spelling (identity.RatingAge); it is passed in so this package does not
// depend on identity.
func ClassifyPendingRatings(ctx context.Context, db *sql.DB, age func(string) (int, bool)) (bool, error) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	type entry struct {
		key string
		age int
	}
	read := dbwork.ReadHandle(ctx, db)
	rows, err := read.QueryContext(ctx, `SELECT value_key,value FROM content_rating_pending ORDER BY value_key LIMIT ?`, RatingClassificationBatch)
	if err != nil {
		return false, err
	}
	var pending []entry
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			rows.Close()
			return false, err
		}
		minimum := -1
		if resolved, ok := age(value); ok {
			minimum = resolved
		}
		pending = append(pending, entry{key, minimum})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(pending) > 0 {
		if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			for _, row := range pending {
				if _, err := tx.ExecContext(ctx, `INSERT INTO content_rating_ages(value_key,minimum_age) VALUES(?,?) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age WHERE minimum_age IS NOT excluded.minimum_age`, row.key, row.age); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM content_rating_pending WHERE value_key=?`, row.key); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return false, err
		}
	}
	var more bool
	err = read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM content_rating_pending)`).Scan(&more)
	return more, err
}
