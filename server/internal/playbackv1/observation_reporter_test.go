package playbackv1

import (
	"context"
	"testing"
	"time"
)

// reporter reports for sessions the way a playing device does (claim, then
// observations) and writes the latest samples at once, as the batch worker
// does when they fall due. Tests that assert a sample's durable effect use
// report; tests of the hit path use observe.
type reporter struct {
	t      testing.TB
	s      *Service
	c      Caller
	epochs map[string]string
	seq    map[string]int64
}

func newReporter(t testing.TB, s *Service, c Caller) *reporter {
	t.Cleanup(func() {
		// Let the batch worker exit with the test.
		if o := s.obs; o != nil {
			o.mu.Lock()
			o.entries = map[string]*observer{}
			o.wakeLocked()
			o.mu.Unlock()
		}
	})
	return &reporter{t: t, s: s, c: c, epochs: map[string]string{}, seq: map[string]int64{}}
}

// claim makes the reporter's device the session's observer (once).
func (r *reporter) claim(id string, generation int) (string, error) {
	if epoch := r.epochs[id]; epoch != "" {
		return epoch, nil
	}
	grant, err := r.s.ClaimObserver(context.Background(), r.c, id, ObserverClaim{Generation: generation})
	if err != nil {
		return "", err
	}
	r.epochs[id] = grant.Epoch
	return grant.Epoch, nil
}

// observe sends one sample. Epoch and seq are filled in; a zero monotonic
// clock defaults to one second per sample.
func (r *reporter) observe(id string, ob Observation) (ObservationAck, error) {
	epoch, err := r.claim(id, ob.Generation)
	if err != nil {
		return ObservationAck{}, err
	}
	r.seq[id]++
	ob.Epoch, ob.Seq = epoch, r.seq[id]
	if ob.MonotonicMs == 0 {
		ob.MonotonicMs = ob.Seq * 1000
	}
	return r.s.Observe(context.Background(), r.c, id, ob)
}

// report sends one sample and writes it before returning.
func (r *reporter) report(id string, ob Observation) error {
	if _, err := r.observe(id, ob); err != nil {
		return err
	}
	r.apply(id)
	return nil
}

// apply writes the listed sessions' unwritten samples now, in one batch, and
// fails the test if any is left unwritten.
func (r *reporter) apply(ids ...string) {
	r.t.Helper()
	r.applySamples(false, ids...)
}

// applyDue runs only work whose batch deadline has arrived, including
// lease-only renewals. Immediate replays must leave that deadline untouched.
func (r *reporter) applyDue(ids ...string) {
	r.t.Helper()
	r.applySamples(true, ids...)
}

func (r *reporter) applySamples(dueOnly bool, ids ...string) {
	r.t.Helper()
	o := r.s.observations()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var batch []*observer
		waiting := false
		o.mu.Lock()
		for _, id := range ids {
			switch e := o.entries[id]; {
			case e == nil:
			case e.busy:
				waiting = true
			case dueOnly && (e.due().IsZero() || e.due().After(r.s.now())):
			default:
				e.busy = true
				batch = append(batch, e)
			}
		}
		o.mu.Unlock()
		if len(batch) > 0 {
			r.s.applyObservationBatch(context.Background(), o, batch)
		}
		if !waiting {
			break
		}
		if time.Now().After(deadline) {
			r.t.Fatal("observation batch still running")
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, id := range ids {
		if e := o.entries[id]; e != nil && e.dirty && (!dueOnly || !e.due().After(r.s.now())) {
			r.t.Fatalf("observation for %s was not written (%d failed batches)", id, o.counts.batchesFailed.Load())
		}
	}
}
