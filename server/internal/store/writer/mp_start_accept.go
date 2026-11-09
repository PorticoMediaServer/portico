package writer

import (
	"context"
	"database/sql"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MP start-preparation accept/retire/timeout (packet MP-08, commands 703-705).
//
// MP-07 (this train) owns commands 700-702 and the preparation/lifecycle/
// execution rows plus the MP start receipt root. This file adds ONLY the MP
// terminal half:
//
//   - 703 accept transfers the one physical custody of a sealed preparation to
//     the stable session id already reserved by that preparation. It never
//     creates a playback_sessions row, an mp_media_inputs row, a grant or a
//     second public 710 birth; MP-09 owns that birth and consumes this custody.
//   - 704 retire retires a stale pending/preparing/prepared/failed preparation.
//   - 705 timeout retires only a preparation whose exact deadline key has
//     elapsed. It is selected through playback_start_preparations_deadline.
//
// Neither 704 nor 705 deletes, exits or cleans the mp_start_execution row, its
// staged bytes or its custody. A successor never releases a predecessor, and no
// timeout is physical-exit proof. Helper work never starts from this frame.
//
// The UO side of start remains the MP-07 seam in mp_start_prep_births.go; this
// frame reads no UO root table and mints no second root graph.

const (
	MPStartPrepAccept  CommandID = 703
	MPStartPrepRetire  CommandID = 704
	MPStartPrepTimeout CommandID = 705
)

// mpStartAcceptValue is the complete immutable result of one terminal MP start
// command. Custody is empty unless 703 transferred it, and PhysicalExited/Cleaned
// are always retained observations, never exit proof minted by this frame.
type mpStartAcceptValue struct {
	Preparation, State, Execution, Custody string
	Session, LegacySession, Snapshot       string
	Revision, DeadlineMS                   int64
	LegacyGeneration, AppliedMS            int64
	PhysicalExited, Cleaned                int64
}

func (v mpStartAcceptValue) valid(kind CommandID) bool {
	if !mpPrepTexts(v.Preparation, v.State, v.Session, v.LegacySession, v.Snapshot) ||
		!mpPrepText(v.Execution, true) || !mpPrepText(v.Custody, true) ||
		!mpPrepRevision(v.Revision) || v.DeadlineMS <= 0 || v.LegacyGeneration <= 0 || v.AppliedMS <= 0 ||
		(v.PhysicalExited != 0 && v.PhysicalExited != 1) || (v.Cleaned != 0 && v.Cleaned != 1) || v.Cleaned > v.PhysicalExited {
		return false
	}
	switch kind {
	case MPStartPrepAccept:
		return v.State == "accepted" && v.Execution != "" && v.Custody == v.Session &&
			v.PhysicalExited == 0 && v.Cleaned == 0
	case MPStartPrepRetire, MPStartPrepTimeout:
		return v.State == "retired" && v.Custody == "" && v.PhysicalExited == 0 && v.Cleaned == 0
	}
	return false
}

// mpStartAcceptFence is the exact expected row witness. Accept revalidates the
// current source/queue and item; retire/timeout deliberately do not adopt today's
// source or queue (the preparation is being abandoned), but both still require
// the CURRENT actor authority supplied in this same turn.
type mpStartAcceptFence struct {
	id, execution   string
	revision        int64
	actor           mpPrepActor
	source          mpPrepSource
	queue           mpPrepQueue
	sourceWitnessed bool
}

func (f mpStartAcceptFence) valid() bool {
	return mpPrepTexts(f.id) && mpPrepText(f.execution, true) && mpPrepRevision(f.revision) &&
		f.actor.valid(0) && f.source.valid() && f.queue.valid() && f.sourceWitnessed
}

type mpStartAcceptInput struct {
	fence mpStartAcceptFence
}

type mpStartRetireInput struct {
	fence mpStartAcceptFence
}

type mpStartTimeoutInput struct {
	fence mpStartAcceptFence
}

func (q mpStartAcceptInput) valid() bool  { return q.fence.valid() && q.fence.execution != "" }
func (q mpStartRetireInput) valid() bool  { return q.fence.valid() }
func (q mpStartTimeoutInput) valid() bool { return q.fence.valid() }

type mpStartAcceptCommand struct {
	kind      CommandID
	accept    *mpStartAcceptInput
	retire    *mpStartRetireInput
	timeout   *mpStartTimeoutInput
	reference ReceiptReference
}

func newMPStartAcceptCommand(kind CommandID, id CommandIdentity, payload any) (*mpStartAcceptCommand, error) {
	if id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) || id.Sequence <= 0 {
		return nil, ErrInvalid
	}
	c := &mpStartAcceptCommand{kind: kind}
	switch q := payload.(type) {
	case mpStartAcceptInput:
		q.fence.sourceWitnessed = true
		if kind != MPStartPrepAccept || !q.valid() {
			return nil, ErrInvalid
		}
		c.accept = &q
	case mpStartRetireInput:
		q.fence.sourceWitnessed = true
		if kind != MPStartPrepRetire || !q.valid() {
			return nil, ErrInvalid
		}
		c.retire = &q
	case mpStartTimeoutInput:
		q.fence.sourceWitnessed = true
		if kind != MPStartPrepTimeout || !q.valid() {
			return nil, ErrInvalid
		}
		c.timeout = &q
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

func (c *mpStartAcceptCommand) owner() string {
	switch {
	case c.accept != nil:
		return c.accept.fence.id
	case c.retire != nil:
		return c.retire.fence.id
	case c.timeout != nil:
		return c.timeout.fence.id
	}
	return ""
}

func (c *mpStartAcceptCommand) fence() mpStartAcceptFence {
	switch {
	case c.accept != nil:
		return c.accept.fence
	case c.retire != nil:
		return c.retire.fence
	case c.timeout != nil:
		return c.timeout.fence
	}
	return mpStartAcceptFence{}
}

// A command-bearing invocation for the shared dispatcher. It carries no receipt
// capability, authority, exit proof or second executor.
type mpStartAcceptInvocation struct {
	disclosure *mpStartPrepDisclosure
	command    *mpStartAcceptCommand
}

func (*mpStartAcceptInvocation) isWCCommandInvocation() {}

type mpStartAcceptResult struct {
	Outcome ReceiptOutcome
	Value   mpStartAcceptValue
	Digest  ResultDigest
}

func mpStartAcceptWorkingBytes(v *mpStartAcceptInvocation) uint64 {
	n := uint64(64 << 10)
	if v == nil || v.disclosure == nil {
		return n
	}
	a := v.disclosure.actor
	for _, x := range []string{a.server, a.authority, a.account, a.profile, a.device, a.family, a.grant, a.sessionHash, v.disclosure.library} {
		n += uint64(len(x)) * 32
	}
	s := v.disclosure.scope
	for _, x := range []string{s.memberBirth, s.profileBirth, s.memberLibraries, s.membershipRole, s.role,
		s.primaryProfile, s.profileLibraries.String, s.horizon, s.expires} {
		n += uint64(len(x)) * 32
	}
	if c := v.command; c != nil {
		f := c.fence()
		n += uint64(len(f.id)+len(f.execution)) * 32
		for _, x := range []string{f.actor.server, f.actor.authority, f.actor.account, f.actor.profile, f.actor.device, f.actor.family, f.actor.grant, f.actor.sessionHash,
			f.source.library, f.source.source, f.source.asset, f.source.object, f.source.revision, f.source.root, f.source.incarnation, f.source.remote,
			f.source.configuration, f.source.policy, f.source.authority, f.queue.id, f.queue.entry, f.queue.ownerKind, f.queue.ownerID} {
			n += uint64(len(x)) * 32
		}
	}
	return n
}

func checkMPStartAcceptDisclosure(w *Writer, v *mpStartAcceptInvocation) error {
	if v == nil || v.disclosure == nil || v.command == nil {
		return ErrDisclosureDenied
	}
	d := v.disclosure
	if d.self != d || d.writer != w || w == nil || w.effective == nil || d.lifetime == nil || d.lifetime.Err() != nil || d.revoked.Load() || d.incarnation != v.command.reference.Identity.Incarnation {
		return ErrDisclosureDenied
	}
	if !mpPrepTexts(d.library) || !d.actor.sameSession(v.command.fence().actor) {
		return ErrDisclosureDenied
	}
	return nil
}

func (w *Writer) runMPStartAccept(ctx context.Context, d *mpStartPrepDisclosure, kind CommandID, reference ReceiptReference, payload any) (out mpStartAcceptResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if w == nil || w.effective == nil {
		return out, ErrInvalid
	}
	if payload == nil {
		return out, ErrInvalid
	}
	rebuilt, e := newMPStartAcceptCommand(kind, reference.Identity, payload)
	if e != nil || rebuilt.reference != reference {
		return out, errors.Join(ErrConflict, e)
	}
	v := &mpStartAcceptInvocation{disclosure: d, command: rebuilt}
	result, _, err := w.effective.dispatchCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, false, nil, v)
	return result.mpStartAccept, err
}

// executeMPStartAccept is the sole terminal MP start body: current disclosure,
// original receipt classification, saved original OR fresh exact effects, then
// the MP result/debt rows in the SAME W turn.
func executeMPStartAccept(ctx context.Context, t *commandTurn, v *mpStartAcceptInvocation, plan effectivePlan) (out mpStartAcceptResult, historical bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			O mpStartAcceptResult
			H bool
		}{out, historical}, err)
		out, historical, err = x.O, x.H, e
	}()
	if v == nil || v.command == nil {
		return out, false, ErrInvalid
	}
	if err = t.requireCommandMemory(ctx, v.command.kind, mpStartAcceptWorkingBytes(v), "mp_start_accept_memory"); err != nil {
		return out, false, err
	}
	kind, reference := v.command.kind, v.command.reference
	out.Outcome.Reference = reference
	fence := v.command.fence()
	owner, err := readMPStartAcceptFresh(ctx, t, kind, v.disclosure, fence)
	if err != nil {
		return out, false, err
	}
	facts, saved, err := mpStartAcceptReadReceipt(ctx, t, fence.id, owner.mpStartPrepOwner, reference)
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
		if err = mpStartAcceptLoadPayload(ctx, t, fence.id, &saved); err != nil {
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
	// Only a FRESH command adopts today's head. A lost-response retry of an
	// applied but unACKed terminal command returned its saved original above,
	// even after the deadline passed, the queue/source moved or the replaced
	// session ended. Replay re-checks current actor authority in the read above
	// and has zero new effects.
	if err = checkMPStartAcceptFreshGates(ctx, t, kind, fence, owner); err != nil {
		return out, false, err
	}
	switch kind {
	case MPStartPrepAccept:
		out.Value, err = applyMPStartPrepAccept(ctx, t, fence, owner)
	case MPStartPrepRetire:
		out.Value, err = applyMPStartPrepRetire(ctx, t, fence, owner)
	case MPStartPrepTimeout:
		out.Value, err = applyMPStartPrepTimeout(ctx, t, fence, owner)
	default:
		return out, false, ErrInvalid
	}
	if err != nil {
		return out, false, err
	}
	out.Value, err = completeMPStartAcceptResult(ctx, t, kind, out.Value)
	if err != nil {
		return out, false, err
	}
	digest, err := mpStartAcceptWriteReceipt(ctx, t, fence.id, &owner.mpStartPrepOwner, kind, reference, out.Value, out.Value.AppliedMS)
	if err != nil {
		return out, false, err
	}
	out.Digest = digest
	return out, false, nil
}

