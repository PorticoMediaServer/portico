package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestBindSnapshotCarriesExactReaderWithoutGrantingWriter(t *testing.T) {
	db := testDB(t)
	type originalKey struct{}
	original, cancel := context.WithCancel(context.WithValue(context.Background(), originalKey{}, "original"))
	defer cancel()
	view, err := BeginSnapshot(original, db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	bound, err := BindSnapshot(original, view.Tx())
	if err != nil || Snapshot(bound) != view.Tx() || InWriteTx(bound) || bound.Value(originalKey{}) != "original" {
		t.Fatal("reader lost its exact transaction/caller or gained writer authority", err)
	}
	if Snapshot(original) != nil {
		t.Fatal("binding mutated the original operation context")
	}
	joined, err := BeginSnapshot(bound, db)
	if err != nil {
		t.Fatal(err)
	}
	if joined.Tx() != view.Tx() {
		t.Fatal("read helper opened a detached snapshot")
	}
	joined.Rollback()
	cancel()
	if !errors.Is(bound.Err(), context.Canceled) {
		t.Fatal("carrier detached from original cancellation")
	}
}

func TestBindSnapshotRetainsActualGatedWriterAndUncommittedRows(t *testing.T) {
	db := testDB(t)
	for _, reserved := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "reserved connection"}[reserved], func(t *testing.T) {
			ctx := context.Background()
			var w *Write
			var err error
			if reserved {
				var conn *sql.Conn
				var release func()
				ctx, conn, release, err = ReserveWriterConn(ctx, db, ClassPlaybackStart)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				w, err = BeginConn(ctx, conn, ClassPlaybackStart)
			} else {
				w, err = Begin(ctx, db, ClassPlaybackStart)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			if _, err = w.Tx().Exec(`INSERT INTO rows(value) VALUES('not committed')`); err != nil {
				t.Fatal(err)
			}
			bound, err := BindSnapshot(ctx, w.Tx())
			if err != nil || Snapshot(bound) != w.Tx() || !InWriteTx(bound) || carriedWrite(bound) != w.owned || bound.Value(changeKey{}) != w.changes {
				t.Fatal("binding relabeled or replaced the actual gated writer", err)
			}
			if class, ok := WriteClass(bound); !ok || class != ClassPlaybackStart {
				t.Fatal("binding lost actual writer class", class, ok)
			}
			var count int
			err = Snapshot(bound).QueryRow(`SELECT count(*) FROM rows WHERE value='not committed'`).Scan(&count)
			if err != nil || count != 1 {
				t.Fatal("callback read detached from accepting writer", count, err)
			}
			// An existing carrier cannot relabel the actual registered writer.
			bound, err = BindSnapshot(WithWrite(ctx, w.Tx(), ClassMaintenance), w.Tx())
			if err != nil || carriedWrite(bound) != w.owned {
				t.Fatal("binding trusted a replacement writer/class", err)
			}
		})
	}
}

func TestBindSnapshotRefusesForeignCarriersAndInvalidInputs(t *testing.T) {
	db := testDB(t)
	view, err := BeginSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	writer, err := Begin(context.Background(), db, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	for _, input := range []struct {
		ctx context.Context
		tx  *sql.Tx
	}{
		{nil, view.Tx()}, {context.Background(), nil},
		{view.Context(), writer.Tx()}, {writer.Context(), view.Tx()},
		{withSnapshot(writer.Context(), view.Tx()), writer.Tx()},
	} {
		if bound, err := BindSnapshot(input.ctx, input.tx); !errors.Is(err, ErrTransactionContext) || bound != nil {
			t.Fatal("binding accepted a foreign or missing carrier", bound, err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if bound, err := BindSnapshot(canceled, view.Tx()); !errors.Is(err, context.Canceled) || bound != nil {
		t.Fatal("binding revived a canceled caller", bound, err)
	}
}
