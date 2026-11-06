package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestGovernedTurnMissingObserverClockInvalidatesOldSample(t *testing.T) {
	clock := time.Now()
	g, a, wal, request := governedFixture(t, func() time.Time { return clock })
	if err := wal.Publish(wal.Observation(), governor.WALState{PageSize: 4096}, clock); err != nil {
		t.Fatal(err)
	}
	observation, err := observeWriterWAL("not-read-without-clock", wal, nil)
	if !errors.Is(err, ErrInvalid) || observation != nil {
		t.Fatal("missing clock did not refuse", observation, err)
	}
	decision := g.Try(request)
	if decision.Grant != nil {
		_ = a.Retired(decision.Grant)
	}
	if decision.Grant != nil || decision.Refusal == nil || decision.Refusal.Code != governor.FailSafe {
		t.Fatal("missing clock left old evidence admissible", decision)
	}
}
