package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Bounded private scalar controls on ONE existing commandTurn, reached only
// through the MG-02 current-source acquisition witness. They open no writer,
// connection, callback or public facade, and perform no provider, tuner, decoder
// or filesystem IO: the whole hold is SQL on the caller's transaction.
func mgTunerTurn(a *mgSourceAcquisition) *commandTurn {
	if a == nil || a.self != a || a.frame == nil || a.frame.self != a.frame {
		return nil
	}
	return a.frame.turn
}

func mgTunerRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil || t.transaction == nil {
		return commandRow{}, ErrInvalid
	}
	switch id {
	case mgTunerRead, mgTunerResource, mgTunerChannel, mgTunerShared, mgTunerNext, mgTunerReservation, mgTunerInsert, mgTunerRenew, mgTunerRetire, mgTunerReconcileNext:
	default:
		return commandRow{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return t.queryRow(ctx, step.statementStep, args...)
}

// Every result below is a valued command/protocol result: the caller lifetime is
// joined on every return, so a cancelled or closed turn never yields a usable
// tuple.
func scanMGTuner(row commandRow) (a mgTunerAllocation, err error) {
	if err = row.Scan(&a.id, &a.source, &a.channel, &a.resource, &a.kind, &a.authority, &a.account, &a.profile, &a.token, &a.generation, &a.state, &a.lease, &a.created, &a.preempt); err != nil {
		return mgTunerAllocation{}, err
	}
	if !a.valid() {
		return mgTunerAllocation{}, ErrInvalid
	}
	return a, nil
}

func mgTunerMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}

func readMGTuner(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (out mgTunerAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, e := mgTunerRow(ctx, t, id, args...)
	if e != nil {
		return out, e
	}
	return scanMGTuner(row)
}

// This body accepts only the NORMAL tuner purposes and requires the native
// synchronous level to match the declared durability. It is not a runtime
// recipe: the accepting parent still admits, receipts and commits.
func mgTunerPurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose || !t.transaction.policyVerified {
		return ErrInvalid
	}
	if purpose != MGAcquireAllocation && purpose != MGRenewAllocation && purpose != MGTunerRetireAllocation && purpose != MGTunerReconcileAllocations {
		return ErrInvalid
	}
	if t.transaction.descriptor.Durability != Normal || t.transaction.settings.Synchronous != int(sqliteconn.Normal) {
		return ErrInvalid
	}
	return nil
}

func mgTunerTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil {
		return 0, ErrInvalid
	}
	tx := t.transaction
	switch tx.descriptor.ID {
	case MGAcquireAllocation, MGRenewAllocation, MGTunerRetireAllocation, MGTunerReconcileAllocations:
	default:
		return 0, ErrInvalid
	}
	// The parent owns admission; an unknown or unverified frame yields no clock.
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil || !tx.policyVerified {
		return 0, ErrInvalid
	}
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	defer tx.guardPanic()
	now := tx.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err = tx.observeTime(now); err != nil {
		return 0, err
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return now.UnixMilli(), nil
}

// The parent-selected head must be the source's current published generation,
// the source must still be active and unremoved, and the channel must exist in
// that exact generation. A stale head, a disabled or retired source and a
// channel from another guide are all refusals without effects.
func mgTunerSourceChannel(ctx context.Context, t *commandTurn, guide, source, channel string) (out mgSourceFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, err = mgReadSource(ctx, t, source)
	if err != nil {
		return out, err
	}
	if out.state != "active" || out.removed != 0 || out.generation != guide {
		return out, ErrConflict
	}
	row, e := mgTunerRow(ctx, t, mgTunerChannel, guide, channel)
	if e != nil {
		return out, e
	}
	var found string
	if e = row.Scan(&found); e != nil {
		return out, mgTunerMissing(e)
	}
	if found != channel {
		return out, ErrConflict
	}
	return out, nil
}

