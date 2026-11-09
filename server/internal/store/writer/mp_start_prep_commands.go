package writer

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// No public constructor. Trusted composition binds a current ordinary native
// authentication to this exact writer and device. Copies are dead. A historical
// UO root reference is never disclosure or command authority.
type mpStartPrepDisclosure struct {
	self        *mpStartPrepDisclosure
	writer      *Writer
	lifetime    context.Context
	revoked     atomic.Bool
	actor       mpPrepActor
	library     string
	incarnation [16]byte
}

func (d *mpStartPrepDisclosure) Actor() mpPrepActor { return d.actor }

type mpStartPrepBeginInput struct {
	birth              mpStartPrepUOBirth
	id, item           string
	keyDigest          string
	bodyDigest         string
	part               int64
	deadlineMS         int64
	tokenExpiresMS     int64
	replaces           string
	replacesGeneration int64
	actor              mpPrepActor
	source             mpPrepSource
	queue              mpPrepQueue
	results            mpStartPrepStableIDs
}

func (q mpStartPrepBeginInput) valid() bool {
	return q.birth.valid() && mpPrepTexts(q.id, q.item) && lowerHex(q.keyDigest, 64) &&
		lowerHex(q.bodyDigest, 64) && q.part >= 0 && q.deadlineMS > 0 && q.tokenExpiresMS >= q.deadlineMS &&
		q.actor.valid(0) && q.source.valid() && q.queue.valid() && q.results.valid() &&
		mpPrepText(q.replaces, true) && (q.replaces == "") == (q.replacesGeneration == 0) && q.replacesGeneration >= 0
}

type mpStartPrepClaimInput struct {
	fence     mpStartPrepFence
	execution string
}

func (q mpStartPrepClaimInput) valid() bool {
	return q.fence.valid() && q.fence.execution == "" && mpPrepTexts(q.execution)
}

type mpStartPrepSealInput struct {
	fence      mpStartPrepFence
	completion mpStartPrepCompletion
}

func (q mpStartPrepSealInput) valid() bool {
	return q.fence.valid() && q.fence.execution != "" && q.completion.valid() && q.completion.execution == q.fence.execution
}

type mpStartPrepCommand struct {
	kind      CommandID
	begin     *mpStartPrepBeginInput
	claim     *mpStartPrepClaimInput
	seal      *mpStartPrepSealInput
	reference ReceiptReference
}

func newMPStartPrepCommand(kind CommandID, id CommandIdentity, payload any) (*mpStartPrepCommand, error) {
	if id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) || id.Sequence <= 0 {
		return nil, ErrInvalid
	}
	c := &mpStartPrepCommand{kind: kind}
	switch q := payload.(type) {
	case mpStartPrepBeginInput:
		if kind != MPStartPrepBegin || !q.valid() || id.Sequence != 1 || q.birth.incarnation != id.Incarnation {
			return nil, ErrInvalid
		}
		c.begin = &q
	case mpStartPrepClaimInput:
		q.fence.sourceWitnessed = true
		if kind != MPStartPrepClaim || !q.valid() {
			return nil, ErrInvalid
		}
		c.claim = &q
	case mpStartPrepSealInput:
		q.fence.sourceWitnessed = true
		if kind != MPStartPrepSeal || !q.valid() {
			return nil, ErrInvalid
		}
		c.seal = &q
	default:
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(kind, 1, id, payload)
	if err != nil {
		return nil, err
	}
	c.reference = ReceiptReference{Identity: id, RequestDigest: digest}
	return c, nil
}

func (c *mpStartPrepCommand) payload() any {
	switch {
	case c.begin != nil:
		return *c.begin
	case c.claim != nil:
		return *c.claim
	case c.seal != nil:
		return *c.seal
	}
	return nil
}

func (c *mpStartPrepCommand) owner() string {
	if c.begin != nil {
		return c.begin.id
	}
	if c.claim != nil {
		return c.claim.fence.id
	}
	if c.seal != nil {
		return c.seal.fence.id
	}
	return ""
}

func (c *mpStartPrepCommand) device() string {
	switch {
	case c.begin != nil:
		return c.begin.actor.device
	case c.claim != nil:
		return c.claim.fence.actor.device
	case c.seal != nil:
		return c.seal.fence.actor.device
	}
	return ""
}

// A command-bearing invocation for the shared dispatcher. It carries no
// receipt capability, authority or exit proof.
type mpStartPrepInvocation struct {
	disclosure *mpStartPrepDisclosure
	command    *mpStartPrepCommand
}

func (*mpStartPrepInvocation) isWCCommandInvocation() {}

type mpStartPrepResult struct {
	Outcome ReceiptOutcome
	Value   mpStartPrepValue
	Digest  ResultDigest
}

