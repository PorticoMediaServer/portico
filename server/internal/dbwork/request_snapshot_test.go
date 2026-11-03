package dbwork

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// snapshotOf runs one read snapshot under ctx and returns its transaction and
// the row count of sample it saw.
func snapshotOf(t *testing.T, ctx context.Context, db *sql.DB) (*sql.Tx, int) {
	t.Helper()
	var tx *sql.Tx
	var rows int
	if err := WithReadSnapshot(ctx, db, func(snapshot context.Context) error {
		tx = Snapshot(snapshot)
		return QueryRow(snapshot, db, `SELECT count(*) FROM sample`).Scan(&rows)
	}); err != nil {
		t.Fatal(err)
	}
	return tx, rows
}

func TestARequestReusesItsSnapshotOnlyWhileNothingHasCommitted(t *testing.T) {
	db := readerFixture(t)
	request, end := WithRequestSnapshot(context.Background())
	defer end()

	first, rows := snapshotOf(t, request, db)
	reusesBefore := RequestSnapshotReuses()
	second, again := snapshotOf(t, request, db)
	if second != first || again != rows {
		t.Fatal("a request opened a second snapshot although nothing had committed")
	}
	if RequestSnapshotReuses() != reusesBefore+1 {
		t.Fatal("a reused snapshot was not counted")
	}

	// A GET that writes and then reads again sees its own write.
	if _, err := ExecWrite(request, db, ClassInteractive, `INSERT INTO sample VALUES(3)`); err != nil {
		t.Fatal(err)
	}
	third, afterOwnWrite := snapshotOf(t, request, db)
	if third == second || afterOwnWrite != rows+1 {
		t.Fatalf("after the request's own write it read %d rows in the old snapshot: %v", afterOwnWrite, third == second)
	}

	// So does one that waits for another goroutine's write, made without the
	// request's context (a worker), and inside a transaction.
	if err := WithWriteTx(context.Background(), db, ClassMaintenance, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO sample VALUES(4)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fourth, afterWorker := snapshotOf(t, request, db)
	if fourth == third || afterWorker != rows+2 {
		t.Fatalf("after a worker's commit it read %d rows, want %d", afterWorker, rows+2)
	}

	// A write that returns its rows, outside a transaction, commits when its
	// rows finish.
	reserved, conn, release, err := ReserveWriterConn(context.Background(), db, ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	var id int
	err = conn.QueryRowContext(reserved, `INSERT INTO sample VALUES(5) RETURNING id`).Scan(&id)
	release()
	if err != nil {
		t.Fatal(err)
	}
	if _, latest := snapshotOf(t, request, db); latest != rows+3 {
		t.Fatalf("after an INSERT … RETURNING it read %d rows, want %d", latest, rows+3)
	}
}

func TestARequestSnapshotIsHeldOnlyBriefly(t *testing.T) {
	db := readerFixture(t)
	request, end := WithRequestSnapshot(context.Background())

	first, _ := snapshotOf(t, request, db)
	if Snapshots().Held == 0 {
		t.Fatal("the request let its snapshot go between two reads")
	}
	time.Sleep(10 * requestSnapshotIdle)
	if held := Snapshots().Held; held != 0 {
		t.Fatalf("an idle request still held %d snapshots", held)
	}
	second, _ := snapshotOf(t, request, db)
	if second == first {
		t.Fatal("a snapshot was reused after it went idle")
	}
	end()
	if held := Snapshots().Held; held != 0 {
		t.Fatalf("a finished request still held %d snapshots", held)
	}
	// After the request ends its context opens private snapshots again.
	third, _ := snapshotOf(t, request, db)
	if third == second {
		t.Fatal("a finished request's snapshot was reused")
	}
	if held := Snapshots().Held; held != 0 {
		t.Fatalf("a read after the request ended left %d snapshots held", held)
	}
}

func TestARequestSnapshotIsNotSharedOrKeptWhereItMustNotBe(t *testing.T) {
	db := readerFixture(t)
	request, end := WithRequestSnapshot(context.Background())
	defer end()

	// Two goroutines of one request never share it.
	var inner *sql.Tx
	var outer *sql.Tx
	if err := WithReadSnapshot(request, db, func(snapshot context.Context) error {
		outer = Snapshot(snapshot)
		done := make(chan struct{})
		go func() {
			defer close(done)
			inner, _ = snapshotOf(t, request, db)
		}()
		<-done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if inner == outer {
		t.Fatal("a concurrent call of the same request shared its snapshot")
	}

	// A failed call's snapshot is not reused: the failure may be its deadline.
	failed := context.Canceled
	var broken *sql.Tx
	if err := WithReadSnapshot(request, db, func(snapshot context.Context) error {
		broken = Snapshot(snapshot)
		return failed
	}); err != failed {
		t.Fatalf("got %v, want the call's own error", err)
	}
	if after, _ := snapshotOf(t, request, db); after == broken {
		t.Fatal("a snapshot whose call failed was reused")
	}

	// The opening call's own shorter deadline does not end the request's
	// snapshot once that call has returned.
	short, cancel := context.WithTimeout(request, time.Second)
	opened, _ := snapshotOf(t, short, db)
	cancel()
	if reused, _ := snapshotOf(t, request, db); reused != opened {
		t.Fatal("the opening call's cancelled context ended the request's snapshot")
	}

	// Background work keeps its own snapshots.
	background := WithClass(request, ClassBackgroundMedia)
	first, _ := snapshotOf(t, background, db)
	if second, _ := snapshotOf(t, background, db); second == first {
		t.Fatal("background work reused a request snapshot")
	}
}

func TestARequestSnapshotEndsWithTheRequest(t *testing.T) {
	db := readerFixture(t)
	parent, cancelRequest := context.WithCancel(context.Background())
	request, end := WithRequestSnapshot(parent)
	defer end()
	snapshotOf(t, request, db)
	cancelRequest()
	// As without a request snapshot, a cancelled request cannot read.
	if err := WithReadSnapshot(request, db, func(context.Context) error { return nil }); err == nil {
		t.Fatal("a cancelled request read in the snapshot it held")
	}
}

func TestAWriteReturningRowsEndsReuseWhenItsRowsFinish(t *testing.T) {
	db := readerFixture(t)
	idle := requestSnapshotIdle
	requestSnapshotIdle = time.Minute
	defer func() { requestSnapshotIdle = idle }()
	request, end := WithRequestSnapshot(context.Background())
	defer end()

	reserved, conn, release, err := ReserveWriterConn(context.Background(), db, ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	rows, err := conn.QueryContext(reserved, `INSERT INTO sample VALUES(9) RETURNING id`)
	if err != nil {
		t.Fatal(err)
	}
	// The snapshot opens while the insert has not yet committed.
	_, before := snapshotOf(t, request, db)
	for rows.Next() {
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, after := snapshotOf(t, request, db); after != before+1 {
		t.Fatalf("after the insert's rows finished it read %d rows, want %d", after, before+1)
	}
}
