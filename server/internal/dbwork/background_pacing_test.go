package dbwork

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func pacingPressure(t *testing.T, active bool) {
	t.Helper()
	RegisterForegroundProbe(t.Name(), func() bool { return active })
	t.Cleanup(func() { RegisterForegroundProbe(t.Name(), nil) })
}

func TestBackgroundPacingNoPressureAndCancellation(t *testing.T) {
	pacingPressure(t, false)
	started := time.Now()
	for i := 0; i < 100; i++ {
		if !PaceBackground(context.Background(), started.Add(-time.Hour)) {
			t.Fatal("uncontended batch stopped")
		}
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("uncontended batches were paced: %v", elapsed)
	}
	RegisterForegroundProbe(t.Name(), func() bool { return true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- PaceBackground(ctx, time.Now().Add(-time.Hour)) }()
	select {
	case advanced := <-done:
		if advanced {
			t.Fatal("cancelled batch continued")
		}
	case <-time.After(time.Second):
		t.Fatal("background pause ignored cancellation")
	}
}

func TestBackgroundPacingReleasesWriterAndFinishesAllBatches(t *testing.T) {
	pacingPressure(t, true)
	db := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const batches = 24
	started := time.Now()
	for i := 0; i < batches; i++ {
		batchStarted := time.Now()
		if err := WithWriteTx(ctx, db, ClassBackgroundMedia, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO rows(value) VALUES('background')`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if WriteGate().Stats().Active {
			t.Fatal("committed batch retained writer gate")
		}
		if !PaceBackground(ctx, batchStarted) {
			t.Fatal("foreground pressure abandoned background work")
		}
		// Foreground writes must continue between the committed background
		// batches. Neither a pending timer nor its pressure signal owns a gate.
		if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO rows(value) VALUES('foreground')`); err != nil {
			t.Fatal(err)
		}
	}
	var background, foreground int
	if err := db.QueryRow(`SELECT count(*) FROM rows WHERE value='background'`).Scan(&background); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM rows WHERE value='foreground'`).Scan(&foreground); err != nil {
		t.Fatal(err)
	}
	if background != batches || foreground != batches {
		t.Fatalf("pacing lost work: background=%d foreground=%d", background, foreground)
	}
	t.Logf("committed all %d background and %d foreground batches in %v under continuous foreground pressure", background, foreground, time.Since(started))
}

func TestBackgroundPacingBoundsStaleBatchStart(t *testing.T) {
	pacingPressure(t, true)
	started := time.Now()
	if !PaceBackground(context.Background(), time.Time{}) {
		t.Fatal("old batch start abandoned work")
	}
	elapsed := time.Since(started)
	if elapsed < 40*time.Millisecond || elapsed > time.Second {
		t.Fatalf("stale start exceeded bounded pause: %v", elapsed)
	}
}

func TestBackgroundPacingAllowsWriterWhilePaused(t *testing.T) {
	entered := make(chan struct{}, 1)
	RegisterForegroundProbe(t.Name(), func() bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		return true
	})
	defer RegisterForegroundProbe(t.Name(), nil)
	db := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan bool, 1)
	go func() { finished <- PaceBackground(ctx, time.Now().Add(-time.Hour)) }()
	<-entered
	if _, err := ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO rows(value) VALUES('during pause')`); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-finished
}
