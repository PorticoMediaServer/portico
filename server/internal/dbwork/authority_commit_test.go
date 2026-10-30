package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAuthorityTransactionCommitFence(t *testing.T) {
	for _, statement := range []struct {
		name string
		run  func(context.Context, *sql.Tx) error
	}{
		{"exec", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE download_grants SET revoked=1 WHERE hash='grant'`)
			return err
		}},
		{"returning", func(ctx context.Context, tx *sql.Tx) error {
			var revoked int
			return tx.QueryRowContext(ctx, `UPDATE download_grants SET revoked=1 WHERE hash='grant' RETURNING revoked`).Scan(&revoked)
		}},
		{"prepared_exec", func(ctx context.Context, tx *sql.Tx) error {
			stmt, err := tx.PrepareContext(ctx, `DELETE FROM download_grants WHERE hash=?`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(ctx, "grant")
			return err
		}},
		{"prepared_returning", func(ctx context.Context, tx *sql.Tx) error {
			stmt, err := tx.PrepareContext(ctx, `DELETE FROM download_grants WHERE hash=? RETURNING revoked`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			var revoked int
			return stmt.QueryRowContext(ctx, "grant").Scan(&revoked)
		}},
	} {
		for _, commit := range []bool{true, false} {
			ending := "rollback"
			if commit {
				ending = "commit"
			}
			t.Run(statement.name+"/"+ending, func(t *testing.T) {
				ctx := context.Background()
				db, err := OpenHandle(filepath.Join(t.TempDir(), "authority.db"), DefaultPolicy())
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for _, query := range []string{
					`CREATE TABLE download_grants(hash TEXT PRIMARY KEY, revoked INTEGER NOT NULL)`,
					`INSERT INTO download_grants VALUES('grant',0)`,
					`CREATE TABLE download_preparations(bytes_done INTEGER NOT NULL)`,
					`INSERT INTO download_preparations VALUES(0)`,
				} {
					if _, err = db.ExecContext(ctx, query); err != nil {
						t.Fatal(err)
					}
				}
				w, err := Begin(ctx, db, ClassForegroundTransfer)
				if err != nil {
					t.Fatal(err)
				}
				defer w.Rollback()
				before := AuthorityGeneration()
				if err = statement.run(ctx, w.Tx()); err != nil {
					t.Fatal(err)
				}
				afterStatement := AuthorityGeneration()
				if afterStatement != before+1 {
					t.Fatalf("authority statement generation = %d; want %d", afterStatement, before+1)
				}
				var allowed bool
				readGrant := func() {
					t.Helper()
					if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_grants WHERE hash='grant' AND revoked=0)`).Scan(&allowed); err != nil {
						t.Fatal(err)
					}
				}
				readGrant()
				if !allowed {
					t.Fatal("reader saw an uncommitted withdrawal")
				}
				finish := w.Rollback
				wantGeneration := afterStatement
				if commit {
					finish = w.Commit
					wantGeneration++
				}
				if err = finish(); err != nil {
					t.Fatal(err)
				}
				if got := AuthorityGeneration(); got != wantGeneration {
					t.Fatalf("%s generation = %d; want %d", ending, got, wantGeneration)
				}
				readGrant()
				if allowed == commit {
					t.Fatalf("grant allowed after %s = %v", ending, allowed)
				}
				// Reuse the single writer connection. Neither a read-only
				// transaction nor a progress write may inherit the dirty marker.
				for _, progress := range []bool{false, true} {
					clean, err := Begin(ctx, db, ClassForegroundTransfer)
					if err != nil {
						t.Fatal(err)
					}
					defer clean.Rollback()
					if progress {
						if _, err = clean.Tx().ExecContext(ctx, `UPDATE download_preparations SET bytes_done=bytes_done+1`); err != nil {
							t.Fatal(err)
						}
					}
					if err = clean.Commit(); err != nil {
						t.Fatal(err)
					}
					if got := AuthorityGeneration(); got != wantGeneration {
						t.Fatalf("ordinary transaction moved generation: %d; want %d", got, wantGeneration)
					}
				}
			})
		}
	}
}