// mpStartAcceptOwner is the joined preparation witness plus the execution
// custody observations this frame must never manufacture.
type mpStartAcceptOwner struct {
	mpStartPrepOwner
	custody                 string
	physicalExited, cleaned int64
}

func (o mpStartAcceptOwner) valid() bool {
	return o.mpStartPrepOwner.valid() && mpPrepText(o.custody, true) &&
		(o.physicalExited == 0 || o.physicalExited == 1) && (o.cleaned == 0 || o.cleaned == 1) &&
		o.cleaned <= o.physicalExited && (o.custody == "" || o.custody == o.ids.session)
}

func readMPStartAcceptOwner(ctx context.Context, t *commandTurn, id, device string, out *mpStartAcceptOwner) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if out == nil || !mpPrepTexts(id, device) {
		return ErrInvalid
	}
	var incarnation []byte
	var sealed int64
	row, err := mpPrepRow(ctx, t, "mp.accept.owner", id, device)
	if err != nil {
		return err
	}
	err = row.Scan(&out.state, &out.lifecycleRevision, &out.execution, &sealed, &incarnation, &out.deadlineMS, &out.tokenExpiresMS,
		&out.device, &out.item, &out.part, &out.createdMS, &out.root, &out.replaces, &out.replacesGeneration,
		&out.last, &out.ack, &out.revision, &out.rows, &out.bytes, &out.control, &out.retirement,
		&out.actor.server, &out.actor.authority, &out.actor.account, &out.actor.profile, &out.actor.device, &out.actor.family, &out.actor.grant, &out.actor.sessionHash, &out.actor.epoch, &out.actor.tokenGeneration, &out.actor.horizonMS,
		&out.source.library, &out.source.source, &out.source.asset, &out.source.object, &out.source.revision, &out.source.root, &out.source.incarnation, &out.source.remote, &out.source.configuration, &out.source.policy, &out.source.authority, &out.source.libraryGeneration, &out.source.configurationGeneration, &out.source.profileRevision, &out.source.size, &out.source.modifiedNS,
		&out.queue.id, &out.queue.entry, &out.queue.ownerKind, &out.queue.ownerID, &out.queue.revision, &out.queue.custody,
		&out.ids.session, &out.ids.legacySession, &out.ids.legacyGeneration, &out.ids.snapshot,
		&out.custody, &out.physicalExited, &out.cleaned, &out.member, &out.profileLifetime)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisclosureDenied
	}
	if err != nil {
		return err
	}
	if len(incarnation) != 16 || out.device != device {
		return ErrDisclosureDenied
	}
	copy(out.incarnation[:], incarnation)
	if sealed < 0 || sealed > 1 {
		return ErrResolveRequired
	}
	out.sealed = sealed == 1
	if !out.valid() {
		return ErrResolveRequired
	}
	return nil
}

