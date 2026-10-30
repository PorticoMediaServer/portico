package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/reportwindow"
)

// readDashboardPeopleTotal counts accounts in this pane's exact period and
// filters. It joins the caller's read transaction, so the first page's total
// cannot observe a different state from its items or the other report panels.
// A cursor is not a total; subsequent pages omit this first-page fact.
func readDashboardPeopleTotal(ctx context.Context, tx *sql.Tx, q PlayHistoryQuery, r reportwindow.Interval, seesRemoved bool) (*int64, error) {
	if q.Cursor != "" {
		return nil, nil
	}
	filter, args, err := historyFilter(q, r, seesRemoved)
	if err != nil {
		return nil, err
	}
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT count(DISTINCT h.account_id) FROM play_history h`+filter, args...).Scan(&total); err != nil {
		return nil, err
	}
	return &total, nil
}