func mpStartPrepWorkingBytes(v *mpStartPrepInvocation) uint64 {
	n := uint64(64 << 10)
	if v == nil || v.disclosure == nil {
		return n
	}
	s := v.disclosure.actor
	for _, x := range []string{s.server, s.authority, s.account, s.profile, s.device, s.family, s.grant, s.sessionHash, v.disclosure.library} {
		n += uint64(len(x)) * 32
	}
	if c := v.command; c != nil {
		if q := c.begin; q != nil {
			for _, x := range []string{q.id, q.item, q.keyDigest, q.bodyDigest, q.replaces, q.birth.birth.root, q.birth.birth.member, q.birth.birth.profileLifetime,
				q.source.library, q.source.source, q.source.asset, q.source.object, q.source.revision, q.source.root, q.source.incarnation, q.source.remote,
				q.source.configuration, q.source.policy, q.source.authority, q.queue.id, q.queue.entry, q.queue.ownerKind, q.queue.ownerID,
				q.results.session, q.results.legacySession, q.results.snapshot} {
				n += uint64(len(x)) * 32
			}
		}
		if q := c.claim; q != nil {
			n += mpStartPrepFenceBytes(q.fence)
			n += uint64(len(q.execution)) * 32
		}
		if q := c.seal; q != nil {
			n += mpStartPrepFenceBytes(q.fence)
			n += uint64(len(q.completion.execution)+len(q.completion.resultDigest)) * 32
		}
	}
	return n
}

func mpStartPrepFenceBytes(f mpStartPrepFence) uint64 {
	n := uint64(len(f.id)+len(f.execution)) * 32
	for _, x := range []string{f.actor.server, f.actor.authority, f.actor.account, f.actor.profile, f.actor.device, f.actor.family, f.actor.grant, f.actor.sessionHash,
		f.source.library, f.source.source, f.source.asset, f.source.object, f.source.revision, f.source.root, f.source.incarnation, f.source.remote,
		f.source.configuration, f.source.policy, f.source.authority, f.queue.id, f.queue.entry, f.queue.ownerKind, f.queue.ownerID} {
		n += uint64(len(x)) * 32
	}
	return n
}

func checkMPStartPrepDisclosure(w *Writer, v *mpStartPrepInvocation) error {
	if v == nil || v.disclosure == nil || v.command == nil {
		return ErrDisclosureDenied
	}
	d := v.disclosure
	if d.self != d || d.writer != w || w == nil || w.effective == nil || d.lifetime == nil || d.lifetime.Err() != nil || d.revoked.Load() || d.incarnation != v.command.reference.Identity.Incarnation {
		return ErrDisclosureDenied
	}
	if !mpPrepTexts(d.library) || d.actor != v.command.fenceActor() || d.actor.device != v.command.device() {
		return ErrDisclosureDenied
	}
	return nil
}

func (c *mpStartPrepCommand) fenceActor() mpPrepActor {
	switch {
	case c.begin != nil:
		return c.begin.actor
	case c.claim != nil:
		return c.claim.fence.actor
	case c.seal != nil:
		return c.seal.fence.actor
	}
	return mpPrepActor{}
}

func (w *Writer) runMPStartPrep(ctx context.Context, d *mpStartPrepDisclosure, kind CommandID, reference ReceiptReference, payload any) (out mpStartPrepResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if w == nil || w.effective == nil {
		return out, ErrInvalid
	}
	// The owner id (preparation) is part of every original request, so a
	// resolve-only call without its input is refused rather than guessed from a
	// receipt identity or a mutable head.
	if payload == nil {
		return out, ErrInvalid
	}
	rebuilt, e := newMPStartPrepCommand(kind, reference.Identity, payload)
	if e != nil || rebuilt.reference != reference {
		return out, errors.Join(ErrConflict, e)
	}
	v := &mpStartPrepInvocation{disclosure: d, command: rebuilt}
	result, _, err := w.effective.dispatchCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, false, nil, v)
	return result.mpStartPrep, err
}

