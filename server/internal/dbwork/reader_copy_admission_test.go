package dbwork

import (
	"context"
	"testing"
	"time"
)

func TestPressureReopensReadersBeforeCopyAndRetainsItsOwner(t *testing.T) {
	db := readerFixture(t)
	scope := readerScopeForTest(t, db)
	finish, drained, _ := scope.pause()
	if finish == nil {
		t.Fatal("could not reserve pressure owner")
	}
	defer finish(true)
	if err := waitReaderDrain(context.Background(), drained); err != nil {
		t.Fatal(err)
	}
	scope.resumePressureReaders()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatalf("copying kept new readers paused: %v", err)
	}
	if stats := scope.stats(); stats.AdmissionPaused {
		t.Fatal("readers remain paused after their old boundaries drained")
	}
	if another, _, _ := scope.pause(); another != nil {
		another(false)
		t.Fatal("reopening readers lost ownership of the in-flight checkpoint")
	}
	if drained := scope.pausePressureReaders(); drained == nil {
		t.Fatal("copy owner could not reserve the short reset drain")
	} else if err := waitReaderDrain(ctx, drained); err != nil {
		t.Fatal(err)
	}
	if !scope.stats().AdmissionPaused {
		t.Fatal("reset did not stop new snapshots from pinning copied WAL")
	}
	finish(false)
	if scope.pressureBackoff() <= 0 {
		t.Fatal("a failed reset after copying lost its retry backoff")
	}
}
