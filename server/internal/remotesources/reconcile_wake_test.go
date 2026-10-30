package remotesources

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

func remoteWakePasses(t *testing.T) chan struct{} {
	t.Helper()
	signal := worker.NewSignal()
	stopWake := registerRemoteSourceWake(signal)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	passes := make(chan struct{}, 8)
	go func() {
		defer close(done)
		worker.Run(ctx, "test.remotesources.wake", signal, func(context.Context) time.Duration { passes <- struct{}{}; return 0 })
	}()
	<-passes
	t.Cleanup(func() { cancel(); <-done; stopWake() })
	return passes
}

func TestRemoteSourceWakeIgnoresTelemetryAndRetainsActualInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newObservationFixture(t)
	passes := remoteWakePasses(t)
	commit := func(query string) {
		t.Helper()
		if err := dbwork.WithWriteTx(context.Background(), f.s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
			t.Fatal(err)
		}
	}
	commit(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms) VALUES(0,'remote-wake-fixture',1,1,'available','',0,0)`)
	select {
	case <-passes:
		t.Fatal("unrelated telemetry woke source reconciliation")
	case <-time.After(40 * time.Millisecond):
	}
	for _, query := range []string{
		`INSERT INTO configuration(key,value) VALUES('remote-wake-fixture','changed')`,
		`UPDATE remote_sources SET state='offline' WHERE id='remote'`,
		`UPDATE jobs SET status='completed' WHERE id='scan'`,
		`UPDATE library_sources SET enabled=0`,
	} {
		commit(query)
		select {
		case <-passes:
		case <-time.After(2 * time.Second):
			t.Fatal("actual source input did not wake reconciliation", query)
		}
	}
}

func TestRemoteSourceRefreshDoesNotWakeItselfForUnchangedMounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newNativeFixture(t)
	passes := remoteWakePasses(t)
	if err := f.s.RefreshMounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-passes:
		t.Fatal("unchanged mount registry generated another source wake")
	case <-time.After(40 * time.Millisecond):
	}
}
