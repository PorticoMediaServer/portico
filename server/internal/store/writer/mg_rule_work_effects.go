package writer

import (
	"context"
	"database/sql"
	"math"
)

const MGReconcileRule CommandID = 826

func mgRuleWorkDescriptors() []Descriptor {
	return []Descriptor{{MGReconcileRule, "mg.rule.reconcile", Normal}}
}

// Private ID826 component only. The parent must save the public receipt and
// finish all acknowledged recording/reservation effects in THIS frame before
// COMMIT. Any error requires whole-frame rollback. A logical lease is never
// capture-release permission. Frozen rule/result values are not authority or
// proof of physical completion; the parent owns matching and EOF evidence.
type mgRuleWorkFacts struct {
	rule                                      string
	revision                                  int64
	generation, phase, cursor                 string
	updated                                   int64
	diagnostic                                string
	owner, leaseGeneration, expires, attempts int64
	due                                       sql.NullInt64
	jobRevision                               int64
}
type mgRuleWorkRequest struct {
	rule           mgRuleFacts
	sourceRevision int64
	before         mgRuleWorkFacts
	worker         int64 // bounded positive native worker token, assigned by parent
	leaseUntil     int64
}
type mgRuleWorkResult struct {
	phase, cursor string
	units         int // already-composed child results; never a scanned-EOF assertion
}

