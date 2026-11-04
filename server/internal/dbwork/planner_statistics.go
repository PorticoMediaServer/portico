package dbwork

import "database/sql"

// PlannerStatisticsChanged makes every pooled connection reload the committed
// SQLite statistics, and drop its cached statement plans, on its next
// checkout (observedConn.ResetSession). Call only after the statistics
// transaction commits. In-use readers are never interrupted, and no
// connection is reopened.
func PlannerStatisticsChanged(db *sql.DB) {
	if db == nil {
		return
	}
	if split, ok := db.Driver().(*splitDriver); ok {
		// The scope is shared by foreground, writer and background pools for
		// this file. Publishing needs no connection, gate or uncancelled SQL.
		scope := acquireReaderScope(split.dsn)
		scope.plannerGeneration.Add(1)
		releaseReaderScope(scope)
	}
}
