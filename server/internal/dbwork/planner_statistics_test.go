package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func plannerConnection(t *testing.T, conn *sql.Conn) *observedConn {
	t.Helper()
	var out *observedConn
	if err := conn.Raw(func(raw any) error { out = raw.(*observedConn); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func plannedIndex(t *testing.T, ctx context.Context, conn *sql.Conn) string {
	t.Helper()
	rows, err := conn.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM measured WHERE common=1 AND rare=7`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err = rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	return plan
}

// New planner statistics reach every pooled connection in place: the next
// borrow reloads them and plans with them, the pool is not reopened (each
// reopen re-parses the whole schema), and a snapshot that holds a connection
// keeps its transaction.
func TestPlannerStatisticsReloadPooledConnectionsInPlace(t *testing.T) {
	ctx := context.Background()
	db, err := OpenHandle(filepath.Join(t.TempDir(), "planner.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// "common" is the same in every row and "rare" nearly unique; only
	// statistics tell the planner which index to use.
	if _, err = db.Exec(`CREATE TABLE measured(id INTEGER PRIMARY KEY, common INTEGER, rare INTEGER);
 CREATE INDEX measured_rare ON measured(rare); CREATE INDEX measured_common ON measured(common);
 WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<2000) INSERT INTO measured SELECT i,1,i FROM n`); err != nil {
		t.Fatal(err)
	}
	foreground, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	backgroundDB := ReadHandle(WithClass(ctx, ClassMaintenance), db)
	background, err := backgroundDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldForeground, oldBackground := plannerConnection(t, foreground), plannerConnection(t, background)
	if plan := plannedIndex(t, ctx, foreground); !strings.Contains(plan, "measured_common") {
		t.Fatalf("without statistics the fixture should plan on measured_common: %s", plan)
	}
	tx, err := foreground.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM measured`).Scan(&count); err != nil || count != 2000 {
		t.Fatalf("initial snapshot: %d %v", count, err)
	}
	if _, err = db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	PlannerStatisticsChanged(db)
	// The refresh cannot abort or lend a successor connection to this snapshot.
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM measured`).Scan(&count); err != nil || count != 2000 {
		t.Fatalf("held snapshot: %d %v", count, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	foreground.Close()
	background.Close()
	fresh, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if plannerConnection(t, fresh) != oldForeground {
		t.Fatal("the foreground connection was reopened instead of reloading its statistics")
	}
	if plan := plannedIndex(t, ctx, fresh); !strings.Contains(plan, "measured_rare") {
		t.Fatalf("foreground still plans without the new statistics: %s", plan)
	}
	freshBackground, err := backgroundDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer freshBackground.Close()
	if plannerConnection(t, freshBackground) != oldBackground {
		t.Fatal("the background connection was reopened instead of reloading its statistics")
	}
	if plan := plannedIndex(t, ctx, freshBackground); !strings.Contains(plan, "measured_rare") {
		t.Fatalf("background still plans without the new statistics: %s", plan)
	}
}

func TestPlannerStatisticsRetirementIsDatabaseScoped(t *testing.T) {
	ctx := context.Background()
	first, err := OpenHandle(filepath.Join(t.TempDir(), "first.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenHandle(filepath.Join(t.TempDir(), "second.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	conn, err := second.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := plannerConnection(t, conn)
	conn.Close()
	PlannerStatisticsChanged(first)
	conn, err = second.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if plannerConnection(t, conn) != original {
		t.Fatal("another database's planner was retired")
	}
}