func (v mgRuleWorkFacts) values() []any {
	var due any
	if v.due.Valid {
		due = v.due.Int64
	}
	return []any{v.rule, v.revision, v.generation, v.phase, v.cursor, v.updated, v.diagnostic, v.owner, v.leaseGeneration, v.expires, v.attempts, due, v.jobRevision}
}
func mgRuleWorkScan(row commandRow) (v mgRuleWorkFacts, err error) {
	err = row.Scan(&v.rule, &v.revision, &v.generation, &v.phase, &v.cursor, &v.updated, &v.diagnostic, &v.owner, &v.leaseGeneration, &v.expires, &v.attempts, &v.due, &v.jobRevision)
	return
}
func mgRuleWorkPhase(p string) bool { return p == "existing" || p == "discover" || p == "idle" }
func mgRuleWorkReadNative(ctx context.Context, t *commandTurn, id string) (out mgRuleWorkFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, e := mgRuleWorkRow(ctx, t, mgRuleWorkerRead, id)
	if e != nil {
		return mgRuleWorkFacts{}, e
	}
	return mgRuleWorkScan(row)
}
func claimMGRuleWork(ctx context.Context, t *commandTurn, r mgRuleWorkRequest) (out mgRuleWorkFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mutateMGRuleWork(ctx, t, r, "claim", mgRuleWorkResult{})
}
func renewMGRuleWork(ctx context.Context, t *commandTurn, r mgRuleWorkRequest) (out mgRuleWorkFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mutateMGRuleWork(ctx, t, r, "renew", mgRuleWorkResult{})
}
func advanceMGRuleWork(ctx context.Context, t *commandTurn, r mgRuleWorkRequest, result mgRuleWorkResult) (out mgRuleWorkFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mutateMGRuleWork(ctx, t, r, "advance", result)
}
func mutateMGRuleWork(ctx context.Context, t *commandTurn, r mgRuleWorkRequest, mode string, result mgRuleWorkResult) (out mgRuleWorkFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != MGReconcileRule || !t.transaction.policyVerified {
		return out, ErrInvalid
	}
	b := r.before
	if !mgRecordingHex(r.rule.id, 64) || !mgRecordingHex(b.generation, 48) || b.rule != r.rule.id || b.revision != r.rule.revision || !b.due.Valid || b.jobRevision < 1 || b.jobRevision == math.MaxInt64 || r.sourceRevision < 1 || r.worker < 1 || r.worker > math.MaxInt32 || !mgRuleWorkPhase(b.phase) || !mgRecordingText(b.cursor, false, 4096) {
		return out, ErrInvalid
	}
	now, e := mgPersonalNow(ctx, t)
	if e != nil {
		return out, e
	}
	ms := now.UnixMilli()
	row, e := mgRuleWorkRow(ctx, t, mgRuleRead, r.rule.id)
	if e != nil {
		return out, e
	}
	rule, e := mgRuleScan(row)
	if e != nil {
		return out, mgPersonalMissing(e)
	}
	if rule != r.rule || rule.enabled != 1 || rule.deleted != 0 {
		return out, ErrConflict
	}
	row, e = mgRuleWorkRow(ctx, t, mgRuleSource, rule.source)
	if e != nil {
		return out, e
	}
	var rev, removed int64
	var gen, state string
	if e = row.Scan(&rev, &gen, &state, &removed); e != nil {
		return out, mgPersonalMissing(e)
	}
	if rev != r.sourceRevision || gen != b.generation || state != "active" || removed != 0 {
		return out, ErrConflict
	}
	actual, e := mgRuleWorkReadNative(ctx, t, b.rule)
	if e != nil {
		return out, mgPersonalMissing(e)
	}
	if actual != b {
		return out, ErrConflict
	}
	a := b
	leaseCheck := b.expires
	a.updated = ms
	a.jobRevision++
	switch mode {
	case "claim":
		if !b.due.Valid || b.due.Int64 > ms || b.expires > ms || b.leaseGeneration == math.MaxInt64 || b.attempts >= 4294967295 || b.phase == "idle" {
			return out, ErrConflict
		}
		a.owner = r.worker
		a.leaseGeneration++
		a.attempts++
		a.expires = r.leaseUntil
		leaseCheck = a.expires
	case "renew", "advance":
		if b.owner != r.worker || b.leaseGeneration < 1 || b.expires <= ms {
			return out, ErrConflict
		}
		if mode == "renew" {
			if r.leaseUntil <= b.expires {
				return out, ErrConflict
			}
			a.expires = r.leaseUntil
			leaseCheck = a.expires
		} else {
			if result.units < 0 || result.units > 8 || !mgRuleWorkPhase(result.phase) || !mgRecordingText(result.cursor, false, 4096) || (result.units == 0 && result.phase == b.phase) || (b.phase == "discover" && result.phase == "existing") || b.phase == "idle" {
				return out, ErrInvalid
			}
			if (result.phase == b.phase && result.cursor <= b.cursor) || (result.phase != b.phase && !((b.phase == "existing" && result.phase == "discover") || (b.phase == "discover" && result.phase == "idle"))) || (result.phase != b.phase && result.cursor != "") {
				return out, ErrInvalid
			}
			a.phase = result.phase
			a.cursor = result.cursor
			if a.phase == "idle" {
				// Retire logical work from the due index. This releases no
				// capture, allocation, generation or artifact custody.
				a.due = sql.NullInt64{}
				a.owner = 0
				a.expires = 0
			}
		}
	default:
		return out, ErrInvalid
	}
	if leaseCheck <= ms || leaseCheck-ms > 20000 || ms < b.updated {
		return out, ErrConflict
	}
	// The private writer transaction serializes the native rule/source checks
	// above with this exact work CAS; no caller-supplied authority is inferred.
	args := append(a.values(), b.values()...)
	step, ok := t.steps[mgRuleWorkerCAS]
	if !ok {
		return out, ErrInvalid
	}
	res, e := t.exec(ctx, step.statementStep, args...)
	if e != nil {
		return out, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return out, e
	}
	if n != 1 {
		return out, ErrConflict
	}
	if e = t.checkLifetimes(ctx); e != nil {
		return out, e
	}
	actual, e = mgRuleWorkReadNative(ctx, t, b.rule)
	if e != nil {
		return out, e
	}
	if actual != a {
		return out, ErrConflict
	}
	// Check the live lease again after the write/readback; expiry is not success.
	end, e := mgPersonalNow(ctx, t)
	if e != nil {
		return out, e
	}
	if end.UnixMilli() >= leaseCheck {
		return out, ErrConflict
	}
	return actual, nil
}
