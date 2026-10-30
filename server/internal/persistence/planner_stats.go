package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Planner statistics without holding the write gate across a table.
//
// `PRAGMA optimize` (and ANALYZE) is a write transaction whose length is the
// size of the table: for a table with no sqlite_stat1 rows it counts the whole
// b-tree. Run on the startup path it held the write gate for minutes on a
// million-song library, and every sign-in queued behind it (BE-member, 23 Sep:
// POST /v1/sessions answered 503 for 486 s after each start).
//
// This pass does what ANALYZE does, but splits the work the way the write gate
// needs: every read — the row count and a bounded sample of each index's key
// columns — runs on a background read connection with no gate held, and the
// only write is a handful of sqlite_stat1 rows per table, a transaction of
// milliseconds. The rows it writes are the ones ANALYZE would: "nRow a1 … ak"
// per index, where ai estimates how many rows share one value of the first i
// key columns.

// plannerReadStep runs before each table-sized read. Tests use it to prove the
// write gate is free while those reads run.
var plannerReadStep = func() {}

// PlannerStatsSample bounds the rows read from each index's key columns.
const PlannerStatsSample = 20000

// RefreshPlannerStatistics brings sqlite_stat1 up to date for every ordinary
// table whose statistics are missing or off by 10× (the rule PRAGMA optimize
// uses), then reloads them into this process's planner. It stops at ctx.
func RefreshPlannerStatistics(ctx context.Context, db *sql.DB) error {
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	if err := ensurePlannerStatTable(ctx, db); err != nil {
		return err
	}
	read := dbwork.ReadHandle(dbwork.WithClass(ctx, dbwork.ClassMaintenance), db)
	tables, err := plannerTables(ctx, read)
	if err != nil {
		return err
	}
	changed := false
	defer func() {
		if changed {
			// Earlier tables may already have committed even if a later read
			// is cancelled. Never leave pooled plans on those old statistics.
			dbwork.PlannerStatisticsChanged(db)
		}
	}()
	for _, table := range tables {
		if !dbwork.PaceBackground(ctx, time.Time{}) {
			return ctx.Err()
		}
		rows, stale, err := plannerTableStats(ctx, read, table)
		if err != nil {
			return fmt.Errorf("planner statistics for %s: %w", table, err)
		}
		if !stale {
			continue
		}
		if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sqlite_stat1 WHERE tbl=?`, table); err != nil {
				return err
			}
			for _, row := range rows {
				var idx any
				if row.index != "" {
					idx = row.index
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO sqlite_stat1(tbl,idx,stat) VALUES(?,?,?)`, table, idx, row.stat); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	// Loading the statistics into the planner reads sqlite_stat1 only.
	return dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ANALYZE sqlite_schema`)
		return err
	})
}

// dropEmptyTableStatistics removes the sqlite_stat1 rows that record zero rows, when the
// database opens and before any connection has planned with them. Earlier versions of this
// pass stored "0 …" for a table that was empty at the time; once the table filled, every lookup
// on it was planned as a scan (on a 60,000-row guide table, 1.6 ms per rowid probe in place of
// microseconds, and this pass's own sampling of it ran for an hour on a one-core host).
func dropEmptyTableStatistics(ctx context.Context, db *sql.DB) error {
	var stale bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='sqlite_stat1')`).Scan(&stale); err != nil || !stale {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_stat1 WHERE stat='0' OR stat LIKE '0 %')`).Scan(&stale); err != nil || !stale {
		return err
	}
	return dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sqlite_stat1 WHERE stat='0' OR stat LIKE '0 %'`); err != nil {
			return err
		}
		// The connection that read the schema to get here has the old rows loaded: reload them.
		_, err := tx.ExecContext(ctx, `ANALYZE sqlite_schema`)
		return err
	})
}

type plannerStatRow struct{ index, stat string }

