package persistence

import (
	"context"
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Two things this build can be wrong about, and one it can safely fix.
//
// A state directory may hold schema objects this build knows nothing about — a
// database written by a different or newer Portico. Those are never touched
// automatically: a build that does not define a table cannot know what dropping
// it would cost. They are counted and reported, which is what turns "this
// database behaves strangely" into a fact.
//
// And a database may hold orphaned child rows, which with enforcement on should
// be impossible. Only one class of them is safe to repair without a judgement
// call: a child declared ON DELETE CASCADE is a row whose whole contract is that
// it disappears with its parent, so deleting one whose parent is already gone
// restores exactly the state the declaration promised. Everything else is
// reported and left alone.

// orphanRepairBatch is how many rows one transaction removes. Small enough that
// a playback control write wins the gate between batches.
const orphanRepairBatch = 500

// UnknownSchemaObjects lists schema objects present in the database that this
// build does not define.
func UnknownSchemaObjects(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := dbwork.Query(ctx, db, `SELECT type,name FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := KnownSchemaObjects()
	var unknown []string
	for rows.Next() {
		var kind, name string
		if err = rows.Scan(&kind, &name); err != nil {
			return nil, err
		}
		if !known[kind+" "+name] {
			unknown = append(unknown, kind+" "+name)
		}
	}
	return unknown, rows.Err()
}

// OrphanRepairReport says what a repair pass did and what it deliberately left.
type OrphanRepairReport struct {
	Removed map[string]int `json:"removedByTable"`
	// Reported are violations in children that are not declared ON DELETE
	// CASCADE. Deleting one of those is a judgement about data the schema does not
	// make for us, so it is the owner's call, not the server's.
	Reported map[string]int `json:"reportedByTable"`
	Duration int64          `json:"durationMillis"`
}

// RepairCascadeOrphans deletes orphaned rows from children declared ON DELETE
// CASCADE, in batches, yielding to foreground work between them. It is
// owner-triggered: nothing here runs on a timer, because a repair that happens
// without anybody asking is a repair nobody reviews.
func RepairCascadeOrphans(ctx context.Context, db *sql.DB) (OrphanRepairReport, error) {
	started := time.Now()
	out := OrphanRepairReport{Removed: map[string]int{}, Reported: map[string]int{}}
	if db == nil {
		return out, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	orphans, err := cascadeOrphans(ctx, conn)
	if err != nil {
		return out, err
	}
	conn.Close()
	for _, table := range sortedKeys(orphans.cascading) {
		ids := orphans.cascading[table]
		for start := 0; start < len(ids); start += orphanRepairBatch {
			if !dbwork.Yield(ctx) {
				out.Duration = time.Since(started).Milliseconds()
				return out, ctx.Err()
			}
			end := min(start+orphanRepairBatch, len(ids))
			batch := ids[start:end]
			args := make([]any, 0, len(batch))
			marks := make([]string, 0, len(batch))
			for _, id := range batch {
				args = append(args, id)
				marks = append(marks, "?")
			}
			// The identifier comes from sqlite_schema, never from a caller.
			statement := `DELETE FROM "` + strings.ReplaceAll(table, `"`, `""`) + `" WHERE rowid IN (` + strings.Join(marks, ",") + `)`
			if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance, statement, args...); err != nil {
				out.Duration = time.Since(started).Milliseconds()
				return out, err
			}
			out.Removed[table] += len(batch)
		}
	}
	for table, count := range orphans.other {
		out.Reported[table] = count
	}
	out.Duration = time.Since(started).Milliseconds()
	return out, nil
}

type orphanSet struct {
	cascading map[string][]int64
	other     map[string]int
}

// cascadeOrphans separates violations that are safe to delete from violations
// that are somebody's decision.
func cascadeOrphans(ctx context.Context, conn *sql.Conn) (orphanSet, error) {
	out := orphanSet{cascading: map[string][]int64{}, other: map[string]int{}}
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return out, err
	}
	type violation struct {
		table string
		rowid sql.NullInt64
		fk    int
	}
	var found []violation
	for rows.Next() {
		var v violation
		var parent string
		if err = rows.Scan(&v.table, &v.rowid, &parent, &v.fk); err != nil {
			rows.Close()
			return out, err
		}
		found = append(found, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	cascade := map[string]map[int]bool{}
	for _, v := range found {
		if _, read := cascade[v.table]; !read {
			cascade[v.table], err = cascadingKeys(ctx, conn, v.table)
			if err != nil {
				return out, err
			}
		}
		// A WITHOUT ROWID table has no rowid to delete by; report it instead of
		// guessing at a key.
		if !v.rowid.Valid || !cascade[v.table][v.fk] {
			out.other[v.table]++
			continue
		}
		out.cascading[v.table] = append(out.cascading[v.table], v.rowid.Int64)
	}
	return out, nil
}

// cascadingKeys reports which of a table's foreign keys are declared ON DELETE
// CASCADE, by index, which is what foreign_key_check reports against.
func cascadingKeys(ctx context.Context, conn *sql.Conn, table string) (map[int]bool, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_list("`+strings.ReplaceAll(table, `"`, `""`)+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		values := make([]any, len(columns))
		holders := make([]sql.RawBytes, len(columns))
		for index := range values {
			values[index] = &holders[index]
		}
		if err = rows.Scan(values...); err != nil {
			return nil, err
		}
		id, onDelete := -1, ""
		for index, name := range columns {
			switch name {
			case "id":
				id, _ = strconv.Atoi(string(holders[index]))
			case "on_delete":
				onDelete = string(holders[index])
			}
		}
		if id >= 0 && strings.EqualFold(onDelete, "CASCADE") {
			out[id] = true
		}
	}
	return out, rows.Err()
}

func sortedKeys(m map[string][]int64) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