func readMPStartAcceptFresh(ctx context.Context, t *commandTurn, kind CommandID, d *mpStartPrepDisclosure, f mpStartAcceptFence) (out mpStartAcceptOwner, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			O mpStartAcceptOwner
		}{out}, err)
		out, err = x.O, e
	}()
	if !f.valid() {
		return out, ErrInvalid
	}
	switch kind {
	case MPStartPrepAccept, MPStartPrepRetire, MPStartPrepTimeout:
	default:
		return out, ErrInvalid
	}
	now, err := t.commandTimeMS(ctx, kind, 0)
	if err != nil {
		return out, err
	}
	// Current actor authority is re-proved on every turn, replay included. The
	// deadline/source/queue/item/replacement comparisons deliberately do NOT run
	// here: the original receipt must be classified first so an applied-but-
	// unACKed retry still returns its saved original after the head moved.
	// This precedes the owner and receipt reads on fresh, saved-original and
	// ACKed-retired paths alike. Scope is current-turn authority, never a saved
	// source-head freshness gate or a field persisted on the preparation.
	if err = checkMPStartAcceptScope(ctx, t, d, f, now); err != nil {
		return out, err
	}
	if err = checkMPAcceptActor(ctx, t, d.actor); err != nil {
		return out, err
	}
	if err = readMPStartAcceptOwner(ctx, t, f.id, f.actor.device, &out); err != nil {
		return out, err
	}
	if !out.actor.sameSession(d.actor) || out.member != d.scope.memberBirth || out.profileLifetime != d.scope.profileBirth ||
		out.source.library != d.library {
		return out, ErrDisclosureDenied
	}
	out.nowMS = now
	return out, nil
}

