package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestCatalogIngestWindowHoldsOnlyTaggedBackgroundBeforeBEGIN(t *testing.T) {
	db := testDB(t)
	token, started := RequestCatalogIngestWindow(db, time.Minute)
	if !started {
		t.Fatal("window not started")
	}
	defer ForgetCatalogIngestWindow(db, token)
	tagged := WithCatalogIngest(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ExecWrite(tagged, db, ClassBackgroundMedia, `INSERT INTO rows VALUES(1000,'resumed')`)
		done <- err
	}()
	// Every foreground class bypasses the hold, even if a caller inherited a tag.
	for _, class := range []Class{ClassSecurityFence, ClassProtectedCapture, ClassEstablishedPlayback, ClassPlaybackStart, ClassInteractive, ClassForegroundTransfer} {
		ctx, cancel := context.WithTimeout(tagged, time.Second)
		_, err := ExecWrite(ctx, db, class, `INSERT INTO rows(value) VALUES('foreground')`)
		cancel()
		if err != nil {
			t.Fatalf("foreground %v was held: %v", class, err)
		}
	}
	if _, err := ExecWrite(context.Background(), db, ClassMaintenance, `INSERT INTO rows(value) VALUES('derived')`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("ingest entered window: %v", err)
	default:
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM rows WHERE value='resumed'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("held writer mutated", n, err)
	}
	ReleaseCatalogIngestWindow(db, token)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM rows WHERE value='resumed'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("ingest not resumed exactly once", n, err)
	}
}
func TestCatalogIngestWindowCapsCannotExtendAndCancelsCleanly(t *testing.T) {
	db := testDB(t)
	began := time.Now()
	token, started := RequestCatalogIngestWindow(db, time.Hour)
	if !started {
		t.Fatal("window not started")
	}
	defer ForgetCatalogIngestWindow(db, token)
	catalogIngestWindows.Lock()
	first := catalogIngestWindows.byDB[db].until
	catalogIngestWindows.Unlock()
	if first.Sub(began) > CatalogIngestWindowLimit+time.Second {
		t.Fatal("window exceeded cap")
	}
	for i := 0; i < 100; i++ {
		next, created := RequestCatalogIngestWindow(db, time.Minute)
		if created || next != token {
			t.Fatal("demand extended active window")
		}
	}
	catalogIngestWindows.Lock()
	last := catalogIngestWindows.byDB[db].until
	catalogIngestWindows.Unlock()
	if !first.Equal(last) {
		t.Fatal("active deadline moved")
	}
	ctx, cancel := context.WithTimeout(WithCatalogIngest(context.Background()), 10*time.Millisecond)
	defer cancel()
	_, err := ExecWrite(ctx, db, ClassBackgroundMedia, `INSERT INTO rows VALUES(1,'cancelled')`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("held cancellation", err)
	}
	ReleaseCatalogIngestWindow(db, token)
	if next, started := RequestCatalogIngestWindow(db, time.Minute); started || next != 0 {
		t.Fatal("ingest recovery interval was skipped")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM rows`).Scan(&n)
	if n != 0 {
		t.Fatal("cancelled pre-BEGIN write changed rows")
	}
}
func TestCatalogIngestQueuedBeforeWindowYieldsToDerivation(t *testing.T) {
	db := testDB(t)
	release, err := gate.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	go func() {
		returnErr := WithWriteTx(WithCatalogIngest(context.Background()), db, ClassBackgroundMedia, func(tx *sql.Tx) error { _, err := tx.Exec(`INSERT INTO rows VALUES(1,'source')`); return err })
		queued <- returnErr
	}()
	deadline := time.Now().Add(time.Second)
	for gate.Waiting()[ClassBackgroundMedia.String()] == 0 {
		if time.Now().After(deadline) {
			release()
			t.Fatal("source never queued")
		}
		time.Sleep(time.Millisecond)
	}
	token, _ := RequestCatalogIngestWindow(db, time.Minute)
	defer ForgetCatalogIngestWindow(db, token)
	release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := ExecWrite(ctx, db, ClassMaintenance, `INSERT INTO rows VALUES(2,'derived')`); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM rows WHERE id=1`).Scan(&n)
	if n != 0 {
		t.Fatal("queued ingest slipped through hold before BEGIN")
	}
	ReleaseCatalogIngestWindow(db, token)
	if err := <-queued; err != nil {
		t.Fatal(err)
	}
}

func TestCatalogIngestWindowThroughputResumesWithoutDroppedWrites(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`INSERT INTO rows VALUES(1,'0')`); err != nil {
		t.Fatal(err)
	}
	tagged := WithCatalogIngest(context.Background())
	run := func(n int) time.Duration {
		began := time.Now()
		for i := 0; i < n; i++ {
			if _, err := ExecWrite(tagged, db, ClassBackgroundMedia, `UPDATE rows SET value=CAST(value AS INTEGER)+1 WHERE id=1`); err != nil {
				t.Fatal(err)
			}
		}
		return time.Since(began)
	}
	baseline := run(100)
	token, _ := RequestCatalogIngestWindow(db, 50*time.Millisecond)
	defer ForgetCatalogIngestWindow(db, token)
	held := run(100)
	var count int
	if err := db.QueryRow(`SELECT CAST(value AS INTEGER) FROM rows WHERE id=1`).Scan(&count); err != nil || count != 200 {
		t.Fatalf("source checkpoint lost/repeated writes: %d %v", count, err)
	}
	if CatalogIngestWindowActive(db) {
		t.Fatal("source failed to resume at deadline")
	}
	if next, started := RequestCatalogIngestWindow(db, time.Minute); started || next != 0 {
		t.Fatal("throughput recovery interval not preserved")
	}
	t.Logf("100 source writes idle=%s; 100 with one 50 ms pause=%s; all 200 committed exactly once; progress-renewed hold <=10min, recovery >=4min", baseline, held)
}

func TestCatalogIngestProgressExtendsOnlyUntilOverallCap(t *testing.T) {
	db := testDB(t)
	token, _ := RequestCatalogIngestWindow(db, time.Millisecond)
	defer ForgetCatalogIngestWindow(db, token)
	catalogIngestWindows.Lock()
	w := catalogIngestWindows.byDB[db]
	w.began = time.Now().Add(-CatalogIngestOverallLimit + time.Second)
	w.until = time.Now().Add(100 * time.Millisecond)
	cap := w.began.Add(CatalogIngestOverallLimit)
	catalogIngestWindows.Unlock()
	ProgressCatalogIngestWindow(db, token)
	catalogIngestWindows.Lock()
	until, next := w.until, w.next
	catalogIngestWindows.Unlock()
	if !until.Equal(cap) || next.Before(cap.Add(4*time.Minute)) {
		t.Fatal("progress escaped cap/recovery", until, next, cap)
	}
	catalogIngestWindows.Lock()
	w.until = time.Now().Add(-time.Second)
	ended := w.until
	catalogIngestWindows.Unlock()
	ProgressCatalogIngestWindow(db, token)
	catalogIngestWindows.Lock()
	unchanged := w.until.Equal(ended)
	catalogIngestWindows.Unlock()
	if !unchanged || CatalogIngestWindowActive(db) {
		t.Fatal("late progress resurrected expired window")
	}
}
