package playbackv1

import "context"

func (o *observations) acquireWork(ctx context.Context) error {
	o.workOnce.Do(func() { o.work = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case o.work <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *observations) releaseWork() { <-o.work }

// FlushObservations writes one bounded pass over the currently pending owners.
// Ticks may continue during a backup; this does not chase an endless stream of
// new samples or promise crash persistence. Each batch uses the ordinary live
// authority checks and failed evidence stays pending for retry.
func (s *Service) FlushObservations(ctx context.Context) error {
	o := s.observations()
	if err := o.acquireWork(ctx); err != nil {
		return err
	}
	defer o.releaseWork()
	o.mu.Lock()
	pending := make([]*observer, 0, len(o.entries))
	for _, e := range o.entries {
		if e.dirty || e.credited() != e.applied {
			pending = append(pending, e)
		}
	}
	o.mu.Unlock()
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(pending), observationBatch)
		batch := pending[:n]
		o.mu.Lock()
		for _, e := range batch {
			e.busy = true
		}
		o.mu.Unlock()
		if err := s.applyObservationBatch(ctx, o, batch); err != nil {
			return err
		}
		pending = pending[n:]
	}
	return nil
}

// CloseObservations stops admission and joins the background worker, then tries
// to save the unwritten window. Call after HTTP drain and before DB close. A
// failed/timed-out flush is reported; it never counts evidence as committed.
func (s *Service) CloseObservations(ctx context.Context) error {
	o := s.observations()
	o.mu.Lock()
	o.closed = true
	if o.workerCancel != nil {
		o.workerCancel()
	}
	done := o.workerDone
	o.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.FlushObservations(ctx)
}