// checkMPStartAcceptFreshGates runs the exact freshness comparisons that belong
// ONLY to a fresh terminal command, after the receipt lookup reported no saved
// original. Accept revalidates the sealed deadline, source/queue equality and
// current item/source/queue/replacement; retire deliberately adopts neither
// today's source nor queue (it is abandoning the preparation); timeout proves
// the stored deadline key elapsed.
func checkMPStartAcceptFreshGates(ctx context.Context, t *commandTurn, kind CommandID, f mpStartAcceptFence, out mpStartAcceptOwner) error {
	if !f.valid() || out.nowMS <= 0 {
		return ErrInvalid
	}
	// The immutable stored actor remains the exact fresh-transition fence. A
	// rotated CURRENT token can authorize the original request, but cannot
	// rewrite its actor or its request digest. Replay returned above this gate.
	if out.actor != f.actor {
		return ErrDisclosureDenied
	}
	switch kind {
	case MPStartPrepAccept:
		if out.deadlineMS <= out.nowMS {
			return ErrConflict
		}
		if out.source != f.source || out.queue != f.queue {
			return ErrConflict
		}
		if err := checkMPAcceptPublicItem(ctx, t, out.item, out.part, out.source); err != nil {
			return err
		}
		if err := checkMPAcceptSource(ctx, t, out.source); err != nil {
			return err
		}
		if err := checkMPAcceptQueue(ctx, t, out.queue, out.actor); err != nil {
			return err
		}
		if out.replaces != "" {
			if err := checkMPAcceptReplacement(ctx, t, out.replaces, out.replacesGeneration, out.actor); err != nil {
				return err
			}
		}
	case MPStartPrepRetire:
		// Retiring a stale source/queue never validates or adopts today's source.
	case MPStartPrepTimeout:
		if out.deadlineMS > out.nowMS {
			return ErrConflict
		}
	default:
		return ErrInvalid
	}
	return nil
}

