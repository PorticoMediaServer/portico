package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func TestArtworkSchemaOpenBreakdown(t *testing.T) {
	_, source, _ := repairFixture(t)
	ctx := context.Background()
	report := func(label string, db *sql.DB) {
		t.Helper()
		var rows, triggers, bytes, triggerBytes int64
		if err := db.QueryRow(`SELECT count(*),sum(type='trigger'),sum(length(CAST(sql AS BLOB))),sum(CASE WHEN type='trigger' THEN length(CAST(sql AS BLOB)) ELSE 0 END) FROM sqlite_schema`).Scan(&rows, &triggers, &bytes, &triggerBytes); err != nil {
			t.Fatal(err)
		}
		t.Logf("ARTWORK-SCHEMA variant=%s rows=%d triggers=%d sql_bytes=%d trigger_sql_bytes=%d", label, rows, triggers, bytes, triggerBytes)
	}
	report("source", source)
	var triggerNames []string
	rows, err := source.Query(`SELECT name FROM sqlite_schema WHERE type='trigger' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		triggerNames = append(triggerNames, name)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, variant := range []string{"full", "without-triggers"} {
		path := filepath.Join(t.TempDir(), variant+".sqlite")
		paths[variant] = path
		conn, err := source.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = conn.Raw(func(raw any) error {
			backup, err := dbwork.RawConn(raw).(interface {
				NewBackup(string) (*sqlite.Backup, error)
			}).NewBackup(path)
			if err != nil {
				return err
			}
			defer backup.Finish()
			more, err := backup.Step(-1)
			if err != nil {
				return err
			}
			if more {
				return fmt.Errorf("fixture backup incomplete")
			}
			return nil
		})
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Change only the disposable diagnostic copy, through a real gated writer.
	stripped, err := dbwork.OpenHandle(paths["without-triggers"], dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	stripCtx := dbwork.WithPlan(ctx, dbwork.WritePlan{Name: "diagnostic-trigger-copy", Why: "remove triggers from a disposable fixture copy for parse-cost attribution"})
	err = dbwork.WithWriteTx(stripCtx, stripped, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, name := range triggerNames {
			if _, err := tx.ExecContext(ctx, `DROP TRIGGER "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	report("without-triggers", stripped)
	if err = stripped.Close(); err != nil {
		t.Fatal(err)
	}
	// Alternate fresh connections to avoid ordering one variant after all others.
	for sample := 0; sample < 6; sample++ {
		for _, variant := range []string{"full", "without-triggers"} {
			dsn, err := dbwork.DefaultPolicy().DSN(paths[variant])
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open(dbwork.DriverName, dsn)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			conn, err := db.Conn(ctx)
			opened := time.Since(started)
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			var n int
			queryStarted := time.Now()
			err = conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema`).Scan(&n)
			queried := time.Since(queryStarted)
			conn.Close()
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("ARTWORK-SCHEMA sample=%d variant=%s open=%s query=%s rows=%d", sample, variant, opened, queried, n)
		}
	}
}
