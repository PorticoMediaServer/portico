package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mgScheduledIntentLimit   = mgReconcileWorkLimit
	mgScheduledCanonicalRule = "rule-removed"
)

type mgScheduledIntentFacts struct {
	id, owner, authority, account, profile, source string
	revision                                       int64
	state, rule                                    string
	ruleRevision, manual                           int64
	start, end, priority                           int64
	cancelGeneration                               int64
	claimToken                                     string
	claimGeneration                                int64
	ownerInstance                                  string
	leaseUntil                                     int64
	artifact                                       string
	item, updated                                  int64
}

func mgScheduledIntentState(state string) bool {
	switch state {
	case "scheduled", "conflicted", "waiting-source", "waiting-guide", "cancelled":
		return true
	}
	return false
}

func (f mgScheduledIntentFacts) stored() bool {
	return mgRecordingHex(f.id, 64) && mgRecordingHex(f.owner, 64) && f.authority == "local" &&
		mgRecordingText(f.account, true, 256) && mgRecordingText(f.profile, true, 256) && mgRecordingHex(f.source, 48) &&
		f.revision >= 1 && mgScheduledIntentState(f.state) &&
		(f.rule == "" || mgRecordingHex(f.rule, 64)) && f.ruleRevision >= 0 &&
		(f.manual == 0 || f.manual == 1) && f.start >= 0 && f.end > f.start && f.priority >= -1000 && f.priority <= 1000 &&
		f.cancelGeneration >= 0 && mgRecordingText(f.claimToken, false, 512) && f.claimGeneration >= 0 &&
		mgRecordingText(f.ownerInstance, false, 512) && f.leaseUntil >= 0 &&
		mgRecordingText(f.artifact, false, 512) && f.item >= 0 && f.updated >= 0 &&
		(f.rule != "" || f.ruleRevision == 0)
}

// A window candidate is still an unattached, not-yet-launched rule intent. A
// launched, shared or terminal occurrence is never in this cohort.
func (f mgScheduledIntentFacts) candidate() bool {
	return f.stored() && f.state != "cancelled" && f.rule != "" && f.ruleRevision >= 1
}

type mgScheduledPage struct {
	intents []mgScheduledIntentFacts
	pending bool
}

type mgScheduledReservation struct {
	present              bool
	source               string
	start, end, priority int64
	enabled              int64
}

type mgScheduledReconcileFacts struct {
	work          mgReconcileWorkFacts
	ids           []string
	ownerRevision int64
	pending       bool
}

func mgScheduledReservationValid(r mgScheduledReservation) bool {
	return r.present && mgRecordingHex(r.source, 48) && r.start >= 0 && r.end > r.start &&
		r.priority >= -1000 && r.priority <= 1000 && (r.enabled == 0 || r.enabled == 1)
}

func mgScheduledReadReservation(ctx context.Context, t *commandTurn, id string) (out mgScheduledReservation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	step, err := mgScheduledStep(t, mgScheduledReservationRead)
	if err != nil {
		return out, err
	}
	row, err := t.queryRow(ctx, step, id)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.source, &out.start, &out.end, &out.priority, &out.enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return mgScheduledReservation{}, nil
	}
	if err != nil {
		return mgScheduledReservation{}, err
	}
	out.present = true
	if !mgScheduledReservationValid(out) {
		return mgScheduledReservation{}, ErrInvalid
	}
	return out, nil
}

func mgScheduledReadIntent(ctx context.Context, t *commandTurn, id string) (out mgScheduledIntentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgRecordingHex(id, 64) {
		return out, ErrInvalid
	}
	step, err := mgScheduledStep(t, mgScheduledIntentRead)
	if err != nil {
		return out, err
	}
	row, err := t.queryRow(ctx, step, id)
	if err != nil {
		return out, err
	}
	out, err = mgScheduledIntentScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mgScheduledIntentFacts{}, ErrConflict
	}
	if err != nil {
		return mgScheduledIntentFacts{}, err
	}
	return out, nil
}

