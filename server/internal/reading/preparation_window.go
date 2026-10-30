package reading

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// preparationWindow bounds raw entity visits before publication, availability,
// format or determination filters. An entirely ineligible window still advances
// the raw frontier; no query searches the rest of the catalogue for a match.
// It releases its rows before the eligibility query or any preparation begins.
const preparationWindowSQL = `SELECT id FROM catalog_entities WHERE id>? ORDER BY id LIMIT ?`

func preparationWindow(ctx context.Context, db *sql.DB, after int64, limit int) (last int64, count int, err error) {
	if !dbwork.PaceBackground(ctx, time.Time{}) {
		return after, 0, ctx.Err()
	}
	rows, err := dbwork.ReadHandle(ctx, db).QueryContext(ctx, preparationWindowSQL, after, limit)
	if err != nil {
		return after, 0, err
	}
	defer rows.Close()
	last = after
	for rows.Next() {
		if err = rows.Scan(&last); err != nil {
			return after, 0, err
		}
		count++
	}
	return last, count, rows.Err()
}
