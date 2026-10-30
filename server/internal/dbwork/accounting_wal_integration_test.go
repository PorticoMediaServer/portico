package dbwork_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"runtime"
	"testing"
)

func TestAccountingWALUsesCommittedNativeWriterCounters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, e := persistence.OpenFresh(filepath.Join(t.TempDir(), "accounting-wal.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dbwork.ResetPlans()
	t.Cleanup(dbwork.ResetPlans)
	e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		dbwork.ObserveAccountingWAL(tx, dbwork.AccountingFlip)
		_, e := tx.Exec(`INSERT INTO configuration(key,value) VALUES('wal-metric','actual')`)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	first := dbwork.Plans().MusicBrainzWAL
	if first.Flip.Samples != 1 || first.Fold.Samples != 0 || first.Flip.MaxCachePages == 0 {
		t.Fatal(first)
	}
	if runtime.GOOS == "linux" && (first.Flip.UnknownWAL != 0 || first.Flip.UnknownFrames != 0 || first.Flip.MaxWALBytes == 0 || first.Flip.MaxFrames == 0) {
		t.Fatal("native I/O not observed", first)
	}
	refused := errors.New("rollback telemetry fixture")
	e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		dbwork.ObserveAccountingWAL(tx, dbwork.AccountingFold)
		if _, e := tx.Exec(`UPDATE configuration SET value='rolled-back' WHERE key='wal-metric'`); e != nil {
			return e
		}
		return refused
	})
	if !errors.Is(e, refused) {
		t.Fatal(e)
	}
	if got := dbwork.AccountingWAL(); got.Fold.Samples != 0 || got.Flip != first.Flip {
		t.Fatal("rollback counted as accepted phase", got)
	}
	e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		dbwork.ObserveAccountingWAL(tx, dbwork.AccountingFold)
		_, e := tx.Exec(`UPDATE configuration SET value='folded' WHERE key='wal-metric'`)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if got := dbwork.AccountingWAL(); got.Fold.Samples != 1 || got.Fold.MaxCachePages == 0 {
		t.Fatal(got)
	}
}