// One keyset page on the recording-rule index: at most eight complete future
// intents plus ONE lookahead row that only proves whether more remain. A
// launched, shared or already-settled row is not a future intent candidate.
func mgScheduledReadPage(ctx context.Context, t *commandTurn, rule, cursor string) (out mgScheduledPage, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	var cursorStart int64
	cursorID := ""
	if cursor != "" {
		start, id, ok := mgScheduledCursorParse(cursor)
		if !ok {
			return out, ErrInvalid
		}
		cursorStart, cursorID = start, id
	}
	step, err := mgScheduledStep(t, mgScheduledIntentWindow)
	if err != nil {
		return out, err
	}
	rows, err := t.query(ctx, step, rule, cursorStart, cursorStart, cursorID, int64(mgScheduledIntentLimit+1))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var in mgScheduledIntentFacts
		if err = rows.Scan(&in.id, &in.owner, &in.authority, &in.account, &in.profile, &in.source, &in.revision,
			&in.state, &in.rule, &in.ruleRevision, &in.manual, &in.start, &in.end, &in.priority,
			&in.cancelGeneration, &in.claimToken, &in.claimGeneration, &in.ownerInstance, &in.leaseUntil,
			&in.artifact, &in.item, &in.updated); err != nil {
			return out, err
		}
		if !in.candidate() {
			return out, ErrConflict
		}
		out.intents = append(out.intents, in)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.intents) > mgScheduledIntentLimit {
		out.intents, out.pending = out.intents[:mgScheduledIntentLimit], true
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// Exact CAS on the logical intent row. A zero-row no-op is allowed only for the
// explicit dependency cleanup below; every intent/reservation mutation must
// affect exactly one row.
func mgScheduledIntentChange(ctx context.Context, t *commandTurn, id stmt.ID, optional bool, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	step, err := mgScheduledStep(t, id)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if optional {
		if n < 0 || n > 1 {
			return ErrConflict
		}
	} else if n != 1 {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}

func mgScheduledOwnerRevision(ctx context.Context, t *commandTurn, owner string) (revision int64, err error) {
	defer func() { revision, err = finishCommand(ctx, commandLifetime(t), revision, err) }()
	step, err := mgScheduledStep(t, mgScheduledOwnerRevisionRead)
	if err != nil {
		return 0, err
	}
	row, err := t.queryRow(ctx, step, owner)
	if err != nil {
		return 0, err
	}
	err = row.Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if revision < 1 {
		return 0, ErrInvalid
	}
	return revision, nil
}

// One sparse per-owner revision advance: insert 1 when the owner has no row yet,
// otherwise exact old-value CAS. This is the owner's intent epoch, never a
// capability, a capture claim or a physical byte refund.
func mgScheduledTouchOwner(ctx context.Context, t *commandTurn, owner string) (revision int64, err error) {
	defer func() { revision, err = finishCommand(ctx, commandLifetime(t), revision, err) }()
	before, err := mgScheduledOwnerRevision(ctx, t, owner)
	if err != nil {
		return 0, err
	}
	if before == math.MaxInt64 {
		return 0, ErrConflict
	}
	if before == 0 {
		err = mgScheduledIntentChange(ctx, t, mgScheduledOwnerRevisionAdd, false, owner, int64(1))
	} else {
		err = mgScheduledIntentChange(ctx, t, mgScheduledOwnerRevisionCAS, false, owner, before)
	}
	if err != nil {
		return 0, err
	}
	after, err := mgScheduledOwnerRevision(ctx, t, owner)
	if err != nil {
		return 0, err
	}
	if after != before+1 {
		return 0, ErrConflict
	}
	return after, nil
}

// The rule must already be retired (disabled or logically deleted) and the
// source head must still be the exact current head. This is native comparison,
// never authority: the accepting frame resolves the current session first.
func mgScheduledRuleNative(ctx context.Context, t *commandTurn, r mgReconcileScheduledRequest) (out mgRuleFacts, err error) {
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
	// Only a rule that can no longer produce occurrences is reconciled here; an
	// enabled live rule's future intents remain its own.
	if rule.deleted != 1 && rule.enabled != 0 {
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

// The bounded page transition: existing/discover -> discover while more remain,
// otherwise -> idle. Idle retirement clears the due slot and lease owner but
// preserves attempts and lease generation.
func mgScheduledWorkNext(before mgReconcileWorkFacts, cursor string, units int64, pending bool, nowMS int64) (after mgReconcileWorkFacts, leaseCheck int64, err error) {
	if before.phase != "existing" && before.phase != "discover" || before.jobRevision == math.MaxInt64 ||
		before.jobOwner < 1 || before.leaseGeneration < 1 || nowMS < before.updated ||
		units < 0 || units > mgScheduledIntentLimit {
		return mgReconcileWorkFacts{}, 0, ErrInvalid
	}
	a := before
	a.updated, a.jobRevision = nowMS, before.jobRevision+1
	if pending {
		if _, _, ok := mgScheduledCursorParse(cursor); !ok || units < 1 || before.phase == "discover" && cursor <= before.cursor {
			return mgReconcileWorkFacts{}, 0, ErrInvalid
		}
		a.phase, a.cursor = "discover", cursor
	} else {
		if cursor != "" {
			return mgReconcileWorkFacts{}, 0, ErrInvalid
		}
		a.phase, a.cursor = "idle", ""
		a.hasDue, a.dueMS, a.jobOwner, a.expires = false, 0, 0, 0
	}
	leaseCheck = before.expires
	if leaseCheck <= nowMS || leaseCheck-nowMS > mgReconcileLeaseMaxMS || !a.valid() {
		return mgReconcileWorkFacts{}, 0, ErrConflict
	}
	return a, leaseCheck, nil
}

// 827 body: detach (keep) or cancel a bounded page of future scheduled intents
// of one retired rule, disable their reservations, drop only a dependency the
// cancelled occurrence still owns, and co-commit the exact work cursor plus one
// owner-revision advance. Partial pages stay truthfully pending.
func mgScheduledReconcile(ctx context.Context, t *commandTurn, r mgReconcileScheduledRequest) (out mgScheduledReconcileFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgReconcilePurpose(t, MGScheduledReconcile); err != nil {
		return out, err
	}
	if err = r.validate(); err != nil {
		return out, err
	}
	rule, err := mgScheduledRuleNative(ctx, t, r)
	if err != nil {
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
	nowMS := now.UnixMilli()
	if r.before.expires <= nowMS {
		return out, ErrConflict
	}
	page, err := mgScheduledReadPage(ctx, t, r.rule, r.before.cursor)
	if err != nil {
		return out, err
	}
	for _, in := range page.intents {
		if in.owner != rule.owner || in.account != r.owner.account || in.profile != r.owner.profile ||
			in.source != rule.source || in.rule != r.rule {
			return out, ErrConflict
		}
		want := in
		want.updated = nowMS
		if r.policy == "keep" {
			if !in.candidate() {
				return out, ErrConflict
			}
			if err = mgScheduledIntentChange(ctx, t, mgScheduledIntentDetach, false,
				append([]any{nowMS}, in.values()...)...); err != nil {
				return out, err
			}
			want.manual, want.rule, want.ruleRevision, want.revision = 1, "", 0, in.revision+1
		} else {
			if !in.candidate() {
				return out, ErrConflict
			}
			reservation, err := mgScheduledReadReservation(ctx, t, in.id)
			if err != nil {
				return out, err
			}
			// A reservation that is still the exact window is disabled; one that
			// was already disabled is left untouched. A present but different
			// window is foreign custody and refuses the page.
			if reservation.present {
				if !mgScheduledReservationValid(reservation) || reservation.source != in.source ||
					reservation.start != in.start || reservation.end != in.end || reservation.priority != in.priority {
					return out, ErrConflict
				}
			}
			if err = mgScheduledIntentChange(ctx, t, mgScheduledIntentCancel, false,
				append([]any{mgScheduledCanonicalRule, nowMS, nowMS}, in.values()...)...); err != nil {
				return out, err
			}
			if reservation.present && reservation.enabled == 1 {
				if err = mgScheduledIntentChange(ctx, t, mgScheduledReservationOff, false,
					in.id, reservation.source, reservation.start, reservation.end, reservation.priority, reservation.enabled); err != nil {
					return out, err
				}
			}
			// Disable the conflict window, then drop only the source dependency
			// this cancelled occurrence still owns. An artifact/item intent keeps
			// it: no capture, allocation or physical custody is touched.
			if in.artifact == "" && in.item == 0 {
				if err = mgScheduledIntentChange(ctx, t, mgScheduledDependencyDelete, true, in.source, in.id); err != nil {
					return out, err
				}
			}
			want.state, want.cancelGeneration, want.revision = "cancelled", in.cancelGeneration+1, in.revision+1
		}
		actual, err := mgScheduledReadIntent(ctx, t, in.id)
		if err != nil {
			return out, err
		}
		if actual != want {
			return out, ErrConflict
		}
		out.ids = append(out.ids, in.id)
	}
	cursor := ""
	if page.pending && len(out.ids) > 0 {
		cursor = mgScheduledCursor(page.intents[len(out.ids)-1].start, page.intents[len(out.ids)-1].id)
	}
	after, leaseCheck, err := mgScheduledWorkNext(r.before, cursor, int64(len(out.ids)), page.pending, nowMS)
	if err != nil {
		return out, err
	}
	if err = mgReconcileWorkStore(ctx, t, r.before, after, leaseCheck); err != nil {
		return out, err
	}
	revision, err := mgScheduledTouchOwner(ctx, t, rule.owner)
	if err != nil {
		return out, err
	}
	out.work, out.pending, out.ownerRevision = after, page.pending, revision
	return out, nil
}