// ensurePlannerStatTable creates sqlite_stat1 the way `.dump` output does, so
// the first pass on a fresh database has somewhere to write. ANALYZE of the
// schema table reads only the schema.
func ensurePlannerStatTable(ctx context.Context, db *sql.DB) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='sqlite_stat1')`).Scan(&exists); err != nil || exists {
		return err
	}
	return dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ANALYZE sqlite_schema`)
		return err
	})
}

func plannerTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' AND sql NOT LIKE 'CREATE VIRTUAL TABLE%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// plannerIndex is one index as the statistics pass reads it: the SQL of each
// key column (a quoted column name, or the index's own expression text) and,
// for a partial index, its WHERE clause.
type plannerIndex struct {
	name  string
	terms []string
	where string
}

// plannerTableStats computes the sqlite_stat1 rows one table should have and
// reports whether any differ from what is stored by the rule PRAGMA optimize
// uses. Indexes over every row share the table's freshness; each partial index
// is judged on its own row count.
func plannerTableStats(ctx context.Context, db *sql.DB, table string) ([]plannerStatRow, bool, error) {
	indexes, err := plannerIndexes(ctx, db, table)
	if err != nil {
		return nil, false, err
	}
	stored := map[string]string{}
	storedRows, err := db.QueryContext(ctx, `SELECT COALESCE(idx,''),stat FROM sqlite_stat1 WHERE tbl=?`, table)
	if err != nil {
		return nil, false, err
	}
	for storedRows.Next() {
		var idx, stat string
		if err = storedRows.Scan(&idx, &stat); err != nil {
			storedRows.Close()
			return nil, false, err
		}
		stored[idx] = stat
	}
	if err = storedRows.Close(); err != nil {
		return nil, false, err
	}
	recordedRows := func(key string) int64 {
		stat, ok := stored[key]
		if !ok {
			return -1
		}
		first, _, _ := strings.Cut(stat, " ")
		n, err := strconv.ParseInt(first, 10, 64)
		if err != nil {
			return -1
		}
		return n
	}
	var withoutRowid bool
	if err = db.QueryRowContext(ctx, `SELECT wr FROM pragma_table_list WHERE schema='main' AND name=?`, table).Scan(&withoutRowid); err != nil {
		return nil, false, err
	}
	rowid := !withoutRowid

	full := []plannerIndex{}
	partial := []plannerIndex{}
	for _, index := range indexes {
		if index.where == "" {
			full = append(full, index)
		} else {
			partial = append(partial, index)
		}
	}
	// The table's recorded row count: every full index's row agrees on it.
	keys := []string{}
	for _, index := range full {
		keys = append(keys, index.name)
	}
	if len(indexes) == 0 {
		keys = []string{""}
	}
	var recorded int64 = -1
	for n, key := range keys {
		r := recordedRows(key)
		if r < 0 || (n > 0 && r != recorded) {
			recorded = -1
			break
		}
		recorded = r
	}
	out := []plannerStatRow{}
	changed := false
	if len(keys) > 0 {
		if recorded == 0 {
			// A stored zero is from before empty tables were left without rows: count again.
			recorded = -1
		}
		count, stale, err := plannerRowCount(ctx, db, table, "", rowid, recorded)
		if err != nil {
			return nil, false, err
		}
		switch {
		case !stale:
			for _, key := range keys {
				out = append(out, plannerStatRow{key, stored[key]})
			}
		case count == 0:
			// An empty table gets no statistics, as ANALYZE gives it none. A stored "0 rows"
			// outlives the emptiness: the planner then believes a scan of the table is free and
			// walks it for every rowid lookup, long after the table has filled.
			for _, key := range keys {
				if _, had := stored[key]; had {
					changed = true
				}
			}
		case len(indexes) == 0:
			out = append(out, plannerStatRow{"", strconv.FormatInt(count, 10)})
			changed = true
		default:
			for _, index := range full {
				row, err := plannerIndexRow(ctx, db, table, index, count, rowid)
				if err != nil {
					return nil, false, err
				}
				out = append(out, row)
			}
			changed = true
		}
	}
	for _, index := range partial {
		was := recordedRows(index.name)
		if was == 0 {
			was = -1
		}
		count, stale, err := plannerRowCount(ctx, db, table, index.where, rowid, was)
		if err != nil {
			return nil, false, err
		}
		if !stale {
			out = append(out, plannerStatRow{index.name, stored[index.name]})
			continue
		}
		if count == 0 {
			// No statistics for an index over no rows (see above).
			if _, had := stored[index.name]; had {
				changed = true
			}
			continue
		}
		row, err := plannerIndexRow(ctx, db, table, index, count, rowid)
		if err != nil {
			return nil, false, err
		}
		out = append(out, row)
		changed = true
	}
	return out, changed, nil
}