// mpAcceptExec runs one closed write statement and requires exactly one changed
// row. The four terminal CAS statements deliberately return no row: the CAS in
// their WHERE clause is the entire proof, and an unexpected affected count is a
// conflict, never a second transfer or a silent no-op success.
func mpAcceptExec(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	if t == nil {
		return ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return ErrInvalid
	}
	result, err := t.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func checkMPAcceptActor(ctx context.Context, t *commandTurn, a mpPrepActor) error {
	if !a.valid(0) {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.accept.actor_current", a.sessionHash, a.family, a.server, a.authority, a.account, a.profile, a.device, a.epoch, a.tokenGeneration)
}

func checkMPAcceptSource(ctx context.Context, t *commandTurn, s mpPrepSource) error {
	if !s.valid() {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.accept.source_current", s.object, s.source, s.asset, s.revision, s.incarnation, s.size, s.modifiedNS, s.library, s.root, s.libraryGeneration, s.remote)
}

func checkMPAcceptPublicItem(ctx context.Context, t *commandTurn, item string, part int64, s mpPrepSource) error {
	if part < 0 || !mpPrepTexts(item) {
		return ErrInvalid
	}
	raw, ok := entityid.Decode(item)
	if !ok || len(raw) != 16 {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.accept.public_item_current", raw, s.library, s.asset, part)
}

func checkMPAcceptQueue(ctx context.Context, t *commandTurn, q mpPrepQueue, a mpPrepActor) error {
	if !q.valid() {
		return ErrInvalid
	}
	if q.id == "" {
		return nil
	}
	return mpPrepOne(ctx, t, "mp.accept.queue_current", q.id, q.ownerKind, q.ownerID, q.revision, q.custody, q.entry, a.authority, a.account, a.profile)
}

func checkMPAcceptReplacement(ctx context.Context, t *commandTurn, id string, generation int64, a mpPrepActor) error {
	if !mpPrepTexts(id) || generation <= 0 {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.accept.replacement", id, a.authority, a.account, a.profile, a.device, generation)
}

// applyMPStartPrepAccept transfers custody exactly once. The sealed preparation,
// execution and source were proved current in this same turn; the custody update
// owns the one-way CAS (empty -> reserved session id) and the lifecycle CAS
// publishes accepted only after the transfer succeeded.
func applyMPStartPrepAccept(ctx context.Context, t *commandTurn, f mpStartAcceptFence, owner mpStartAcceptOwner) (out mpStartAcceptValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepAccept); err != nil {
		return
	}
	if f.execution == "" || owner.state != "prepared" || !owner.sealed || owner.execution != f.execution ||
		owner.lifecycleRevision != f.revision || owner.custody != "" || owner.deadlineMS <= owner.nowMS {
		return out, ErrConflict
	}
	if err = mpAcceptExec(ctx, t, "mp.accept.custody", owner.ids.session, f.id, f.execution); err != nil {
		return out, err
	}
	if err = mpAcceptExec(ctx, t, "mp.accept.lifecycle", f.id, f.revision, f.execution); err != nil {
		return out, err
	}
	return mpStartAcceptValue{Preparation: f.id, State: "accepted", Execution: f.execution, Custody: owner.ids.session,
		Session: owner.ids.session, LegacySession: owner.ids.legacySession, LegacyGeneration: owner.ids.legacyGeneration,
		Snapshot: owner.ids.snapshot, Revision: f.revision + 1, DeadlineMS: owner.deadlineMS, AppliedMS: owner.nowMS}, nil
}

// applyMPStartPrepRetire retires the preparation and deliberately leaves the
// execution row, its staged bytes, its result digest and its custody untouched.
func applyMPStartPrepRetire(ctx context.Context, t *commandTurn, f mpStartAcceptFence, owner mpStartAcceptOwner) (out mpStartAcceptValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepRetire); err != nil {
		return
	}
	if owner.state == "accepted" || owner.state == "retired" || owner.lifecycleRevision != f.revision ||
		owner.execution != f.execution || owner.custody != "" {
		return out, ErrConflict
	}
	if err = mpAcceptExec(ctx, t, "mp.accept.retire", f.id, f.revision, f.execution); err != nil {
		return out, err
	}
	return mpStartAcceptValue{Preparation: f.id, State: "retired", Execution: owner.execution,
		Session: owner.ids.session, LegacySession: owner.ids.legacySession, LegacyGeneration: owner.ids.legacyGeneration,
		Snapshot: owner.ids.snapshot, Revision: f.revision + 1, DeadlineMS: owner.deadlineMS,
		PhysicalExited: owner.physicalExited, Cleaned: owner.cleaned, AppliedMS: owner.nowMS}, nil
}

// composeMPStartAccept installs the terminal MP start profile into the existing
// registry and private executor. MP-07 must own the same W: this frame reads and
// writes rows that 700-702 created, and it never composes a second executor,
// receipt root or default budget.
func composeMPStartAccept(ctx context.Context, r *effectiveRuntime, p effectivePlan) (err error) {
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
	if r.mpStartAcceptPlan != nil {
		return ErrBusy
	}
	if r.mpStartPrepPlan == nil {
		return ErrInvalid
	}
	defs := mpStartAcceptDefinitions()
	frozen, err := freezeCommandPlan(r.executor, p, defs, MPStartPrepAccept)
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
	r.mpStartAcceptPlan = &frozen
	if _, err = finishCommand(ctx, ctx, struct{}{}, nil); err != nil {
		r.registry = old
		r.mpStartAcceptPlan = nil
	}
	return err
}