// executeMPStartPrep is the sole MP start-preparation body: current disclosure,
// original receipt classification, saved original OR fresh exact effects, then
// the MP result/debt rows in the SAME W turn.
func executeMPStartPrep(ctx context.Context, t *commandTurn, v *mpStartPrepInvocation, plan effectivePlan) (out mpStartPrepResult, historical bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			O mpStartPrepResult
			H bool
		}{out, historical}, err)
		out, historical, err = x.O, x.H, e
	}()
	if v == nil || v.command == nil {
		return out, false, ErrInvalid
	}
	if err = t.requireCommandMemory(ctx, v.command.kind, mpStartPrepWorkingBytes(v), "mp_start_prep_memory"); err != nil {
		return out, false, err
	}
	kind, reference := v.command.kind, v.command.reference
	out.Outcome.Reference = reference
	if kind == MPStartPrepBegin {
		q := *v.command.begin
		now, e := t.commandTimeMS(ctx, MPStartPrepBegin, 0)
		if e != nil {
			return out, false, e
		}
		if e = mpStartPrepBeginFresh(ctx, t, reference.Identity.Incarnation, q, now); e != nil {
			return out, false, e
		}
		replay, replayed, e := mpStartPrepBeginDuplicate(ctx, t, q, reference.Identity.Incarnation)
		if e != nil {
			return out, false, e
		}
		if replayed {
			digest, e := canonicalResultDigest(reference.Identity, reference.RequestDigest, replay)
			if e != nil {
				return out, false, e
			}
			out.Value, out.Digest = replay, digest
			out.Outcome.State, out.Outcome.Replayed = Applied, true
			return out, true, nil
		}
		out.Value, err = applyMPStartPrepBegin(ctx, t, reference.Identity.Incarnation, q, now)
		if err != nil {
			return out, false, err
		}
		out.Value, err = completeMPStartPrepResult(ctx, t, kind, out.Value)
		if err != nil {
			return out, false, err
		}
		out.Digest, err = canonicalResultDigest(reference.Identity, reference.RequestDigest, out.Value)
		return out, false, err
	}
	var fence mpStartPrepFence
	var apply func() (mpStartPrepValue, error)
	switch kind {
	case MPStartPrepClaim:
		fence = v.command.claim.fence
		apply = func() (mpStartPrepValue, error) {
			return applyMPStartPrepClaim(ctx, t, fence, v.command.claim.execution)
		}
	case MPStartPrepSeal:
		fence = v.command.seal.fence
		apply = func() (mpStartPrepValue, error) {
			return applyMPStartPrepSeal(ctx, t, fence, v.command.seal.completion)
		}
	default:
		return out, false, ErrInvalid
	}
	owner, err := readMPStartPrepFresh(ctx, t, kind, fence)
	if err != nil {
		return out, false, err
	}
	facts, saved, err := mpStartPrepReadReceipt(ctx, t, fence.id, owner, reference)
	if err != nil {
		return out, false, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, kind, owner.receiptFacts(), facts)
	if err != nil {
		return out, false, err
	}
	switch decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, true, nil
	case receiptReplay:
		if owner.rows < 1 || owner.bytes < saved.storedBytes {
			return out, true, ErrResolveRequired
		}
		if err = mpStartPrepLoadPayload(ctx, t, fence.id, &saved); err != nil {
			return out, true, err
		}
		if saved.value.Preparation != fence.id || saved.appliedMS <= 0 {
			return out, true, ErrResolveRequired
		}
		out.Value, out.Digest = saved.value, saved.digest
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		return out, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, false, ErrResolveRequired
	}
	if v.command.claim == nil && v.command.seal == nil {
		return out, false, ErrInvalid
	}
	out.Value, err = apply()
	if err != nil {
		return out, false, err
	}
	out.Value, err = completeMPStartPrepResult(ctx, t, kind, out.Value)
	if err != nil {
		return out, false, err
	}
	digest, err := mpStartPrepWriteReceipt(ctx, t, fence.id, &owner, kind, reference, out.Value, out.Value.AppliedMS)
	if err != nil {
		return out, false, err
	}
	out.Digest = digest
	return out, false, nil
}

// composeMPStartPrep installs the complete MP start profile into the existing
// registry and private executor. No second executor, default budget, callbacks
// or generic API is accepted.
func composeMPStartPrep(ctx context.Context, r *effectiveRuntime, p effectivePlan) (err error) {
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if ctx == nil || r == nil || r.executor == nil || r.registry == nil || r.governor == nil || r.authority == nil || r.wal == nil {
		return ErrInvalid
	}
	if !r.mu.TryLock() {
		return ErrBusy
	}
	defer r.mu.Unlock()
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	defer r.executor.mu.Unlock()
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if r.mpStartPrepPlan != nil {
		return ErrBusy
	}
	defs := mpStartPrepDefinitions()
	frozen, err := freezeCommandPlan(r.executor, p, defs, MPStartPrepBegin)
	if err != nil {
		return err
	}
	if frozen.mpWorkerMemory == (commandMemoryBudget{}) {
		return &Refusal{Code: RefusalUnqualified}
	}
	all := []stmt.Definition{}
	for _, e := range r.registry.Manifest() {
		all = append(all, e.Definition)
	}
	for _, d := range defs {
		found := false
		for _, old := range all {
			if old.ID == d.ID {
				if !reflect.DeepEqual(old, d) {
					return ErrInvalid
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	registry, err := stmt.New(all, p.registration)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	old := r.registry
	r.registry = registry
	r.mpStartPrepPlan = &frozen
	if _, err = finishCommand(ctx, ctx, struct{}{}, nil); err != nil {
		r.registry = old
		r.mpStartPrepPlan = nil
	}
	return err
}
