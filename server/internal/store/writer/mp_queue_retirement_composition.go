package writer

import "context"

// Closed same-turn physical effect for a queue mutation invalidating its
// prepared successor. It runs before the queue/next cancellation component;
// the actual parent alone receives a receipt. No descriptor is borrowed or
// changed and no separate physical worker sequence is consumed here.
type mpQueuePreparedRetirement struct {
	queue, next       string
	revision, custody int64
	physical          mpPhysicalFence
}

func mpQueuePreparedRetirementPurpose(kind CommandID) bool {
	switch kind {
	case mpQueueEdit, MPQueueRepeat, mpQueueSettings, mpQueueRetire, mpQueueSave,
		mpQueueAdvance, mpPrepareNext, mpCommitNext, mpCancelNext:
		return true
	}
	return false
}

func composeMPQueuePreparedRetire(ctx context.Context, t *commandTurn, q mpQueuePreparedRetirement) (out mpSessionRetireProjection, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || !mpQueuePreparedRetirementPurpose(t.transaction.descriptor.ID) || !mpID(q.queue) || !mpID(q.next) || !mpPrepTexts(q.physical.retireRequest) {
		return out, ErrInvalid
	}
	f := q.physical
	if !f.valid() || !f.actor.valid(f.nowMS) {
		return out, ErrInvalid
	}
	// Current W disclosure/classification remains prerequisite. This additional
	// native actor check binds the selected queue and original occurrence.
	if err = checkMPPrepActor(ctx, t, f.actor); err != nil {
		return
	}
	h, e := mpLoadQueue(ctx, t, q.queue)
	if e != nil {
		return out, e
	}
	if err = mpRequireRevision(h, q.revision, q.custody); err != nil {
		return
	}
	if h.ownerKind != "device" || h.owner != f.actor.device || h.authority != f.actor.authority || h.account != f.actor.account || h.profile != f.actor.profile {
		return out, ErrDisclosureDenied
	}
	p, e := mpLoadPrepared(ctx, t, q.queue, q.next)
	if e != nil {
		return out, e
	}
	if p.state != "prepared" || p.media != f.media || p.mediaGeneration != f.generation || p.device != f.actor.device || p.family != f.actor.family {
		return out, ErrConflict
	}
	physical, e := captureMPPhysical(ctx, t, f, false)
	if e != nil {
		return out, e
	}
	if physical.exit.retiredGeneration == f.generation {
		if physical.exit.retirementRequest != f.retireRequest {
			return out, ErrConflict
		}
		return mpRetireProjection(physical), nil
	}
	if physical.exited || physical.cleaned {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.session.request_stop", f.retireRequest, f.media, f.revision, f.generation, f.execution); err != nil {
		return
	}
	physical.revision++
	physical.state = "stopped"
	physical.grantRetired = true
	physical.exit.retiredGeneration = f.generation
	physical.exit.retirementRequest = f.retireRequest
	return mpRetireProjection(physical), nil
}
