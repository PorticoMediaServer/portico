package plan_test

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestSQLClosedDMLEnumeration(t *testing.T) {
	const seed int64 = 0x484e444d
	const cases = 16384
	r := rand.New(rand.NewSource(seed))
	forbidden := []string{
		"INSERT INTO t(id) SELECT id FROM rhs",
		"INSERT INTO t(id) VALUES((SELECT id FROM rhs))",
		"INSERT INTO t(id) VALUES(?,?)",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT(id) DO UPDATE SET id=coalesce((SELECT id FROM rhs),?)",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT(id) DO UPDATE SET id=(excluded.id)",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT(id) DO UPDATE SET id=? WHERE id=?",
		"UPDATE t SET value=CASE id WHEN ? THEN ? END WHERE id=?",
		"UPDATE t SET value=(?,?) WHERE id=?",
		"UPDATE t SET value=coalesce((SELECT id FROM rhs),0) WHERE id=?",
		"UPDATE t SET value=? WHERE id LIKE (SELECT id FROM rhs)",
		"UPDATE t SET value=? WHERE id NOT NULL",
		"DELETE FROM t WHERE id IN rhs",
		"DELETE FROM t WHERE (id,value)>(?,?)",
		"DELETE FROM t WHERE id=? RETURNING sum(id) OVER(PARTITION BY id,value)",
		"DELETE FROM t WHERE id=? RETURNING (id)",
		"DELETE FROM t WHERE id=?; SELECT id FROM rhs",
	}
	allowed, flagged := 0, 0
	for i := 0; i < cases; i++ {
		var sql string
		inside := i%2 == 0
		if inside {
			column := []string{"value", `"CASE(,)"`, "[other]", "`quoted`"}[r.Intn(4)]
			predicate := []string{"id=?", "id IN(?,?)", "id BETWEEN ? AND ?", "id IS NULL OR id=?"}[r.Intn(4)]
			switch r.Intn(4) {
			case 0:
				sql = "INSERT INTO t(id," + column + ") VALUES(?,?) ON CONFLICT(id) DO NOTHING RETURNING id"
			case 1:
				sql = "INSERT INTO t(id," + column + ") VALUES(?,?) ON CONFLICT(id) DO UPDATE SET " + column + "=excluded." + column
			case 2:
				sql = "UPDATE t SET " + column + "=coalesce(" + column + ",0)" + strings.Repeat("+?", r.Intn(8)) + " WHERE " + predicate + " RETURNING id"
			default:
				sql = "DELETE FROM t WHERE " + predicate + " RETURNING id," + column
			}
			allowed++
		} else {
			sql = forbidden[(i/2)%len(forbidden)]
			flagged++
		}
		if i%3 == 0 {
			sql = "/* SELECT CASE () */ " + sql
		}
		if i%5 == 0 {
			sql = strings.Replace(sql, " WHERE ", " WHERE -- SELECT()\n", 1)
		}
		issues := plan.LintSQL(sql)
		if inside != (len(issues) == 0) {
			t.Fatalf("seed=%x case=%d allowed=%v: %v\n%s", seed, i, inside, issues, sql)
		}
	}
	if allowed != 8192 || flagged != 8192 {
		t.Fatal("case inventory changed")
	}
	if len(plan.LintSQL("UPDATE t SET value=value"+strings.Repeat("+?", 8)+" WHERE id=?")) == 0 {
		t.Fatal("ninth SET term accepted")
	}
	t.Logf("seed=%x cases=%d allowed=%d flagged=%d forbidden families=%d", seed, cases, allowed, flagged, len(forbidden))
}
