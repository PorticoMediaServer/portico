package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func readerFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenHandle(filepath.Join(t.TempDir(), "readers.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = ExecWrite(context.Background(), db, ClassInteractive, `CREATE TABLE sample(id INTEGER); INSERT INTO sample VALUES(1),(2)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestReaderLifetimesCoverSnapshotHelpersAndDirectTransactions(t *testing.T) {
	db := readerFixture(t)
	// ReaderLifetimes is process-wide, and another fixture can still be
	// physically releasing a reader after its test has returned: database/sql
	// rolls a canceled transaction back from a goroutine, so joining the test's
	// own goroutines does not join that cleanup. Measure this fixture's scope so
	// a foreign lease cannot move the baseline under the assertions.
	scope := readerScopeForTest(t, db)
	ctx := context.Background()
	baseline := scope.stats().Snapshots
	if baseline != 0 {
		t.Fatalf("fixture scope began with%d snapshots", baseline)
	}
	defer func() {
		if t.Failed() {
			t.Logf("reader lifetime baseline=%d final=%+v", baseline, scope.stats())
		}
	}()
	check := func(want int) {
		t.Helper()
		if stats := scope.stats(); stats.Snapshots != baseline+want {
			t.Fatalf("snapshot lifetime: %+v, want%d", stats, baseline+want)
		}
	}
	read, closeRead, err := BeginRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	var count int
	if err = read.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	check(1) // Closing a statement must not release its transaction snapshot.
	closeRead()
	closeRead()
	check(0)
	snapshot, err := BeginSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	if err = snapshot.Commit(); err != nil {
		t.Fatal(err)
	}
	check(0)
	if err = WithReadSnapshot(ctx, db, func(snap context.Context) error {
		check(1)
		return QueryRow(snap, db, `SELECT count(*) FROM sample`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	check(0)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	check(0)
	cancelCtx, cancel := context.WithCancel(ctx)
	tx, err = db.BeginTx(cancelCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	cancel()
	waitReaderCondition(t, func() bool { return scope.stats().Snapshots == baseline })
	tx, err = db.Begin() // Legacy/default transactions also own a snapshot.
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	check(0)
}

func TestReaderLifetimesCoverImplicitRowsPreparedRowsAndErrors(t *testing.T) {
	db := readerFixture(t)
	baseline := ReaderLifetimes().ImplicitRows
	check := func(want int) {
		t.Helper()
		if stats := ReaderLifetimes(); stats.ImplicitRows != baseline+want {
			t.Fatalf("implicit lifetime: %+v, want%d", stats, baseline+want)
		}
	}
	rows, err := db.Query(`SELECT id FROM sample`)
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	check(0)
	stmt, err := db.PrepareContext(context.Background(), `SELECT id FROM sample`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.QueryContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	check(0) // database/sql's EOF auto-close must release the implicit reader.
	if _, err = db.Query(`SELECT missing_column FROM sample`); err == nil {
		t.Fatal("invalid query succeeded")
	}
	check(0)
	cancelCtx, cancel := context.WithCancel(context.Background())
	rows, err = db.QueryContext(cancelCtx, `SELECT id FROM sample`)
	if err != nil {
		t.Fatal(err)
	}
	check(1)
	cancel()
	waitReaderCondition(t, func() bool { return ReaderLifetimes().ImplicitRows == baseline })
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatalf("returned rows did not observe cancellation: %v", rows.Err())
	}
	if err = WithWriteTx(context.Background(), db, ClassInteractive, func(tx *sql.Tx) error {
		if stats := ReaderLifetimes(); stats.Snapshots != 0 {
			t.Fatalf("gated writer counted as snapshot: %+v", stats)
		}
		var count int
		err := tx.QueryRow(`SELECT count(*) FROM sample`).Scan(&count)
		check(0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReaderLifetimeScopeIsolatesForeignFixture(t *testing.T) {
	fixture := readerFixture(t)
	foreign := readerFixture(t)
	scope := readerScopeForTest(t, fixture)
	foreignScope := readerScopeForTest(t, foreign)
	baseline := scope.stats().Snapshots

	_, closeForeign, err := BeginRead(context.Background(), foreign)
	if err != nil {
		t.Fatal(err)
	}
	defer closeForeign()
	if stats := foreignScope.stats(); stats.Snapshots != 1 {
		t.Fatalf("foreign snapshot lifetime: %+v, want1", stats)
	}
	if stats := scope.stats(); stats.Snapshots != baseline {
		t.Fatalf("foreign lease entered the fixture scope: %+v, want%d", stats, baseline)
	}
	if stats := ReaderLifetimes(); stats.Snapshots < baseline+1 {
		t.Fatalf("process-wide lifetime missed the foreign lease: %+v", stats)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tx, err := fixture.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if stats := scope.stats(); stats.Snapshots != baseline+1 {
		t.Fatalf("fixture snapshot lifetime: %+v, want%d", stats, baseline+1)
	}
	cancel()
	waitReaderCondition(t, func() bool { return scope.stats().Snapshots == baseline })
	// The foreign lease is deliberately still held: convergence here proves the
	// fixture measures its own scope, not the process-wide aggregate that made
	// the old baseline non-deterministic.
	if stats := foreignScope.stats(); stats.Snapshots != 1 {
		t.Fatalf("foreign snapshot ended during fixture convergence: %+v", stats)
	}
	if stats := ReaderLifetimes(); stats.Snapshots < baseline+1 {
		t.Fatalf("process-wide lifetime lost the foreign lease: %+v", stats)
	}
}

func TestReaderLifetimeDirectTransactionCancellationLateRelease(t *testing.T) {
	db := readerFixture(t)
	scope := readerScopeForTest(t, db)
	baseline := scope.stats().Snapshots
	check := func(want int) {
		t.Helper()
		if stats := scope.stats(); stats.Snapshots != baseline+want {
			t.Fatalf("snapshot lifetime: %+v, want%d", stats, baseline+want)
		}
	}

	// A context that is already canceled must be refused before any connection
	// admits a reader lease.
	cancelled, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	if tx, err := db.BeginTx(cancelled, &sql.TxOptions{ReadOnly: true}); !errors.Is(err, context.Canceled) {
		if tx != nil {
			_ = tx.Rollback()
		}
		t.Fatalf("already-canceled BeginTx: tx=%v err=%v", tx, err)
	}
	check(0)

	// Cancel after a successful read. database/sql releases the snapshot from
	// its context goroutine; an explicit late Rollback/Commit must not touch the
	// already-released lease or resurrect the count.
	ctx, cancelTx := context.WithCancel(context.Background())
	defer cancelTx()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("canceled fixture read count%d, want2", count)
	}
	check(1)
	cancelTx()
	waitReaderCondition(t, func() bool { return scope.stats().Snapshots == baseline })
	if err = tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("late rollback: %v", err)
	}
	check(0)
	if err = tx.Commit(); !errors.Is(err, sql.ErrTxDone) && !errors.Is(err, context.Canceled) {
		t.Fatalf("late commit: %v", err)
	}
	check(0)

	// The reverse race: release explicitly at the same moment the cancellation
	// watchdog would. Either owner may win; the lease must still be released
	// exactly once and the scope must neither leak nor go below its baseline.
	ctx, cancelTx = context.WithCancel(context.Background())
	defer cancelTx()
	tx, err = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	check(1)
	cancelTx()
	if err = tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("racing rollback: %v", err)
	}
	waitReaderCondition(t, func() bool { return scope.stats().Snapshots == baseline })
	check(0)
}

// Two implicit result sets open on one physical connection share its single
// reader lease, which ends when the last of them closes.
func TestImplicitRowsShareOneLeasePerConnection(t *testing.T) {
	db := readerFixture(t)
	ctx := context.Background()
	scope := readerScopeForTest(t, db)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	first, err := conn.QueryContext(ctx, `SELECT value FROM json_each('[1,2,3]')`)
	if err != nil {
		t.Fatal(err)
	}
	first.Next()
	second, err := conn.QueryContext(ctx, `SELECT value FROM json_each('[4,5,6]')`)
	if err != nil {
		first.Close()
		t.Fatalf("second result set on one connection: %v", err)
	}
	second.Next()
	if stats := scope.stats(); stats.ImplicitRows != 1 {
		t.Fatalf("two result sets on one connection hold %d leases", stats.ImplicitRows)
	}
	first.Close()
	if stats := scope.stats(); stats.ImplicitRows != 1 {
		t.Fatal("closing one result set ended the shared lease")
	}
	second.Close()
	if stats := scope.stats(); stats.ImplicitRows != 0 {
		t.Fatal("the shared lease outlived its last result set")
	}
}