func plannerIndexRow(ctx context.Context, db *sql.DB, table string, index plannerIndex, count int64, rowid bool) (plannerStatRow, error) {
	averages, err := plannerAverages(ctx, db, table, index, count, rowid)
	if err != nil {
		return plannerStatRow{}, err
	}
	parts := []string{strconv.FormatInt(count, 10)}
	for _, a := range averages {
		parts = append(parts, strconv.FormatInt(a, 10))
	}
	return plannerStatRow{index.name, strings.Join(parts, " ")}, nil
}

// plannerFreshScan bounds how many rows the freshness check counts when it
// cannot use the rowid span.
const plannerFreshScan = 1_000_000

// plannerRowCount decides whether statistics recorded for recorded rows (< 0:
// none) are stale by the PRAGMA optimize rule — missing, or 10× away from the
// rows now there — and returns the exact count when they are. where narrows the
// rows to a partial index's. Rows that already have statistics are checked
// without counting them all: a whole rowid table by its rowid span, which
// bounds the count from above, and anything else by a count that stops at 10×
// the recorded rows or at plannerFreshScan, whichever is first. Only rows that
// may really be stale are counted in full.
func plannerRowCount(ctx context.Context, db *sql.DB, table, where string, rowid bool, recorded int64) (int64, bool, error) {
	filter := ""
	if where != "" {
		filter = ` WHERE ` + where
	}
	exact := func() (int64, error) {
		plannerReadStep()
		var count int64
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+quoteIdent(table)+filter).Scan(&count)
		return count, err
	}
	staleAt := func(count int64) bool {
		if count == 0 && recorded == 0 {
			return false
		}
		return recorded == 0 || count >= 10*recorded || count*10 <= recorded
	}
	if recorded < 0 {
		count, err := exact()
		return count, true, err
	}
	if rowid && where == "" {
		var span int64
		if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT max(rowid) FROM `+quoteIdent(table)+`)-(SELECT min(rowid) FROM `+quoteIdent(table)+`)+1,0)`).Scan(&span); err != nil {
			return 0, false, err
		}
		if span < 10*recorded && span*10 > recorded {
			// Fewer than 10× the recorded rows can exist; a shrink this bound
			// cannot see only makes the planner cautious.
			return 0, false, nil
		}
	} else {
		limit := 10 * recorded
		if limit < 1 {
			limit = 1
		}
		clamped := limit > plannerFreshScan
		if clamped {
			limit = plannerFreshScan
		}
		plannerReadStep()
		var seen int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM `+quoteIdent(table)+filter+` LIMIT ?)`, limit).Scan(&seen); err != nil {
			return 0, false, err
		}
		if seen < limit {
			return seen, staleAt(seen), nil
		}
		if clamped {
			return 0, false, nil
		}
	}
	count, err := exact()
	if err != nil {
		return 0, false, err
	}
	return count, staleAt(count), nil
}

