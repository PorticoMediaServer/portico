package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mgTunerRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil || t.transaction == nil {
		return commandRow{}, ErrInvalid
	}
	switch id {
	case mgTunerShared, mgTunerRead, mgTunerResource, mgTunerChannel, mgTunerNext, mgTunerInsert, mgTunerRenew:
	default:
		return commandRow{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return t.queryRow(ctx, step.statementStep, args...)
}
func scanMGTuner(row commandRow) (a mgTunerAllocation, err error) {
	err = row.Scan(&a.id, &a.source, &a.channel, &a.resource, &a.kind, &a.authority, &a.account, &a.profile, &a.token, &a.generation, &a.state, &a.lease, &a.created, &a.preempt)
	if err == nil && !a.valid() {
		err = ErrInvalid
	}
	return
}
func readMGTuner(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (out mgTunerAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgTunerRow(ctx, t, id, args...)
	if err != nil {
		return out, err
	}
	return scanMGTuner(row)
}

// Two closed index streams skip complete duplicate runs using the next channel
// key. Work stops at the known capacity (<=256); expired leases still count.
func readMGTunerCapacity(ctx context.Context, t *commandTurn, source string, capacity int64) (out mgTunerCapacity, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if capacity < 0 || capacity > 256 {
		return out, ErrInvalid
	}
	out.effective = capacity
	if capacity == 0 {
		return out, nil
	}
	// Legacy empty channel keys cannot be skipped as though they were free.
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
			break
		}
		out.held++
		cursor = next
	}
	return out, nil
}
func mgTunerFrame(ctx context.Context, t *commandTurn, purpose CommandID, r mgTunerRequest) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose || !t.transaction.policyVerified {
		return 0, ErrInvalid
	}
	if _, err = freezeMGTunerRequest(r); err != nil {
		return 0, err
	}
	tx := t.transaction
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.done || !tx.begun {
		return 0, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	now, e := tx.clockTime()
	if e != nil {
		return 0, e
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	out = now.UnixMilli()
	a := r.allocation
	if out < 0 || a.lease <= out || a.lease-out > 20000 || (r.protectedUntil > 0 && a.lease > r.protectedUntil) || (a.preempt > 0 && a.lease > a.preempt) {
		return 0, ErrConflict
	}
	return out, nil
}
func mgTunerSourceChannel(ctx context.Context, t *commandTurn, r mgTunerRequest) (out mgSourceFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, err = mgReadSource(ctx, t, r.allocation.source)
	if err != nil {
		return out, err
	}
	if out.state != "active" || out.removed != 0 || out.generation != r.guide {
		return out, ErrConflict
	}
	row, err := mgTunerRow(ctx, t, mgTunerChannel, r.guide, r.allocation.channel, r.allocation.source)
	if err != nil {
		return out, err
	}
	var channel string
	if err = row.Scan(&channel); err != nil {
		return out, mgDVRPolicyMissing(err)
	}
	if channel != r.allocation.channel {
		return out, ErrConflict
	}
	return out, nil
}
func acquireMGTuner(ctx context.Context, t *commandTurn, r mgTunerRequest) (out mgTunerAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	now, err := mgTunerFrame(ctx, t, MGAcquireAllocation, r)
	if err != nil {
		return out, err
	}
	a := r.allocation
	if a.state != "active" || a.created != now || a.preempt != 0 {
		return out, ErrConflict
	}
	source, err := mgTunerSourceChannel(ctx, t, r)
	if err != nil {
		return out, err
	}
	if _, err = readMGTuner(ctx, t, mgTunerResource, a.kind, a.resource); !errors.Is(err, sql.ErrNoRows) {
		if err == nil {
			err = ErrConflict
		}
		return out, err
	}
	// The published effective value already applies the owner's discovery
	// choice and limit. Physical discovery metadata alone cannot override it.
	capacity := source.tuners
	facts, err := readMGTunerCapacity(ctx, t, a.source, capacity)
	if err != nil {
		return out, err
	}
	// Sharing uses a current active logical row; this does not convey a physical
	// capability. A preempting or expired row remains debt, never a share grant.
	if capacity > 0 && facts.held >= capacity {
		row, e := mgTunerRow(ctx, t, mgTunerShared, a.source, "active", a.channel)
		if e != nil {
			return out, e
		}
		var lease, preempt int64
		e = row.Scan(&lease, &preempt)
		if e != nil {
			return out, mgDVRPolicyMissing(e)
		}
		if lease <= now || preempt != 0 {
			return out, ErrConflict
		}
	}

	row, err := mgTunerRow(ctx, t, mgTunerInsert, a.values()...)
	if err != nil {
		return out, err
	}
	changed, err := scanMGTuner(row)
	if err != nil {
		return out, err
	}
	if changed != a {
		return out, ErrConflict
	}
	out, err = readMGTuner(ctx, t, mgTunerRead, a.id)
	if err == nil && out != a {
		err = ErrConflict
	}
	if err == nil {
		_, err = mgTunerFrame(ctx, t, MGAcquireAllocation, r)
	}
	return
}
func renewMGTuner(ctx context.Context, t *commandTurn, before mgTunerAllocation, r mgTunerRequest) (out mgTunerAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	now, err := mgTunerFrame(ctx, t, MGRenewAllocation, r)
	if err != nil {
		return out, err
	}
	want := before
	want.lease = r.allocation.lease
	if !before.valid() || want != r.allocation || (before.state != "active" && before.state != "preempting") || before.lease <= now || want.lease <= before.lease {
		return out, ErrConflict
	}
	if _, err = mgTunerSourceChannel(ctx, t, r); err != nil {
		return out, err
	}
	current, err := readMGTuner(ctx, t, mgTunerRead, before.id)
	if err != nil {
		return out, mgDVRPolicyMissing(err)
	}
	if current != before {
		return out, ErrConflict
	}
	row, err := mgTunerRow(ctx, t, mgTunerRenew, append([]any{want.lease}, before.values()...)...)
	if err != nil {
		return out, err
	}
	changed, err := scanMGTuner(row)
	if err != nil {
		return out, mgDVRPolicyMissing(err)
	}
	if changed != want {
		return out, ErrConflict
	}
	out, err = readMGTuner(ctx, t, mgTunerRead, before.id)
	if err == nil && out != want {
		err = ErrConflict
	}
	if err == nil {
		_, err = mgTunerFrame(ctx, t, MGRenewAllocation, r)
	}
	return
}
