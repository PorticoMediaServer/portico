package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func openPlanDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenHandle(filepath.Join(t.TempDir(), "plan.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = WithWriteTx(context.Background(), db, ClassMaintenance, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE item(id INTEGER PRIMARY KEY, name TEXT NOT NULL); CREATE TABLE audit(item INTEGER NOT NULL);
			CREATE TRIGGER item_audit AFTER INSERT ON item BEGIN INSERT INTO audit(item) VALUES (new.id); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertItems(ctx context.Context, db *sql.DB, class Class, n int) error {
	return WithWriteTx(ctx, db, class, func(tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			if _, err := tx.Exec(`INSERT INTO item(name) VALUES (?)`, fmt.Sprintf("item %d with a name long enough to fill pages %0200d", i, i)); err != nil {
				return err
			}
		}
		return nil
	})
}

func TestWritePlanMeasuresRowsPagesAndVMOperations(t *testing.T) {
	db := openPlanDB(t)
	ResetPlans()
	if err := insertItems(context.Background(), db, ClassInteractive, 1); err != nil {
		t.Fatal(err)
	}
	stats := Plans()
	if stats.Measured != 1 || stats.Overruns != 0 {
		t.Fatalf("one small write: %+v", stats)
	}
	// One row and its trigger row; at least one page; some VM work.
	if stats.Largest.Rows != 2 || stats.Largest.Pages < 1 {
		t.Fatalf("small write cost %+v", stats.Largest)
	}
	if _, err := ExecWrite(context.Background(), db, ClassInteractive, `UPDATE item SET name = 'x'`); err != nil {
		t.Fatal(err)
	}
	if got := Plans().Measured; got != 2 {
		t.Fatalf("autocommit write not measured: %d", got)
	}
}

func TestWritePlanCountsAnOverrunFromATestFileWithoutPanicking(t *testing.T) {
	db := openPlanDB(t)
	ResetPlans()
	// 100 inserts change 200 rows through the trigger: over the 160-row plan.
	if err := insertItems(context.Background(), db, ClassBackgroundMedia, 100); err != nil {
		t.Fatal(err)
	}
	stats := Plans()
	if stats.Overruns != 1 || stats.OverrunsByClass["background-media"] != 1 || stats.OverrunsByDimension["rows"] != 1 {
		t.Fatalf("overrun not recorded: %+v", stats)
	}
	recent := stats.Recent[len(stats.Recent)-1]
	if recent.Cost.Rows != 200 || recent.Plan != "background-media" || !strings.Contains(recent.Site, "insertItems") {
		t.Fatalf("overrun record %+v", recent)
	}
}

func TestWritePlanNamedPlanCoversItsDeclaredWork(t *testing.T) {
	db := openPlanDB(t)
	ResetPlans()
	plan := WritePlan{Name: "test-bulk", Rows: 400, Why: "test"}
	if err := insertItems(WithPlan(context.Background(), plan), db, ClassBackgroundMedia, 100); err != nil {
		t.Fatal(err)
	}
	if stats := Plans(); stats.Overruns != 0 || stats.Measured != 1 {
		t.Fatalf("declared plan overran: %+v", stats)
	}
}

func TestWritePlanOverrunFromServerCodePanicsInTests(t *testing.T) {
	t.Setenv(PlanReportEnv, "")
	ResetPlans()
	defer func() {
		message, _ := recover().(string)
		if !strings.Contains(message, `exceeded its "interactive" plan on rows`) {
			t.Fatalf("expected the test-mode assertion, got %q", message)
		}
		if Plans().Overruns != 1 {
			t.Fatal("the overrun was not counted before the assertion")
		}
	}()
	// An unresolved site stands for server code: it is not in a test file.
	notePlan(ClassInteractive, DefaultPlan(ClassInteractive), WriteCost{Rows: 321}, true, callSite{})
}

func TestWritePlanSmallOverrunFromServerCodeIsOnlyCounted(t *testing.T) {
	t.Setenv(PlanReportEnv, "")
	ResetPlans()
	notePlan(ClassInteractive, DefaultPlan(ClassInteractive), WriteCost{Rows: 320, Pages: 256}, true, callSite{})
	if stats := Plans(); stats.Overruns != 1 || stats.OverrunsByDimension["rows"] != 1 || stats.OverrunsByDimension["pages"] != 1 {
		t.Fatalf("an overrun within twice the plan must still be counted: %+v", stats)
	}
}

// The security fence is 256 KiB, not the design's 128 KiB: identity fences
// measure 136–264 KiB on this schema (P2-11, raised in P2-13).
func TestDefaultPlansFollowTheDesignReservations(t *testing.T) {
	for class, pages := range map[Class]int64{ClassSecurityFence: 64, ClassInteractive: 128, ClassEstablishedPlayback: 128, ClassBackgroundMedia: 256, ClassMaintenance: 256} {
		plan := DefaultPlan(class)
		if plan.Rows != 160 || plan.VMOps != 250_000 || plan.Pages != pages {
			t.Fatalf("%s plan %+v", class, plan)
		}
	}
}

func TestDriverCountsAndRefusesUngatedWrites(t *testing.T) {
	db := openPlanDB(t)
	_, readerBefore := UngatedWrites()
	// From a test file: counted, allowed (fixture seeding).
	if _, err := db.Exec(`INSERT INTO item(name) VALUES ('fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, reader := UngatedWrites(); reader != readerBefore+1 {
		t.Fatalf("read-pool write not counted: %d -> %d", readerBefore, reader)
	}
	// Reads, temporary tables and pragmas are not changes.
	for _, query := range []string{`SELECT count(*) FROM item`, `PRAGMA wal_checkpoint(PASSIVE)`, `CREATE TEMP TABLE scratch(x)`, `WITH x AS (SELECT 1) SELECT * FROM x`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(query, err)
		}
	}
	if _, reader := UngatedWrites(); reader != readerBefore+1 {
		t.Fatalf("a read was counted as a change: %d", reader)
	}
	// From server code: refused in a test binary.
	t.Setenv(PlanReportEnv, "")
	previous := ungatedCaller
	ungatedCaller = func() (string, string, int, bool) {
		return "github.com/PorticoMediaServer/portico/server/internal/example.write", "example.go", 1, false
	}
	defer func() { ungatedCaller = previous }()
	if _, err := db.Exec(`UPDATE item SET name = 'raw'`); !errors.Is(err, ErrUngatedWrite) {
		t.Fatalf("read-pool write from server code: %v", err)
	}
	writerBefore, _ := UngatedWrites()
	if _, err := writerHandle(db).Exec(`DELETE FROM item`); !errors.Is(err, ErrUngatedWrite) {
		t.Fatalf("writer write without the gate: %v", err)
	}
	if writer, _ := UngatedWrites(); writer != writerBefore+1 {
		t.Fatal("ungated writer write not counted")
	}
	// The gated paths are untouched.
	if _, err := ExecWrite(context.Background(), db, ClassInteractive, `DELETE FROM item`); err != nil {
		t.Fatal(err)
	}
}

func TestMutationClassifier(t *testing.T) {
	for query, want := range map[string]bool{
		"INSERT INTO a VALUES (1)":                           true,
		"  -- note\n update a set x=1":                       true,
		"/* c */ DELETE FROM a":                              true,
		"REPLACE INTO a VALUES (1)":                          true,
		"CREATE TABLE a(x)":                                  true,
		"CREATE TEMP TABLE a(x)":                             false,
		"CREATE TEMPORARY TABLE a(x)":                        false,
		"DROP TABLE temp.a":                                  false,
		"WITH x AS (SELECT 1) INSERT INTO a SELECT * FROM x": true,
		"WITH x AS (SELECT 1) SELECT * FROM x":               false,
		"SELECT updated_at FROM a":                           false,
		"PRAGMA wal_checkpoint(TRUNCATE)":                    false,
		"PRAGMA foreign_keys=ON; INSERT INTO a VALUES (1)":   true,
		"ANALYZE":         true,
		"BEGIN IMMEDIATE": true,
	} {
		if got := mutatesDatabase(query); got != want {
			t.Errorf("%q: got %v, want %v", query, got, want)
		}
	}
}

func TestConnRoleIsStrippedFromTheDSN(t *testing.T) {
	writer, err := DefaultPolicy().WriterDSN("/tmp/x.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	name, role := connRoleOf(writer)
	if role != roleWriter || strings.Contains(name, roleParameter) || !strings.Contains(name, "wal_autocheckpoint") {
		t.Fatalf("writer DSN %q -> %q %v", writer, name, role)
	}
	reader, _ := DefaultPolicy().readerDSN("/tmp/x.sqlite")
	if name, role = connRoleOf(reader); role != roleReader || strings.Contains(name, roleParameter) || strings.HasSuffix(name, "&") {
		t.Fatalf("reader DSN %q -> %q %v", reader, name, role)
	}
	if name, role = connRoleOf("file:/tmp/x.sqlite"); role != roleUnmanaged || name != "file:/tmp/x.sqlite" {
		t.Fatal("unmarked DSN changed")
	}
}

func TestWriterConnectionHasItsOwnShortBusyTimeout(t *testing.T) {
	db := openPlanDB(t)
	var writer, reader int64
	if err := WithWriteTx(context.Background(), db, ClassInteractive, func(tx *sql.Tx) error {
		return tx.QueryRow(`PRAGMA busy_timeout`).Scan(&writer)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&reader); err != nil {
		t.Fatal(err)
	}
	if writer != 25 || reader != int64(DefaultPolicy().BusyTimeoutMilli) {
		t.Fatalf("writer busy timeout %d, reader %d", writer, reader)
	}
	invalid := DefaultPolicy()
	invalid.WriterBusyTimeoutMilli = 15000
	if invalid.Validate() == nil {
		t.Fatal("a writer busy timeout that hides a stall validated")
	}
}

func TestHeavyReadsAreCountedNotInterrupted(t *testing.T) {
	db := openPlanDB(t)
	before := Reads().HeavyReads
	var total int64
	if err := db.QueryRow(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<400000) SELECT sum(x) FROM n`).Scan(&total); err != nil || total == 0 {
		t.Fatalf("heavy read failed: %v", err)
	}
	if got := Reads().HeavyReads; got != before+1 {
		t.Fatalf("a read past the VM ceiling was not counted: %d -> %d", before, got)
	}
	if err := db.QueryRow(`SELECT count(*) FROM item`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if got := Reads().HeavyReads; got != before+1 {
		t.Fatal("a light read was counted as heavy")
	}
}

func TestUsePlanReplacesThePlanOfARunningTransaction(t *testing.T) {
	db := openPlanDB(t)
	ResetPlans()
	unit := WritePlan{Name: "one-unit", Rows: 1000, Why: "test"}
	if err := WithWriteTx(context.Background(), db, ClassBackgroundMedia, func(tx *sql.Tx) error {
		for i := 0; i < 100; i++ {
			if _, err := tx.Exec(`INSERT INTO item(name) VALUES ('x')`); err != nil {
				return err
			}
		}
		return UsePlan(tx, unit)
	}); err != nil {
		t.Fatal(err)
	}
	if stats := Plans(); stats.Overruns != 0 || stats.Measured != 1 {
		t.Fatalf("the declared plan was not used: %+v", stats)
	}
	if UsePlan(nil, unit) == nil {
		t.Fatal("a plan without a live transaction was accepted")
	}
}

func TestASnapshotOpenedInsideAWriteIsNotMeteredAsTheWrite(t *testing.T) {
	db := openPlanDB(t)
	ResetPlans()
	if err := WithWriteTxContext(context.Background(), db, ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO item(name) VALUES ('x')`); err != nil {
			return err
		}
		// A read on another connection with the write's context.
		read, err := ReadHandle(ctx, db).BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		var n int
		if err = read.QueryRow(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<50000) SELECT count(*) FROM n`).Scan(&n); err != nil {
			return err
		}
		return read.Commit()
	}); err != nil {
		t.Fatal(err)
	}
	largest := Plans().Largest
	if largest.VMOps < 0 || largest.VMOps > 50000 || largest.Rows != 2 {
		t.Fatalf("the write was charged for the snapshot's work: %+v", largest)
	}
}
