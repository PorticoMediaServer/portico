package plan_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// The oracle is a constructor of known grammar forms, not a second parser and
// not an import of the implementation. The label is fixed before SQL rendering.
// One input is resident at a time; no library/database scale fixture is built.
func TestSQLAllowListBoundedEnumeration(t *testing.T) {
	const seed int64 = 0x484e3305
	const cases = 144218
	random := rand.New(rand.NewSource(seed))
	columns := []string{"key", "id", "t.key", `"SELECT(,)"`, `[CASE]`, `"it""s)"`}
	literal := []string{"0", "1", "-2", "'x(,)'", "'it''s SELECT OVER(,)'"}
	pick := func(values []string) string { return values[random.Intn(len(values))] }
	var scalar func(int) string
	scalar = func(depth int) string {
		if depth == 0 || random.Intn(3) != 0 {
			switch random.Intn(3) {
			case 0:
				return pick(columns)
			case 1:
				return "?"
			default:
				return pick(literal)
			}
		}
		switch random.Intn(4) {
		case 0:
			return "abs(" + scalar(depth-1) + ")"
		case 1:
			return "coalesce(" + scalar(depth-1) + "," + scalar(depth-1) + ")"
		case 2:
			return "replace(" + scalar(depth-1) + ",'(',')')"
		default:
			return "like(? ," + scalar(depth-1) + ")"
		}
	}
	predicate := func() string {
		column := pick(columns)
		switch random.Intn(7) {
		case 0:
			return column + pick([]string{"=", "!=", "<>", ">", ">=", "<", "<="}) + scalar(3)
		case 1:
			return scalar(3) + "=" + scalar(3)
		case 2:
			return column + pick([]string{" IS NULL", " IS NOT NULL"})
		case 3:
			return column + " IN(" + pick([]string{"?,?", "0,1,-2", "'x','it''s(,)'", "?,1,'x'"}) + ")"
		case 4:
			return column + " BETWEEN " + scalar(2) + " AND " + scalar(2)
		case 5:
			return column + pick([]string{" LIKE ?", " NOT GLOB ?", " MATCH other_key", " REGEXP ?"})
		default:
			return column + " COLLATE NOCASE> ?"
		}
	}
	subquery := "SELECT key FROM items WHERE id=?"
	forbidden := []func(string) string{
		func(s string) string { return "abs(key) LIKE ? AND " + s },
		func(s string) string { return "'pattern' GLOB ? AND " + s },
		func(s string) string {
			depth := 1 + random.Intn(6)
			return strings.Repeat("(", depth) + "key" + strings.Repeat(")", depth) + "=? AND " + s
		},
		func(s string) string {
			depth := 1 + random.Intn(6)
			return strings.Repeat("(", depth) + "key,id" + strings.Repeat(")", depth) + ">(?,?) AND " + s
		},
		func(s string) string { return "(?,?)<((key,id)) AND " + s },
		func(s string) string { return "CASE key WHEN (" + subquery + ") THEN 1 END AND " + s },
		func(s string) string { return "CASE (" + subquery + ") WHEN key THEN 1 END AND " + s },
		func(s string) string {
			depth := 1 + random.Intn(6)
			return "key LIKE " + strings.Repeat("(", depth) + subquery + strings.Repeat(")", depth) + " AND " + s
		},
		func(s string) string { return "key NOT GLOB (" + subquery + ") AND " + s },
		func(s string) string { return "key MATCH (" + subquery + ") AND " + s },
		func(s string) string { return "key REGEXP (" + subquery + ") AND " + s },
		func(s string) string { return "((" + subquery + ")) NOT /* NULL */ NULL AND " + s },
		func(s string) string { return "key NOT NULL AND " + s },
		func(s string) string { return "key IN scoped AND " + s },
		func(s string) string { return "key NOT IN main.scoped AND " + s },
		func(s string) string { return "key IN json_each(?) AND " + s },
		func(s string) string { return "key>coalesce((" + subquery + "),0) AND " + s },
		func(s string) string { return "unknown(key)>? AND " + s },
		func(s string) string { return "sum(key) OVER(PARTITION BY id,key ORDER BY key)>0 AND " + s },
		func(s string) string { return "key BETWEEN ? AND (" + subquery + ") AND " + s },
		func(s string) string { return "CAST(key AS INTEGER)>? AND " + s },
		func(s string) string { return "key+1>? AND " + s },
	}
	allowed, flagged := 0, 0
	counts := make([]int, len(forbidden))
	for i := 0; i < cases; i++ {
		condition := predicate() + pick([]string{" AND ", " OR "}) + predicate()
		inside := i%2 == 0
		if !inside {
			mutation := (i / 2) % len(forbidden)
			condition = forbidden[mutation](condition)
			counts[mutation]++
		}
		sql := "SELECT id,key FROM items AS t INDEXED BY item_sort WHERE " + condition + " ORDER BY key COLLATE NOCASE,id DESC LIMIT ?"
		// Insert comments only at a known token boundary. Quoted SQL-looking data
		// above remains data, while the forbidden structural form remains outside it.
		if i%3 == 0 {
			sql = strings.Replace(sql, " WHERE ", " /* SELECT CASE OVER(,) */ WHERE -- (,)\n", 1)
		}
		if i%5 == 0 {
			sql = strings.Replace(sql, " WHERE ", " where ", 1)
		}
		issues := plan.LintSQL(sql)
		if inside && len(issues) != 0 {
			t.Fatalf("seed=%x case=%d known grammar flagged: %v\n%s", seed, i, issues, sql)
		}
		if !inside && len(issues) == 0 {
			t.Fatalf("seed=%x case=%d forbidden mutation allowed\n%s", seed, i, sql)
		}
		if inside {
			allowed++
		} else {
			flagged++
		}
	}
	for i, count := range counts {
		if count == 0 {
			t.Fatal(fmt.Sprintf("mutation %d not exercised", i))
		}
	}
	if allowed != 72109 || flagged != 72109 {
		t.Fatalf("case inventory changed: %d/%d", allowed, flagged)
	}
	t.Logf("seed=%x cases=%d allowed=%d flagged=%d forbidden families=%d counts=%v", seed, cases, allowed, flagged, len(forbidden), counts)
}
