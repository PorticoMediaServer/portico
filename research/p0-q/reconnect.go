package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func forceReconnectControl(db *sql.DB) {
	connectionState(db, "before-forced-cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	start := time.Now()
	rows, e := db.QueryContext(ctx, `WITH RECURSIVE c(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM c WHERE x<1000000) SELECT x FROM c ORDER BY random()`)
	if e == nil {
		for rows.Next() {
			var n int
			must(rows.Scan(&n))
		}
		e = rows.Err()
		rows.Close()
	}
	cancel()
	assertQ(e != nil, "forced read deadline exercised")
	emit("forced_connection_cancel", map[string]any{"ms": float64(time.Since(start).Microseconds()) / 1000, "error": fmt.Sprint(e)})
	connectionState(db, "after-forced-cancel")
}
