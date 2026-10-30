package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
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

func TestPlannerStatisticsRetireOldPoolsWithoutInterruptingSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := OpenHandle(filepath.Join(t.TempDir(), "planner.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE measured(id INTEGER PRIMARY KEY); INSERT INTO measured VALUES(1)`); err != nil {
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
	tx, err := foreground.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM measured`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("initial snapshot: %d %v", count, err)
	}
	PlannerStatisticsChanged(db)
	// The refresh cannot abort or lend a successor connection to this snapshot.
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM measured`).Scan(&count); err != nil || count != 1 {
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
	if plannerConnection(t, fresh) == oldForeground {
		t.Fatal("foreground reused stale planner")
	}
	freshBackground, err := backgroundDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer freshBackground.Close()
	if plannerConnection(t, freshBackground) == oldBackground {
		t.Fatal("background reused stale planner")
	}
	if err = fresh.QueryRowContext(ctx, `SELECT count(*) FROM measured`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("fresh connection lost data: %d %v", count, err)
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
