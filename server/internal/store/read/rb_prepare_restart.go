package read

import "errors"

// rbPrepareRestartLimit is the F-04 restart budget: an attempt that observed a
// moved content revision may be re-run twice, and no more. The original 12s job
// lifetime, owner, physical governor grant and pinned authorization context are
// never replaced or extended by a restart.
const rbPrepareRestartLimit = 2

// errRBPrepareContent marks a producer attempt that completed its bounded raw
// walk but found the source content revision had moved away from the snapshot it
// was reducing. It is NOT a security, authorization or lease invalidation: those
// win immediately and are never restartable. The attempt supplies the fresh
// fence it observed so the wrapper can prove the pinned context is unchanged.
var errRBPrepareContent = errors.New("prepared source content changed")

type rbPrepareRestartOutcome struct {
	// Attempts counts producer invocations, including the first one.
	Attempts int
	// Restarts counts content-invalidated attempts that were discarded and re-run.
	Restarts int
}

// rbPrepareProducer performs ONE bounded content attempt against the admitted
// entry and returns the fence it observed, even on success. A nil error means
// the attempt captured, retained and finished the entry. errRBPrepareContent
// asks the wrapper for another attempt from the beginning of the source.
type rbPrepareProducer func(attempt int) (browseFence, error)

// rbPrepareRestart wraps a content producer over ONE existing preparation
// entry. When a producer reports that its content revision moved, the wrapper
// discards every value from that attempt and runs the producer again, at most
// rbPrepareRestartLimit times.
//
// Restart deliberately does NOT call BeginPreparation: the owner, dependency
// footprint, reserved payload, governor lease and original absolute deadline of
// p all survive unchanged. A security, policy, viewer, lifetime or structural
// fence change is a denial rather than a content restart, and no already
// delivered result can be restarted. A new attempt cannot publish stale values
// because the entry is emptied and re-captured before it can become ready. Only
// a nil producer result is deliverable; a producer that fails must not leave the
// entry ready.
func rbPrepareRestart(p *rbPreparedEntry, pinned browseFence, produce rbPrepareProducer) (rbPrepareRestartOutcome, error) {
	var out rbPrepareRestartOutcome
	if p == nil || p.cache == nil || p.cache.a == nil || produce == nil {
		return out, ErrInput
	}
	for {
		// Same ctx, deadline and lease check as every other preparation quantum.
		if err := p.restartCheck(); err != nil {
			return out, err
		}
		fresh, attemptErr := produce(out.Attempts)
		out.Attempts++
		if attemptErr != nil && !errors.Is(attemptErr, errRBPrepareContent) {
			return out, attemptErr
		}
		if !rbPreparePinnedFenceEqual(pinned, fresh) {
			// Viewer/policy/lifetime/source structure changed. Content restarts
			// are not an authorization refresh; discard the values.
			_ = p.close()
			return out, ErrDenied
		}
		if attemptErr == nil {
			return out, nil
		}
		if out.Restarts >= rbPrepareRestartLimit {
			// Never leave a partial reduction that a caller could mistake for a
			// complete result.
			_ = p.close()
			return out, attemptErr
		}
		if err := p.restartReset(fresh); err != nil {
			return out, err
		}
		out.Restarts++
	}
}

func (p *rbPreparedEntry) restartCheck() error {
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	return p.checkLocked()
}

// restartReset discards an invalidated attempt while keeping the physical
// governor grant. The retained vector capacity is reused; nothing is re-begun,
// re-sized or re-deadlined. The next attempt starts from the fresh content
// revision already observed, so the payload is not reallocated.
func (p *rbPreparedEntry) restartReset(fresh browseFence) error {
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if err := p.checkLocked(); err != nil {
		return err
	}
	if p.delivered {
		// The first authorized handoff already published values. A later page
		// must fail its fresh fence match rather than replay a restart.
		return ErrStale
	}
	p.ids = p.ids[:0]
	p.facets = p.facets[:0]
	p.total = 0
	p.complete = false
	if p.captured {
		p.fence = fresh
	} else {
		p.fence = browseFence{}
	}
	return nil
}

// rbPreparePinnedFenceEqual proves a restart observed the same authorization
// context. Only content revision counters may advance; every identity, security,
// policy, lifetime, personal and structural field must match exactly.
func rbPreparePinnedFenceEqual(pinned, fresh browseFence) bool {
	pinned.dataRevision, fresh.dataRevision = 0, 0
	pinned.orderRevision, fresh.orderRevision = 0, 0
	pinned.metadataRevision, fresh.metadataRevision = 0, 0
	pinned.ageRevision, fresh.ageRevision = 0, 0
	return pinned == fresh
}
