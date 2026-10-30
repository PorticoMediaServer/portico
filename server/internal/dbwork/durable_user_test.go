package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDurableUserWriteKeepsClassAndRestoresConnection(t *testing.T) {
	db, err := OpenHandle(filepath.Join(t.TempDir(), "user.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, durable := range []bool{true, false} {
		ctx := context.Background()
		if durable {
			ctx = WithDurableUser(ctx)
		}
		err = WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
			var mode int
			if err := tx.QueryRow(`PRAGMA synchronous`).Scan(&mode); err != nil {
				return err
			}
			want := 1
			if durable {
				want = 2
			}
			if mode != want {
				t.Fatalf("durable=%v synchronous=%d want=%d", durable, mode, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
