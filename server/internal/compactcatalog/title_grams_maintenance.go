package compactcatalog

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const titleGramsMergeInterval = 30 * time.Second

// Explicit native-FTS exception (O022): SQLite checks the incremental leaf
// target only between terms and may also retire completed input segments. One
// long term's posting list and tombstone/segment cleanup can exceed four pages.
// Neither this target nor the observed cache writes is a source frame bound.
// All ordinary relational purge units retain their independent 24/128 limits.
var titleGramsMergePlan = dbwork.WritePlan{
	Name: "catalog-title-grams-merge",
	Why:  "approved native FTS5 primitive: merge4 is an incremental leaf-page target; a complete posting term and native completed-segment cleanup may exceed it; no relational/frame ceiling claimed",
}

func maintainTitleGrams(ctx context.Context, db *sql.DB) error {
	if !dbwork.WaitBackground(ctx) || !dbwork.Yield(ctx) {
		return ctx.Err()
	}
	_, e := dbwork.ExecWrite(dbwork.WithPlan(ctx, titleGramsMergePlan), db, dbwork.ClassMaintenance, `INSERT INTO catalog_title_grams(catalog_title_grams,rank) VALUES('merge',4)`)
	return e
}
