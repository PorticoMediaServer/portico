package writer

import (
	"context"
	"math"
)

// Purpose and durability bind these private effects to the closed NORMAL
// command they serve. A FULL, foreign, unrelated or already-finished frame can
// never borrow the rule-work or scheduled mutation.
func mgReconcilePurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGRuleWorkReconcile, MGScheduledReconcile:
		return nil
	}
	return ErrInvalid
}

type mgReconcileSourceFacts struct {
	revision   int64
	generation string
	state      string
	removed    int64
}

func mgReconcileSource(ctx context.Context, t *commandTurn, source string) (out mgReconcileSourceFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgRuleRow(ctx, t, mgRuleSource, source)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.revision, &out.generation, &out.state, &out.removed); err != nil {
		return mgReconcileSourceFacts{}, mgOwnerMissing(err)
	}
	return out, nil
}

// The rule/source witness for 826. The rule row may be enabled/live or already
// disabled/deleted: retiring its outstanding work is legitimate custody, but
// the owner, rule revision and current source head must still be exact, and the
// work row's generation must be the source's current published generation. This
// is not authority: the accepting frame resolves the current session first.
func mgReconcileRuleNative(ctx context.Context, t *commandTurn, r mgReconcileRuleRequest) (out mgRuleFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !r.owner.valid() || !mgRecordingHex(r.rule, 64) || r.ruleRevision < 1 || r.sourceRevision < 1 || !mgRecordingHex(r.generation, 48) {
		return out, ErrInvalid
	}
	if err = mgRecordingCheckOwner(ctx, t, r.owner); err != nil {
		return out, err
	}
	row, err := mgRuleRow(ctx, t, mgRuleRead, r.rule)
	if err != nil {
		return out, err
	}
	rule, err := mgRuleScan(row)
	if err != nil {
		return mgRuleFacts{}, mgPersonalMissing(err)
	}
	if rule.id != r.rule || rule.owner != r.owner.key() || rule.account != r.owner.account || rule.profile != r.owner.profile ||
		rule.revision != r.ruleRevision || !mgRecordingHex(rule.source, 48) {
		return mgRuleFacts{}, ErrConflict
	}
	source, err := mgReconcileSource(ctx, t, rule.source)
	if err != nil {
		return mgRuleFacts{}, err
	}
	if source.revision != r.sourceRevision || source.generation != r.generation || source.state != "active" || source.removed != 0 {
		return mgRuleFacts{}, ErrConflict
	}
	return rule, nil
}

