package compactcatalog

import (
	"context"
	"database/sql"
	"time"
)

// TitleBacklog is how many titles may wait for their browse row or search
// document while a scan finds more. A scan runs faster than titles are
// derived, and the derived worker yields the write gate to it, so without a
// bound a library built from empty became browsable only after its whole
// scan; with it, titles appear within seconds of being found.
const TitleBacklog = 2000

// titleWaitPoll is how often a waiting scan looks at the backlog again.
const titleWaitPoll = 50 * time.Millisecond

// TitlesBehind reports whether more than TitleBacklog titles wait for their
// browse row or their search document (two bounded index reads).
func TitlesBehind(ctx context.Context, db *sql.DB) (bool, error) {
	var behind bool
	err := db.QueryRowContext(ctx, `SELECT (SELECT entity_id FROM catalog_dirty WHERE domain=?1 ORDER BY entity_id LIMIT 1 OFFSET ?3) IS NOT NULL
 OR (SELECT entity_id FROM catalog_dirty WHERE domain=?2 ORDER BY entity_id LIMIT 1 OFFSET ?3) IS NOT NULL`, DomainBrowseRows, DomainSearch, TitleBacklog).Scan(&behind)
	return behind, err
}

// AwaitTitles waits, at most limit, until the title backlog is back within
// TitleBacklog. It never waits on a backlog it can't read, and a stalled
// worker holds a scan back by limit per step at most. It reports false only
// when ctx ended.
func AwaitTitles(ctx context.Context, db *sql.DB, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		behind, err := TitlesBehind(ctx, db)
		if err != nil || !behind || !time.Now().Before(deadline) {
			return ctx.Err() == nil
		}
		timer := time.NewTimer(titleWaitPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}