// A live allocation may only carry the protected boundary its owner's conflict
// policy selected, and that boundary is data here, not a grant: it must still be
// an enabled reservation window for this source that has not already ended.
func mgTunerBoundary(ctx context.Context, t *commandTurn, source string, r mgTunerRequest, now int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if r.protectedUntil == 0 {
		return nil
	}
	// freezeMGTunerRequest already fixed the live shape and the lease bound.
	row, e := mgTunerRow(ctx, t, mgTunerReservation, source, r.protectedUntil, now)
	if e != nil {
		return e
	}
	var start int64
	if e = row.Scan(&start); e != nil {
		// No enabled, not-yet-ended reservation starts at the selected boundary.
		return mgTunerMissing(e)
	}
	return nil
}

// Counts distinct channels that still hold a tuner, not allocations: rows in
// both held states share one channel. The walk is a keyset merge of two ordered
// index streams that stops at the effective capacity, so a full result is a
// lower bound, never a total. A legacy empty channel key cannot be skipped as
// though it were free, so it refuses instead of undercounting.
func readMGTunerCapacity(ctx context.Context, t *commandTurn, source string, capacity int64) (out mgTunerCapacity, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if capacity < 0 || capacity > mgTunerMaxCount {
		return out, ErrInvalid
	}
	out.effective = capacity
	if capacity == 0 {
		return out, nil
	}
	for _, state := range []string{"active", "preempting"} {
		row, e := mgTunerRow(ctx, t, mgTunerShared, source, state, "")
		if e != nil {
			return out, e
		}
		var lease, preempt int64
		e = row.Scan(&lease, &preempt)
		if e == nil {
			return out, ErrConflict
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
	}
	cursor := ""
	for out.held < capacity {
		next := ""
		for _, state := range []string{"active", "preempting"} {
			row, e := mgTunerRow(ctx, t, mgTunerNext, source, state, cursor)
			if e != nil {
				return out, e
			}
			var key string
			e = row.Scan(&key)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return out, e
			}
			if !mgSourceText(key, true, 256) || key <= cursor {
				return out, ErrInvalid
			}
			if next == "" || key < next {
				next = key
			}
		}
		if next == "" {
			return out, nil
		}
		out.held++
		cursor = next
	}
	return out, nil
}

// The frozen owner tuple must be the requester's own current principal. The
// proof was independently re-prepared by the authority frame, so reading it here
// is reusing a verified comparison fact, not accepting caller-supplied identity.
func mgTunerOwner(a *mgSourceAcquisition, x mgTunerAllocation) bool {
	if a == nil || a.frame == nil || a.frame.actor == nil {
		return false
	}
	i := a.frame.actor.proof.native.witness.issuer
	return x.authority == "local" && x.account == i.account && x.profile == i.profile
}

// Sharing is an existing active lease on the same physical channel with no
// deadline and a lease that has not expired. A preempting or expired row is debt,
// not a share grant, and capacity is never recovered by touching another row.
func mgTunerSharedStream(ctx context.Context, t *commandTurn, source, channel string, now int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	row, e := mgTunerRow(ctx, t, mgTunerShared, source, "active", channel)
	if e != nil {
		return e
	}
	var lease, preempt int64
	if e = row.Scan(&lease, &preempt); e != nil {
		return mgTunerMissing(e)
	}
	if lease <= now || preempt != 0 {
		return ErrConflict
	}
	return nil
}

