// Package waketest checks the SQLite commit-notification boundary only. Its
// scalar tables model change events, not a business schema or worker grant.
package waketest

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

func Inputs(t *testing.T, subscribe func(*worker.Signal) func(), relevant ...string) {
	t.Helper()
	db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "wake.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	names := append([]string{"telemetry_rollups"}, relevant...)
	valid := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for _, name := range names {
		if !valid.MatchString(name) {
			t.Fatal("invalid test table", name)
		}
		if _, err = db.Exec(`CREATE TABLE ` + name + `(value INTEGER NOT NULL);INSERT INTO ` + name + ` VALUES(1)`); err != nil {
			t.Fatal(err)
		}
	}
	if err = dbwork.InstallWakeDependencies(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	signal := worker.NewSignal()
	stop := subscribe(signal)
	defer stop()
	write := func(name string) {
		t.Helper()
		if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			statement, err := tx.Prepare(`UPDATE ` + name + ` SET value=value+1`)
			if err != nil {
				return err
			}
			defer statement.Close()
			_, err = statement.Exec()
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("telemetry_rollups")
	if signal.Take() {
		t.Fatal("unrelated telemetry woke runtime")
	}
	for _, name := range relevant {
		t.Run(name, func(t *testing.T) {
			write(name)
			if !signal.Take() {
				t.Fatal("actual committed input did not wake runtime", name)
			}
		})
	}
	stop()
	if len(relevant) > 0 {
		write(relevant[0])
		if signal.Take() {
			t.Fatal("retired runtime retained commit subscription")
		}
	}
}
