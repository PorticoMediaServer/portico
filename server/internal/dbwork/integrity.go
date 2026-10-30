package dbwork

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Nothing ever checked the database's structural health. Corruption was
// discovered when a query happened to fail, at which point the watchdog reported
// CorruptState and correctly refused to repair anything — but by then the owner
// had been running on damaged pages for however long it took.
//
// Both checks here are read-only and both are the size of the database, so
// neither may ever run on the startup path: `quick_check` on a multi-gigabyte
// library is minutes of I/O. They run on a schedule the owner opts into, in the
// maintenance class, in a quiet period, and what they find is published rather
// than logged and forgotten.

// IntegrityCheckDeadline bounds one pass. A check that cannot finish in this
// long on this database is one the owner should be running deliberately, not one
// the server should keep attempting in the background.
const IntegrityCheckDeadline = 10 * time.Minute

// IntegrityReport is the result of one structural check.
type IntegrityReport struct {
	CheckedUnix    int64 `json:"checkedUnix"`
	DurationMillis int64 `json:"durationMillis"`
	// QuickCheck is "ok", or the first problems the database reported.
	QuickCheck string `json:"quickCheck,omitempty"`
	// ForeignKeyViolations counts orphaned child rows, and ByChildTable says
	// where they are — which is the difference between "something is wrong" and a
	// repair somebody can reason about.
	ForeignKeyViolations int            `json:"foreignKeyViolations"`
	ByChildTable         map[string]int `json:"foreignKeyViolationsByTable,omitempty"`
	Error                string         `json:"error,omitempty"`
}

// Integrity runs quick_check and foreign_key_check. Neither writes anything.
func Integrity(ctx context.Context, db *sql.DB) IntegrityReport {
	out := IntegrityReport{CheckedUnix: time.Now().Unix(), ByChildTable: map[string]int{}}
	if db == nil {
		out.Error = "no database"
		return out
	}
	started := time.Now()
	// A whole-database read: it takes the single owner-long permit and a
	// background connection, never a foreground reader a request needs.
	ctx, release, err := AcquireLongRead(ctx, LongReadIntegrity)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, IntegrityCheckDeadline)
	defer cancel()
	defer func() { out.DurationMillis = time.Since(started).Milliseconds() }()
	conn, err := ReadHandle(ctx, db).Conn(ctx)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer conn.Close()
	// quick_check is the cheaper of the two structural checks and finds the
	// damage that matters: it skips the index-content cross-check that makes
	// integrity_check so much slower.
	rows, err := conn.QueryContext(ctx, `PRAGMA quick_check(16)`)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	var problems []string
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			break
		}
		problems = append(problems, line)
	}
	rows.Close()
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.QuickCheck = strings.Join(problems, "; ")
	if len(problems) == 1 && problems[0] == "ok" {
		out.QuickCheck = "ok"
	}
	violations, err := ForeignKeyViolations(ctx, conn)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	for table, count := range violations {
		out.ByChildTable[table] = count
		out.ForeignKeyViolations += count
	}
	return out
}

// ForeignKeyViolations counts orphaned child rows per table. With enforcement on
// they should be impossible, so a non-zero answer means some path wrote with it
// off — the usual cause being a connection that left a helper unenforcing and
// went back to the pool.
func ForeignKeyViolations(ctx context.Context, conn *sql.Conn) (map[string]int, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		if err = rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return nil, err
		}
		out[table]++
	}
	return out, rows.Err()
}

// SortedTables is the stable order a report is read in.
func SortedTables(counts map[string]int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// The check runs on a schedule and the diagnostics endpoint reads it, so the
// result is published here rather than returned to a caller who would have to
// hold it somewhere. A result nobody can see is a check nobody benefits from.
var (
	lastIntegrity  atomic.Pointer[IntegrityReport]
	foreignObjects atomic.Int64
)

// PublishIntegrity records the most recent structural check.
func PublishIntegrity(report IntegrityReport) { lastIntegrity.Store(&report) }

// LastIntegrity returns the most recent check, zero-valued when none has run.
func LastIntegrity() IntegrityReport {
	if report := lastIntegrity.Load(); report != nil {
		return *report
	}
	return IntegrityReport{}
}

// PublishForeignSchemaObjects records how many schema objects the running build
// does not define. Non-zero means this state directory was written by a
// different or newer Portico, which is worth an owner knowing before they spend
// an afternoon on a behaviour this build cannot explain.
func PublishForeignSchemaObjects(count int) { foreignObjects.Store(int64(count)) }

// ForeignSchemaObjects returns that count.
func ForeignSchemaObjects() int { return int(foreignObjects.Load()) }
