package dbwork

import (
	"context"
	"database/sql"
	"testing"
)

// A read repeated inside one read snapshot is answered once, because the
// snapshot cannot change under it. A new snapshot, a write, or a raw
// transaction reads afresh, so a committed change is always seen next time.
func TestASnapshotAnswersARepeatedReadOnce(t *testing.T) {
	db := readerFixture(t)
	reads := 0
	count := func(tx *sql.Tx) int {
		t.Helper()
		n, err := RememberInSnapshot(tx, "sample", nil, func() (int, error) {
			reads++
			var n int
			err := tx.QueryRow(`SELECT count(*) FROM sample`).Scan(&n)
			return n, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	var first int
	if err := WithReadSnapshot(context.Background(), db, func(snapshot context.Context) error {
		tx := Snapshot(snapshot)
		first = count(tx)
		if count(tx) != first || count(tx) != first {
			t.Fatal("a snapshot answered a repeated read differently")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("three reads in one snapshot ran %d times, want 1", reads)
	}

	if _, err := ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO sample VALUES(3)`); err != nil {
		t.Fatal(err)
	}
	if err := WithReadSnapshot(context.Background(), db, func(snapshot context.Context) error {
		if got := count(Snapshot(snapshot)); got != first+1 {
			t.Fatalf("a new snapshot answered %d from an old one, want %d", got, first+1)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Inside a write the answer can change under the caller: never kept.
	reads = 0
	if err := WithWriteTx(context.Background(), db, ClassInteractive, func(tx *sql.Tx) error {
		count(tx)
		if _, err := tx.Exec(`INSERT INTO sample VALUES(4)`); err != nil {
			return err
		}
		if got := count(tx); got != first+2 {
			t.Fatalf("a write read %d after its own insert, want %d", got, first+2)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatalf("a write's reads ran %d times, want 2", reads)
	}
}