// One bounded work transition. Claim requires a due, unleased row; renew only
// extends a held lease; advance composes at most eight already-written child
// results and moves existing -> discover -> idle. Idle retirement clears the
// due slot and lease owner but preserves attempts and lease generation, so the
// attempt/lease history is never rewritten by retirement.
func mgReconcileWorkNext(r mgReconcileRuleRequest, nowMS int64) (after mgReconcileWorkFacts, leaseCheck int64, err error) {
	b := r.before
	if b.leaseGeneration == math.MaxInt64 || b.jobRevision == math.MaxInt64 || nowMS < b.updated {
		return mgReconcileWorkFacts{}, 0, ErrConflict
	}
	a := b
	a.updated, a.jobRevision = nowMS, b.jobRevision+1
	leaseCheck = b.expires
	switch r.mode {
	case "claim":
		if b.phase == "idle" || !b.hasDue || b.dueMS > nowMS || b.expires > nowMS ||
			b.attempts >= mgReconcileAttemptsMax {
			return mgReconcileWorkFacts{}, 0, ErrConflict
		}
		a.jobOwner = r.worker
		a.leaseGeneration = b.leaseGeneration + 1
		a.attempts = b.attempts + 1
		a.expires = r.leaseUntil
		leaseCheck = a.expires
	case "renew":
		if b.jobOwner != r.worker || b.leaseGeneration < 1 || b.expires <= nowMS || r.leaseUntil <= b.expires {
			return mgReconcileWorkFacts{}, 0, ErrConflict
		}
		a.expires = r.leaseUntil
		leaseCheck = a.expires
	case "advance":
		if b.jobOwner != r.worker || b.leaseGeneration < 1 || b.expires <= nowMS || !r.page.valid() {
			return mgReconcileWorkFacts{}, 0, ErrConflict
		}
		switch {
		case r.page.phase == b.phase:
			// Same phase is only a page inside one bounded scan: at least one
			// real child result and a strictly advancing cursor.
			if r.page.units < 1 || r.page.cursor <= b.cursor {
				return mgReconcileWorkFacts{}, 0, ErrInvalid
			}
		case b.phase == "existing" && r.page.phase == "discover":
			if r.page.cursor != "" {
				return mgReconcileWorkFacts{}, 0, ErrInvalid
			}
		case b.phase == "discover" && r.page.phase == "idle":
			if r.page.cursor != "" {
				return mgReconcileWorkFacts{}, 0, ErrInvalid
			}
		default:
			return mgReconcileWorkFacts{}, 0, ErrInvalid
		}
		a.phase, a.cursor = r.page.phase, r.page.cursor
		if a.phase == "idle" {
			// Retire logical work from the due index. This releases no capture,
			// allocation, generation or artifact custody.
			a.hasDue, a.dueMS, a.jobOwner, a.expires = false, 0, 0, 0
		}
	default:
		return mgReconcileWorkFacts{}, 0, ErrInvalid
	}
	if leaseCheck <= nowMS || leaseCheck-nowMS > mgReconcileLeaseMaxMS {
		return mgReconcileWorkFacts{}, 0, ErrConflict
	}
	if !a.valid() {
		return mgReconcileWorkFacts{}, 0, ErrInvalid
	}
	return a, leaseCheck, nil
}

// Exact 13-column CAS followed by an exact readback. Any row count other than
// one, any divergent column or a lifetime failure is a whole-frame refusal.
func mgReconcileWorkStore(ctx context.Context, t *commandTurn, before, after mgReconcileWorkFacts, leaseCheck int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if !before.valid() || !after.valid() || before.rule != after.rule || after.jobRevision != before.jobRevision+1 {
		return ErrInvalid
	}
	step, err := mgReconcileWorkStep(t, mgReconcileWorkCAS)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, append(after.values(), before.values()...)...)
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
	if err = t.checkLifetimes(ctx); err != nil {
		return err
	}
	actual, err := mgReconcileReadWork(ctx, t, before.rule)
	if err != nil {
		return err
	}
	if actual != after {
		return ErrConflict
	}
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return err
	}
	// A released (idle) lease has no live window to re-check; every held or
	// newly claimed lease must still be live after the write and readback.
	if after.phase != "idle" && now.UnixMilli() >= leaseCheck {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}

// 826 body: one bounded rule-work claim/renew/advance on an admitted frame. No
// receipt, owner advance, intent, reservation or physical fact is touched here;
// the accepting parent composes authority and the saved scalar.
func mgReconcileRuleWork(ctx context.Context, t *commandTurn, r mgReconcileRuleRequest) (out mgReconcileWorkFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgReconcilePurpose(t, MGRuleWorkReconcile); err != nil {
		return out, err
	}
	if err = r.validate(); err != nil {
		return out, err
	}
	if _, err = mgReconcileRuleNative(ctx, t, r); err != nil {
		return out, err
	}
	current, err := mgReconcileReadWork(ctx, t, r.rule)
	if err != nil {
		return out, err
	}
	if current != r.before {
		return out, ErrConflict
	}
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return out, err
	}
	after, leaseCheck, err := mgReconcileWorkNext(r, now.UnixMilli())
	if err != nil {
		return out, err
	}
	if err = mgReconcileWorkStore(ctx, t, r.before, after, leaseCheck); err != nil {
		return out, err
	}
	return after, nil
}
