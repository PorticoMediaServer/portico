package httpapi

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
)

// Exercise the reader/SQLite commit boundary without HTTP fixture setup: the
// authorization snapshot must see the old grant after its withdrawal statement
// runs, then the first read after commit must discard that cached verdict.
func TestDownloadReaderReauthorizationBetweenWithdrawalAndCommit(t *testing.T) {
	for _, mutation := range []struct{ name, sql string }{
		{"revoke", `UPDATE download_grants SET revoked=1 WHERE hash='grant'`},
		{"delete", `DELETE FROM download_grants WHERE hash='grant'`},
	} {
		for _, commit := range []bool{true, false} {
			ending := "rollback"
			if commit {
				ending = "commit"
			}
			t.Run(mutation.name+"/"+ending, func(t *testing.T) {
				ctx := context.Background()
				db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "grants.db"), dbwork.DefaultPolicy())
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.Exec(`CREATE TABLE download_grants(hash TEXT PRIMARY KEY, revoked INTEGER NOT NULL)`); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`INSERT INTO download_grants VALUES('grant',0)`); err != nil {
					t.Fatal(err)
				}
				checks := 0
				source := &downloadTestSource{Reader: bytes.NewReader([]byte("private bytes"))}
				r := newDownloadReader(ctx, source, func() error {
					checks++
					snapshot, err := dbwork.BeginSnapshot(ctx, db)
					if err != nil {
						return err
					}
					defer snapshot.Rollback()
					var allowed bool
					if err = snapshot.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_grants WHERE hash='grant' AND revoked=0)`).Scan(&allowed); err != nil {
						return err
					}
					if !allowed {
						return downloads.ErrGrant
					}
					return snapshot.Commit()
				})
				defer r.Close()
				buf := make([]byte, 1)
				if n, err := r.Read(buf); n != 1 || err != nil || checks != 1 {
					t.Fatalf("initial read = %d, %v; checks=%d", n, err, checks)
				}
				w, err := dbwork.Begin(ctx, db, dbwork.ClassForegroundTransfer)
				if err != nil {
					t.Fatal(err)
				}
				defer w.Rollback()
				if _, err = w.Tx().ExecContext(ctx, mutation.sql); err != nil {
					t.Fatal(err)
				}
				// The statement invalidation forces a fresh snapshot. The writer
				// has not committed, so that snapshot still authorizes the grant.
				if n, err := r.Read(buf); n != 1 || err != nil || checks != 2 {
					t.Fatalf("uncommitted read = %d, %v; checks=%d", n, err, checks)
				}
				finish := w.Rollback
				if commit {
					finish = w.Commit
				}
				if err = finish(); err != nil {
					t.Fatal(err)
				}
				n, err := r.Read(buf)
				if commit {
					if n != 0 || !errors.Is(err, downloads.ErrGrant) || checks != 3 || source.reads != 2 || source.closes != 1 {
						t.Fatalf("post-commit read = %d, %v; checks=%d source reads=%d closes=%d", n, err, checks, source.reads, source.closes)
					}
				} else {
					if n != 1 || err != nil || checks != 2 || source.closes != 0 {
						t.Fatalf("post-rollback read = %d, %v; checks=%d closes=%d", n, err, checks, source.closes)
					}
					// A fresh check agrees with the cached verdict: rollback never
					// withdrew the committed grant or exposed the uncommitted one.
					dbwork.BumpAuthority()
					if n, err := r.Read(buf); n != 1 || err != nil || checks != 3 {
						t.Fatalf("fresh post-rollback read = %d, %v; checks=%d", n, err, checks)
					}
				}
			})
		}
	}
}