// plannerIndexes lists the table's indexes with the SQL of each key column. An
// expression column's text and a partial index's WHERE clause come from the
// index's CREATE statement; an index whose statement cannot be read that way is
// left to SQLite's defaults.
func plannerIndexes(ctx context.Context, db *sql.DB, table string) ([]plannerIndex, error) {
	rows, err := db.QueryContext(ctx, `SELECT l.name,l.partial,COALESCE(s.sql,'') FROM pragma_index_list(?) l LEFT JOIN sqlite_schema s ON s.type='index' AND s.name=l.name ORDER BY l.name`, table)
	if err != nil {
		return nil, err
	}
	type listed struct {
		name, sql string
		partial   bool
	}
	all := []listed{}
	for rows.Next() {
		var entry listed
		if err = rows.Scan(&entry.name, &entry.partial, &entry.sql); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, entry)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	out := []plannerIndex{}
	for _, entry := range all {
		cols, err := db.QueryContext(ctx, `SELECT cid,COALESCE(name,'') FROM pragma_index_xinfo(?) WHERE key=1 ORDER BY seqno`, entry.name)
		if err != nil {
			return nil, err
		}
		index := plannerIndex{name: entry.name}
		expression := false
		for cols.Next() {
			var cid int
			var column string
			if err = cols.Scan(&cid, &column); err != nil {
				cols.Close()
				return nil, err
			}
			if cid == -2 || column == "" {
				expression = true
				column = ""
			} else {
				column = quoteIdent(column)
			}
			index.terms = append(index.terms, column)
		}
		if err = cols.Close(); err != nil {
			return nil, err
		}
		if len(index.terms) == 0 {
			continue
		}
		if expression || entry.partial {
			terms, where, ok := parseIndexDefinition(entry.sql)
			if !ok || len(terms) != len(index.terms) || entry.partial != (where != "") {
				continue
			}
			for i, term := range index.terms {
				if term == "" {
					index.terms[i] = terms[i]
				}
			}
			index.where = where
		}
		out = append(out, index)
	}
	return out, nil
}

// parseIndexDefinition splits `CREATE [UNIQUE] INDEX … ON t(term, …) [WHERE
// expr]` into its key terms, each without a trailing ASC or DESC, and the WHERE
// clause. Parentheses and quoted text inside terms are respected.
func parseIndexDefinition(statement string) ([]string, string, bool) {
	open := -1
	var quote byte
	for i := 0; i < len(statement) && open < 0; i++ {
		c := statement[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '[':
			quote = ']'
		case c == '(':
			open = i
		}
	}
	if open < 0 {
		return nil, "", false
	}
	terms := []string{}
	depth := 0
	start := open + 1
	quote = 0
	end := -1
	for i := open + 1; i < len(statement) && end < 0; i++ {
		c := statement[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '[':
			quote = ']'
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case c == ')':
			terms = append(terms, statement[start:i])
			end = i
		case c == ',' && depth == 0:
			terms = append(terms, statement[start:i])
			start = i + 1
		}
	}
	if end < 0 {
		return nil, "", false
	}
	for i, term := range terms {
		term = strings.TrimSpace(term)
		upper := strings.ToUpper(term)
		for _, suffix := range []string{" ASC", " DESC"} {
			if strings.HasSuffix(upper, suffix) {
				term = strings.TrimSpace(term[:len(term)-len(suffix)])
				break
			}
		}
		if term == "" {
			return nil, "", false
		}
		terms[i] = term
	}
	rest := strings.TrimSpace(statement[end+1:])
	where := ""
	if rest != "" {
		if len(rest) < 6 || !strings.EqualFold(rest[:5], "WHERE") {
			return nil, "", false
		}
		where = strings.TrimSpace(rest[5:])
	}
	return terms, where, true
}

