package reportwindow

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// FirstFact is for the server-owner report scope only. Its callers enforce
// live owner/main-profile authority; it reads no private personal-history or
// other server's facts. Panel filters do not change the common interval.
func FirstFact(ctx context.Context, db *sql.DB, until int64) (int64, error) {
	var first sql.NullInt64
	err := dbwork.QueryRow(ctx, db, `SELECT min(event_ms) FROM (
 SELECT min(started_ms) event_ms FROM play_history WHERE started_ms<?
 UNION ALL
 SELECT min(first_event_ms) FROM telemetry_rollups WHERE bucket_ms=(
  SELECT min(bucket_ms) FROM telemetry_rollups WHERE bucket_ms<? AND first_event_ms<?
 ) AND first_event_ms<?
)`, until, until, until, until).Scan(&first)
	if err != nil || !first.Valid {
		return -1, err
	}
	return first.Int64, nil
}
