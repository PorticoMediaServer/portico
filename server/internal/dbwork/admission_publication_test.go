package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
)

// Every writer that follows a changing autocommit must see its publication.
// Check the next writer boundary; rollback and no-op semantics are checked
// separately, because they must not invent changing-autocommit publications.
func TestAdmissionPublicationBridge(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for n := 0; n < 100; n++ {
		before := Publications()
		var wg sync.WaitGroup
		wg.Add(1)
		var writeErr error
		go func() {
			defer wg.Done()
			_, writeErr = ExecWrite(ctx, db, ClassInteractive, "INSERT INTO rows(value) VALUES('x')")
		}()
		wg.Wait()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
			if Publications() != before+1 {
				t.Fatalf("following writer saw publication %d, want %d", Publications(), before+1)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	before := Publications()
	if _, err := ExecWrite(ctx, db, ClassInteractive, "UPDATE rows SET value='x' WHERE id=-1"); err != nil {
		t.Fatal(err)
	}
	if Publications() != before {
		t.Fatal("no-op autocommit published")
	}
	refused := errors.New("rollback")
	if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO rows(value) VALUES('rollback')")
		if err != nil {
			return err
		}
		return refused
	}); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if Publications() != before {
		t.Fatal("rollback published")
	}
}
