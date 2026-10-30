package dbwork

import "database/sql"

// PlannerStatisticsChanged retires pooled connections holding old SQLite
// statistics, including their cached statement plans, on their next checkout.
// Call only after the statistics transaction commits. In-use readers are never
// interrupted, and new connections load the committed statistics normally.
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
