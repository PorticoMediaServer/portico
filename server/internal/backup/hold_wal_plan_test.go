package backup

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// This is the source qualification of the narrow state-only plan. A schema
// change adding state indexes/triggers must reopen the proof, not silently
// inherit its four-frame envelope.
func TestBackupHoldPhaseSourceAndPhysicalWALBound(t *testing.T) {
	s, _, db := testService(t)
	var triggers int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND tbl_name='backup_blob_holds'`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if triggers != 0 {
		t.Fatal("hold state acquired trigger fanout")
	}
	var stateIndexes int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_index_list('backup_blob_holds') l JOIN pragma_index_info(l.name) i WHERE i.name='state'`).Scan(&stateIndexes); err != nil {
		t.Fatal(err)
	}
	if stateIndexes != 0 {
		t.Fatal("hold state acquired index fanout")
	}
	ctx := context.Background()
	limits := copyLimitsFor(KindManual)
	if err := s.holdBlobs(ctx, limits, "writer-plan", "writer-scratch"); err != nil {
		t.Fatal(err)
	}
	defer s.releaseHold("writer-plan")
	var pageSize int64
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	before := dbwork.WALReservations(db)
	for _, state := range []string{"files", "publishing"} {
		if err := s.advanceHold(ctx, limits, "writer-plan", state); err != nil {
			t.Fatal(err)
		}
		if n, got := holdRows(t, db); n != 1 || got != state {
			t.Fatalf("%d %s", n, got)
		}
	}
	after := dbwork.WALReservations(db)
	if runtime.GOOS != "linux" {
		t.Skip("native accounting is qualified only on Linux unix VFS; state transitions above remain functional")
	}
	if after.QualifiedWrites-before.QualifiedWrites != 2 {
		t.Fatalf("source-qualified transitions not admitted: before %+v after %+v", before, after)
	}
	if after.UnknownIO != before.UnknownIO || after.MeasuredBytes <= before.MeasuredBytes {
		t.Fatal("actual WAL I/O was not measured")
	}
	if after.Overruns != before.Overruns || after.MeasuredBytes-before.MeasuredBytes > uint64(2*(32+4*(pageSize+24))) {
		t.Fatalf("source proof exceeded: before %+v after %+v", before, after)
	}
}

func TestBackupHoldCapacityRefusalKeepsPressureAndCancellationDistinct(t *testing.T) {
	ctx := context.Background()
	if e := holdError(ctx, dbwork.ErrWriterCapacity); !errors.Is(e, ErrBackupPressure) {
		t.Fatal("hold refused as an unclassified storage error", e)
	}
	if e := holdError(ctx, context.DeadlineExceeded); !errors.Is(e, ErrBackupPressure) {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e := holdError(cancelled, context.Canceled); !errors.Is(e, context.Canceled) || errors.Is(e, ErrBackupPressure) {
		t.Fatal("cancellation became pressure", e)
	}
	storage := errors.New("unrelated storage failure")
	if e := holdError(ctx, storage); !errors.Is(e, storage) {
		t.Fatal("storage failure swallowed", e)
	}
}