// plannerAverages estimates, for each prefix of the index's key, how many of
// its count rows share one value: count / distinct. An index of at most
// PlannerStatsSample rows is read whole and the answer is exact. A larger one
// is sampled (see samplePlannerRows) and the distinct count is estimated with
// the Haas–Stokes Duj1 estimator, d / (1 - (1-q)·f1/n), where q is the sampled
// fraction and f1 the values seen once. It gives the full count for a key that
// never repeats in the sample and the sampled count for one that always does;
// on a random sample of a key shared by 12 rows it lands within a few percent.
func plannerAverages(ctx context.Context, db *sql.DB, table string, index plannerIndex, count int64, rowid bool) ([]int64, error) {
	out := make([]int64, len(index.terms))
	for i := range out {
		out[i] = 1
	}
	if count == 0 {
		return out, nil
	}
	frequencies := make([]map[string]int, len(index.terms))
	for i := range frequencies {
		frequencies[i] = map[string]int{}
	}
	sampled, err := samplePlannerRows(ctx, db, table, index, count, rowid, func(values []any) {
		var key strings.Builder
		for i, v := range values {
			fmt.Fprintf(&key, "%T:%v\x00", v, v)
			frequencies[i][key.String()]++
		}
	})
	if err != nil || sampled == 0 {
		return out, err
	}
	n := float64(sampled)
	q := math.Min(1, n/float64(count))
	for i, seen := range frequencies {
		singletons := 0
		for _, c := range seen {
			if c == 1 {
				singletons++
			}
		}
		denominator := 1 - (1-q)*float64(singletons)/n
		distinct := float64(count)
		if denominator > 0 {
			distinct = float64(len(seen)) / denominator
		}
		distinct = math.Max(1, math.Min(distinct, float64(count)))
		out[i] = int64(math.Max(1, math.Round(float64(count)/distinct)))
	}
	// A longer prefix never shares more rows than a shorter one.
	for i := 1; i < len(out); i++ {
		if out[i] > out[i-1] {
			out[i] = out[i-1]
		}
	}
	return out, nil
}

// samplePlannerRows hands the key values of at most PlannerStatsSample of the
// index's rows to visit and returns how many it handed over. An index of a
// rowid table larger than the sample is read at PlannerStatsSample random
// rowids, one indexed seek each, each row at most once: rows are usually
// written an album or a show at a time, so a run of neighbouring rows would see
// every key repeat and badly undercount them. A partial index keeps the sampled
// rows it covers, still a uniform sample of it. A WITHOUT ROWID table is read
// from its start, as SQLite's own analysis_limit does.
func samplePlannerRows(ctx context.Context, db *sql.DB, table string, index plannerIndex, count int64, rowid bool, visit func([]any)) (int, error) {
	values := make([]any, len(index.terms))
	pointers := make([]any, len(index.terms))
	for i := range values {
		pointers[i] = &values[i]
	}
	sampled := 0
	read := func(query string, args ...any) error {
		plannerReadStep()
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err = rows.Scan(pointers...); err != nil {
				return err
			}
			sampled++
			visit(values)
		}
		return rows.Err()
	}
	name := quoteIdent(table)
	selectList := `SELECT ` + strings.Join(index.terms, ",") + ` FROM ` + name
	filter := ""
	if index.where != "" {
		filter = `(` + index.where + `)`
	}
	if !rowid || count <= PlannerStatsSample {
		query := selectList
		if filter != "" {
			query += ` WHERE ` + filter
		}
		return sampled, read(query+` LIMIT ?`, PlannerStatsSample)
	}
	var low, high int64
	if err := db.QueryRowContext(ctx, plannerRowidBoundsSQL(table)).Scan(&low, &high); err != nil {
		return 0, err
	}
	points := make([]string, PlannerStatsSample)
	for i := range points {
		points[i] = strconv.FormatInt(low+rand.Int64N(high-low+1), 10)
	}
	query := selectList + ` WHERE rowid IN(SELECT (SELECT rowid FROM ` + name + ` WHERE rowid>=p.value ORDER BY rowid LIMIT 1) FROM json_each(?) p)`
	if filter != "" {
		query += ` AND ` + filter
	}
	return sampled, read(query, "["+strings.Join(points, ",")+"]")
}

// SQLite only optimizes a single min/max aggregate in each SELECT. Keeping
// the endpoints separate avoids scanning the table during each sampled index
// and during the supposedly constant-cost freshness check.
func plannerRowidBoundsSQL(table string) string {
	name := quoteIdent(table)
	return `SELECT (SELECT min(rowid) FROM ` + name + `),(SELECT max(rowid) FROM ` + name + `)`
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
