package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestReadingClosureRemovalDrainsBeforeRunAndSourceDeletion(t *testing.T) {
	for _, kind := range []string{"library", "source"} {
		t.Run(kind, func(t *testing.T) {
			c := catalogtest.Open(t)
			c.Library("closure-removal", "Reading", "book", "/read")
			c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('closure-run','closure-removal','cancelled','now')`)
			c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) SELECT 'closure-run',id,generation,incarnation,root_identity,1 FROM library_sources WHERE library_id='closure-removal'`)
			var source string
			if e := c.DB.QueryRow(`SELECT source_id FROM inventory_runs WHERE job_id='closure-run'`).Scan(&source); e != nil {
				t.Fatal(e)
			}
			c.Exec(`INSERT INTO inventory_reading_jobs(job_id,source_id,incarnation,generation,root_identity,root) SELECT job_id,source_id,root_incarnation,source_generation,root_identity,'/read' FROM inventory_runs`)
			for i := 0; i < 140; i++ {
				key := fmt.Sprintf("A%03d", i)
				c.Exec(`INSERT INTO inventory_reading_directories(job_id,relative_path,parent_path,top_level) VALUES('closure-run',?,'.',?)`, key, key)
				c.Exec(`INSERT INTO inventory_reading_seals(job_id,top_level,source_id,incarnation,generation,root_identity,root,coverage_revision,issue_revision,issue_highwater) SELECT job_id,?,source_id,incarnation,generation,root_identity,root,0,0,0 FROM inventory_reading_jobs`, key)
			}
			if kind == "library" {
				c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='closure-removal'`)
			} else {
				c.Exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, source)
			}
			for i := 0; i < 20 && rows(t, c, `SELECT count(*) FROM inventory_reading_jobs`) > 0; i++ {
				before := rows(t, c, `SELECT count(*) FROM inventory_reading_directories`) + rows(t, c, `SELECT count(*) FROM inventory_reading_seals`)
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					if kind == "library" {
						_, e := removeLibraryBookkeepingTx(ctx, tx, "closure-removal")
						return e
					}
					_, e := removeFolderBatchTx(ctx, tx, source)
					return e
				})
				after := rows(t, c, `SELECT count(*) FROM inventory_reading_directories`) + rows(t, c, `SELECT count(*) FROM inventory_reading_seals`)
				if before-after > 64 {
					t.Fatalf("proof removal exceeded bound: %d", before-after)
				}
			}
			if rows(t, c, `SELECT count(*) FROM inventory_reading_jobs`) > 0 {
				t.Fatal("proof FK blocks deletion")
			}
			if kind == "library" {
				removeLibraryFully(t, c, "closure-removal", false)
			} else {
				c.Exec(`DELETE FROM inventory_runs WHERE job_id='closure-run'`)
				c.Exec(`DELETE FROM jobs WHERE id='closure-run'`)
				c.Exec(`DELETE FROM library_sources WHERE id=?`, source)
			}
			if rows(t, c, `SELECT count(*) FROM inventory_reading_seals`)+rows(t, c, `SELECT count(*) FROM inventory_reading_directories`) > 0 {
				t.Fatal("orphan proofs")
			}
		})
	}
}
