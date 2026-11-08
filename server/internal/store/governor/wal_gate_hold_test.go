package governor

import (
	"testing"
	"time"
)

// WALGate.mu guards in-memory state only. While a funded concurrent PASSIVE is
// physically admitted (the whole native interval, from admission to retirement),
// the mutex is FREE, and every read-only observation and writer-side query takes
// it only briefly. This pins the property a harness stall once appeared to
// contradict: no user-visible observation or admission waits for a PASSIVE.
func TestWALGateMutexIsFreeWhileConcurrentPassiveIsAdmitted(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	cp := f.a.tryCheckpoint(concurrentCPRequest(f))
	if cp.Grant == nil {
		t.Fatal(cp.Refusal)
	}
	retired := false
	defer func() {
		if !retired {
			retire(t, f.a, cp.Grant)
		}
	}()
	if !f.wal.mu.TryLock() {
		t.Fatal("WALGate.mu is held for the PASSIVE interval")
	}
	f.wal.mu.Unlock()
	f.wal.mu.init()
	now := f.clock.now()
	calls := map[string]func(){
		"Observation":            func() { _ = f.wal.Observation() },
		"ResetCount":             func() { _ = f.wal.ResetCount() },
		"LastRestartObservation": func() { _ = f.wal.LastRestartObservation() },
		"RestartRisk":            func() { _, _ = f.wal.RestartRisk(now) },
		"recoveryPressure":       func() { _ = f.wal.recoveryPressure(f.wal.state) },
	}
	for name, call := range calls {
		done := make(chan struct{})
		go func() { defer close(done); call() }()
		select {
		case <-done: // liveness under an admitted PASSIVE; no wall-clock threshold
		case <-time.After(30 * time.Second):
			t.Fatalf("%s waited behind the admitted PASSIVE", name)
		}
	}
	// The permitted W/CP pair: a writer is admitted while the CP is live.
	r := concurrentWriter(t, f)
	w := grant(t, f.g, r)
	retire(t, f.a, w)
	retire(t, f.a, cp.Grant)
	retired = true
}

// The same property across a real native PASSIVE: hammer the lock-taking queries
// while Steps run. Every call must complete (liveness) and the gate must stay
// consistent; hold time itself is covered structurally by the test above.
func TestWALGateQueriesCompleteWhileRealPassiveStepsRun(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	stop := make(chan struct{})
	finished := make(chan int, 1)
	go func() {
		n := 0
		for {
			select {
			case <-stop:
				finished <- n
				return
			default:
			}
			_ = f.wal.Observation()
			_ = f.wal.ResetCount()
			_ = f.wal.LastRestartObservation()
			n++
		}
	}()
	for i := 0; i < 5; i++ {
		out, err := f.cp.Step(t.Context(), concurrentCPRequest(f))
		if err != nil || out.Refusal != nil || !out.PhysicalSafe {
			close(stop)
			t.Fatal(out, err)
		}
		f.clock.ms.Add(60) // the 50 ms minimum start spacing
		f.sample(t)
	}
	close(stop)
	select {
	case n := <-finished:
		if n == 0 {
			t.Fatal("no gate query completed while PASSIVE steps ran")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a gate query waited behind a PASSIVE step")
	}
}
