package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"testing"
	"time"
)

func TestN1MaintenanceFixturePublishesEachActualTerminal(t *testing.T) {
	// Protocol time, not a real-clock hold/deadline assertion. This isolates the
	// owner's publication discipline without running the concurrent CP algorithm.
	f := progressiveProtocolFixture(t, 1)
	f.owner.maintenance = true
	command, err := f.NextOriginal()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	commitProgressiveOriginal(t, f)
	check := func(tx *transaction) {
		t.Helper()
		view := n1BindWALReturn(f.owner.postReturn, start, start, time.Now())
		if view == nil || view.Error != "" || view.Observation == nil || f.owner.postReturn.SetupEntered != tx.setupEnteredAt {
			t.Fatal("owner return missing/unbound", view)
		}
		if _, err := f.owner.wal.RestartRisk(f.owner.executor.now()); err != nil {
			t.Fatal("returned owner left gate awaiting its sample", err)
		}
		previous := f.owner.postReturn
		if err := f.owner.observeReturned(tx); err != nil || f.owner.postReturn != previous {
			t.Fatal("same physical return sampled twice", err)
		}
	}
	check(f.turn.transaction)
	if err := f.Resolve(t); err != nil {
		t.Fatal(err)
	}
	check(f.resolveTurn.transaction)
	if err := f.AckAndSnapshot(t); err != nil {
		t.Fatal(err)
	}
	check(f.ackTurn.transaction)
	if !f.ready || f.acknowledged == nil || *f.acknowledged != command.identity.Sequence {
		t.Fatal("publication bypassed actual saved-original progression")
	}
}

func TestN1AdmissionPartialAndRefusalRemainCallBound(t *testing.T) {
	origin := time.Now()
	start, end := origin.Add(time.Nanosecond), origin.Add(4*time.Nanosecond)
	partial := &n1FixtureAdmission{Started: start, Returned: end, Error: "pre-admission sample unavailable"}
	value := n1BindAdmission(partial, origin, origin, end)
	if value == nil || value.Request != nil || value.Granted != nil || value.Refusal != nil || value.UsageAfter != nil || value.Error == "" {
		t.Fatal("missing phases fabricated", value)
	}
	if n1BindAdmission(partial, origin, end.Add(time.Nanosecond), end.Add(2*time.Nanosecond)) != nil {
		t.Fatal("later Resolve borrowed earlier admission")
	}
	f := progressiveProtocolFixture(t, 1)
	f.owner.maintenance = true
	if _, err := f.NextOriginal(); err != nil {
		t.Fatal(err)
	}
	// Deterministic real admission refusal: fund a memory-only holding from the
	// SAME class/partition. It owns no native reader/writer and changes no cap.
	request := f.owner.request
	request.Key.ID = "refusal-control"
	request.Writer = false
	request.Need[governor.WALBytes] = 0
	request.MaxHold = 0
	held := f.owner.governor.Try(request)
	if held.Grant == nil {
		t.Fatal("control holding", held.Refusal)
	}
	defer func() {
		if err := f.owner.authority.Retired(held.Grant); err != nil {
			t.Error(err)
		}
	}()
	before := time.Now()
	// Exercise the admission seam itself. The outer progressive joined guard
	// correctly refuses an unexpected same-class holding before it calls begin.
	if _, err := f.owner.begin(t, f.current); err == nil || !errors.Is(err, ErrRefused) {
		t.Fatal("actual refusal lost", err)
	}
	refused := n1BindAdmission(f.owner.admission, origin, before, time.Now())
	if refused == nil || refused.Request == nil || refused.Granted == nil || *refused.Granted || refused.Refusal == nil || refused.UsageBefore == nil || refused.UsageAfter == nil {
		t.Fatal("refusal decision collapsed", refused)
	}
}