// Acquire inserts one exactly conserved allocation tuple. Capacity is the source's
// published effective tuner count, never discovery metadata, and an unknown value
// refuses nothing. No established stream is evicted: a saturated source accepts
// only a share of an already active channel.
func acquireMGTuner(ctx context.Context, a *mgSourceAcquisition, r mgTunerRequest) (out mgTunerAllocation, err error) {
	t := mgTunerTurn(a)
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil {
		return out, ErrDisclosureDenied
	}
	if err = mgTunerPurpose(t, MGAcquireAllocation); err != nil {
		return out, err
	}
	if err = a.recheck(ctx, t); err != nil {
		return out, err
	}
	if _, err = freezeMGTunerRequest(r); err != nil {
		return out, err
	}
	if a.witness.source != r.allocation.source || a.witness.head != r.guide {
		return out, ErrConflict
	}
	var now int64
	if now, err = mgTunerTime(ctx, t); err != nil {
		return out, err
	}
	alloc := r.allocation
	if alloc.state != "active" || alloc.generation != 1 || alloc.created != now || alloc.lease <= now || alloc.lease-now > mgTunerLeaseMS {
		return out, ErrConflict
	}
	if !mgTunerOwner(a, alloc) {
		return out, ErrDisclosureDenied
	}
	if err = mgTunerBoundary(ctx, t, alloc.source, r, now); err != nil {
		return out, err
	}
	source, err := mgTunerSourceChannel(ctx, t, r.guide, alloc.source, alloc.channel)
	if err != nil {
		return out, err
	}
	if _, err = readMGTuner(ctx, t, mgTunerResource, alloc.kind, alloc.resource); !errors.Is(err, sql.ErrNoRows) {
		if err == nil {
			err = ErrConflict
		}
		return out, err
	}
	facts, err := readMGTunerCapacity(ctx, t, alloc.source, source.tuners)
	if err != nil {
		return out, err
	}
	if facts.effective > 0 && facts.held >= facts.effective {
		if err = mgTunerSharedStream(ctx, t, alloc.source, alloc.channel, now); err != nil {
			return out, err
		}
	}
	row, e := mgTunerRow(ctx, t, mgTunerInsert, alloc.values()...)
	if e != nil {
		return out, e
	}
	changed, err := scanMGTuner(row)
	if err != nil {
		return out, err
	}
	if changed != alloc {
		return out, ErrConflict
	}
	if out, err = readMGTuner(ctx, t, mgTunerRead, alloc.id); err != nil {
		return out, err
	}
	if out != alloc {
		return out, ErrConflict
	}
	// The original result is only released while the actor and head are still
	// current; a revocation during the insert is a refusal.
	if err = a.recheck(ctx, t); err != nil {
		return out, err
	}
	return out, nil
}

// Renew advances exactly the lease of the same token/generation tuple and
// nothing else. It never resurrects a released/expired lease, never changes the
// channel, resource or owner, and never extends beyond the protected boundary.
func renewMGTuner(ctx context.Context, a *mgSourceAcquisition, before mgTunerAllocation, r mgTunerRequest) (out mgTunerAllocation, err error) {
	t := mgTunerTurn(a)
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil {
		return out, ErrDisclosureDenied
	}
	if err = mgTunerPurpose(t, MGRenewAllocation); err != nil {
		return out, err
	}
	if err = a.recheck(ctx, t); err != nil {
		return out, err
	}
	if _, err = freezeMGTunerRequest(r); err != nil {
		return out, err
	}
	want := before
	want.lease = r.allocation.lease
	if !before.valid() || want != r.allocation || (before.state != "active" && before.state != "preempting") {
		return out, ErrConflict
	}
	if !mgTunerOwner(a, want) {
		return out, ErrDisclosureDenied
	}
	if a.witness.source != want.source || a.witness.head != r.guide {
		return out, ErrConflict
	}
	now, err := mgTunerTime(ctx, t)
	if err != nil {
		return out, err
	}
	if before.lease <= now || want.lease <= before.lease || want.lease-now > mgTunerLeaseMS {
		return out, ErrConflict
	}
	if _, err = mgTunerSourceChannel(ctx, t, r.guide, want.source, want.channel); err != nil {
		return out, err
	}
	row, e := mgTunerRow(ctx, t, mgTunerRenew, append([]any{want.lease}, before.values()...)...)
	if e != nil {
		return out, e
	}
	changed, err := scanMGTuner(row)
	if err != nil {
		return out, mgTunerMissing(err)
	}
	if changed != want {
		return out, ErrConflict
	}
	if out, err = readMGTuner(ctx, t, mgTunerRead, before.id); err != nil {
		return out, err
	}
	if out != want {
		return out, ErrConflict
	}
	if err = a.recheck(ctx, t); err != nil {
		return out, err
	}
	return out, nil
}
