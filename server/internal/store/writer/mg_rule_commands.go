package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// One accepting outcome. decision is the receipt classification; only Replay
// carries a saved result and only Fresh carries a newly persisted one. Retired
// is a complete zero result, never a reconstructed success.
type mgRuleAcceptance struct {
	decision receiptDecision
	rule     mgRuleResult
	digest   ResultDigest
}

// Purpose and durability bind a private rule effect to the closed command it
// serves. A FULL, foreign or closed command can never borrow these NORMAL
// mutations, and a caller-supplied purpose number selects nothing.
func mgRulePurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGSaveRule, MGDeleteRule:
		return nil
	}
	return ErrInvalid
}

// The owner tuple is a comparison input, never a caller-selected identity. Its
// account/profile/member/profile-lifetime/epoch must be the exact frozen US
// witness; member and profile revisions stay CAS expectations that the native
// effect rechecks against the live rows.
func mgRuleOwnerBound(o mgRecordingOwner, witness *usOwnerAuthorityProof) bool {
	if witness == nil || !o.valid() {
		return false
	}
	i := witness.witness.issuer
	return o.account == i.account && o.profile == i.profile && o.memberIncarnation == i.memberIncarnation &&
		o.profileLifetime == i.profileLifetime && o.accountEpoch == i.epoch
}

// mgRuleAuthorize binds the closed purpose and durability, the caller lifetime
// and the frozen witness to the ONE current-session seam. Until the mint is
// merged the production seam refuses with ErrMGRootUnavailable; tests inject a
// fake session, never a boolean or issuer string.
func mgRuleAuthorize(ctx context.Context, turn *commandTurn, purpose CommandID, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, owner mgRecordingOwner) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = mgRulePurpose(turn, purpose); err != nil {
		return nil, err
	}
	if lifetime == nil {
		return nil, ErrInvalid
	}
	if err = lifetime.Err(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	frozen, err := mgPersonalFreezeWitness(witness)
	if err != nil {
		return nil, err
	}
	if !mgRuleOwnerBound(owner, frozen) {
		return nil, ErrDisclosureDenied
	}
	if root == nil {
		return nil, ErrMGRootUnavailable
	}
	session, err := root.authorizeUSCurrentSession(ctx, turn, frozen, lifetime)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrMGRootUnavailable
	}
	identity := session.mgPersonalRootIdentity()
	if len(session.mgPersonalRootID()) != 22 || !identity.complete() ||
		identity.account != owner.account || identity.profile != owner.profile {
		return nil, ErrDisclosureDenied
	}
	if err = mgRulePurpose(turn, purpose); err != nil {
		return nil, err
	}
	return session, nil
}

// Fresh save: prepare the native identity/row facts from the original request,
// resolve a deleted same-identity row, then reuse the merged rule/work effect.
// A replay never re-enters the effect and an ACKed original is retired.
func acceptMGRuleSave(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgRuleSaveRequest) (out mgRuleAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGRuleSaveCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgRuleAuthorize(ctx, turn, MGSaveRule, root, witness, lifetime, request.owner)
	if err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGSaveRule, identity: command.identity, request: command.digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgRuleAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGRule(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgRuleAcceptance{decision: receiptReplay, rule: result, digest: digest}, nil
	case receiptFresh:
		facts, err := prepareMGRuleSave(ctx, turn, command, request.sourceRevision, request.generation)
		if err != nil {
			return out, err
		}
		result := mgRuleResultOf(facts)
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgRuleAcceptance{decision: receiptFresh, rule: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh delete is owner/revision intent CAS plus the native source head the
// effect requires. It retains the rule's work row and any live lease/attempt
// debt; no capture, allocation or scheduled recording is changed here. The
// public future keep/cancel selector is bound into the original request identity
// and, for keep, performed here in this SAME transaction as the legacy
// DeleteRule detach: the rule's scheduled/conflicted/waiting intents become
// manual, rule-less recordings, and their bounded count is saved in the result
// so a replay returns it with zero effects. future=cancel leaves intent custody
// to MG-12's deleted-rule reconcile.
func acceptMGRuleDelete(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgRuleDeleteRequest) (out mgRuleAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGRuleDeleteCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgRuleAuthorize(ctx, turn, MGDeleteRule, root, witness, lifetime, request.owner)
	if err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGDeleteRule, identity: command.identity, request: command.digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgRuleAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGRule(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgRuleAcceptance{decision: receiptReplay, rule: result, digest: digest}, nil
	case receiptFresh:
		facts, err := prepareMGRuleDelete(ctx, turn, command, request.sourceRevision, request.generation)
		if err != nil {
			return out, err
		}
		detached := int64(0)
		if command.request.input.future == "keep" {
			detached, err = detachMGRuleRecordings(ctx, turn, command.owner, facts.id, facts.updated)
			if err != nil {
				return out, err
			}
		}
		result := mgRuleResultOf(facts)
		result.detached = detached
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgRuleAcceptance{decision: receiptFresh, rule: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Exact owner-scoped current native row. Missing, foreign and already-deleted
// rows are one conflict class; no caller snapshot substitutes for it.
func mgRuleCurrent(ctx context.Context, turn *commandTurn, id string) (out mgRuleFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !mgRecordingHex(id, 64) {
		return out, ErrInvalid
	}
	row, err := mgRuleRow(ctx, turn, mgRuleRead, id)
	if err != nil {
		return out, err
	}
	out, err = mgRuleScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mgRuleFacts{}, ErrConflict
	}
	return out, err
}

type mgRuleIdentityLookup struct {
	facts mgRuleFacts
	found bool
}

func mgRuleSameIdentity(ctx context.Context, turn *commandTurn, f mgRuleFacts) (out mgRuleFacts, found bool, err error) {
	defer func() {
		value, failure := finishCommand(ctx, commandLifetime(turn), mgRuleIdentityLookup{out, found}, err)
		out, found, err = value.facts, value.found, failure
	}()
	if turn == nil || turn.transaction == nil {
		return out, false, ErrInvalid
	}
	step, ok := turn.steps[mgRuleIdentity]
	if !ok {
		return out, false, ErrInvalid
	}
	row, err := turn.queryRow(ctx, step.statementStep, f.owner, f.source, f.series, f.title, f.channel)
	if err != nil {
		return out, false, err
	}
	out, err = mgRuleScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mgRuleFacts{}, false, nil
	}
	if err != nil {
		return mgRuleFacts{}, false, err
	}
	return out, true, nil
}

// Fresh save composition. The caller's expected revision selects update (>=1)
// or create (0). Create resolves the native UNIQUE identity: a live duplicate is
// a conflict and a deleted row is revived with the next revision.
func prepareMGRuleSave(ctx context.Context, turn *commandTurn, c *mgRuleSaveCommand, sourceRevision int64, generation string) (out mgRuleFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if c == nil || !c.valid || sourceRevision < 1 || !mgRecordingHex(generation, 48) {
		return out, ErrInvalid
	}
	owner := c.owner
	prepared, err := mgRuleConfigIdentityFor(c.request.input, c.prepared)
	if err != nil {
		return out, err
	}
	now, err := mgPersonalNow(ctx, turn)
	if err != nil {
		return out, err
	}
	after := mgRuleFacts{
		id: c.request.input.id, owner: owner.key(), account: owner.account, profile: owner.profile,
		source: prepared.source, series: prepared.series, title: prepared.title, channel: prepared.channel,
		config: c.prepared, revision: 1, enabled: mgRuleFlag(prepared.enabled), deleted: 0, updated: now.UnixMilli(),
	}
	before := mgRuleFacts{}
	if c.request.input.expectedRevision == 0 {
		prior, found, err := mgRuleSameIdentity(ctx, turn, after)
		if err != nil {
			return out, err
		}
		if found {
			if prior.deleted != 1 || prior.revision >= math.MaxInt64 {
				return out, ErrConflict
			}
			after.id, after.revision, before = prior.id, prior.revision+1, prior
		}
	} else {
		before, err = mgRuleCurrent(ctx, turn, c.request.input.id)
		if err != nil {
			return out, err
		}
		if before.owner != after.owner || before.deleted != 0 || before.revision != c.request.input.expectedRevision ||
			before.revision >= math.MaxInt64 {
			return out, ErrConflict
		}
		// An edit may refine predicates/padding, never change the native identity.
		if before.source != after.source || before.series != after.series || before.title != after.title || before.channel != after.channel {
			return out, ErrConflict
		}
		after.revision = before.revision + 1
	}
	return saveMGRule(ctx, turn, mgRuleRequest{owner: owner, before: before, after: after, sourceRevision: sourceRevision, generation: generation})
}

func prepareMGRuleDelete(ctx context.Context, turn *commandTurn, c *mgRuleDeleteCommand, sourceRevision int64, generation string) (out mgRuleFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if c == nil || !c.valid || sourceRevision < 1 || !mgRecordingHex(generation, 48) {
		return out, ErrInvalid
	}
	owner := c.owner
	before, err := mgRuleCurrent(ctx, turn, c.request.id)
	if err != nil {
		return out, err
	}
	if before.owner != owner.key() || before.deleted != 0 || before.revision != c.request.input.expectedRevision ||
		before.revision >= math.MaxInt64 {
		return out, ErrConflict
	}
	now, err := mgPersonalNow(ctx, turn)
	if err != nil {
		return out, err
	}
	after := before
	after.revision++
	after.enabled, after.deleted, after.updated = 0, 1, now.UnixMilli()
	return deleteMGRule(ctx, turn, mgRuleRequest{owner: owner, before: before, after: after, sourceRevision: sourceRevision, generation: generation})
}

// Legacy DeleteRule(future=keep) detaches the rule's scheduled/conflicted/
// waiting intents before it deletes the rule row. The MG delete performs the
// same bounded detach in this transaction; the count is the saved result.
const mgRuleDetachMaxRecordings = 4096

// One bounded primitive over the MG recording-intent rows. It first counts the
// exact owner/rule/state set at the detach ceiling (LIMIT bound+1 is one row of
// overshoot, never an unbounded UPDATE), refuses beyond the bound with a typed
// policy error, and only then applies the legacy manual-keep columns to the
// bounded set. Every effect is in the caller's delete transaction, so a later
// denial or refused bound rolls the whole frame back.
func detachMGRuleRecordings(ctx context.Context, turn *commandTurn, owner mgRecordingOwner, ruleID string, updated int64) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || !owner.valid() || !mgRecordingHex(ruleID, 64) || updated < 0 {
		return 0, ErrInvalid
	}
	countStep, ok := turn.steps[mgRuleDetachCount]
	if !ok {
		return 0, ErrInvalid
	}
	row, err := turn.queryRow(ctx, countStep.statementStep, ruleID, owner.key(), int64(mgRuleDetachMaxRecordings+1))
	if err != nil {
		return 0, err
	}
	var count int64
	if err = row.Scan(&count); err != nil {
		return 0, err
	}
	if count < 0 || count > mgRuleDetachMaxRecordings {
		return 0, &PolicyError{Field: "mg_rule_delete_recordings", Limit: mgRuleDetachMaxRecordings, Cause: ErrCap}
	}
	if count == 0 {
		return 0, turn.checkLifetimes(ctx)
	}
	detachStep, ok := turn.steps[mgRuleDetach]
	if !ok {
		return 0, ErrInvalid
	}
	result, err := turn.exec(ctx, detachStep.statementStep, updated, ruleID, owner.key(), int64(mgRuleDetachMaxRecordings))
	if err != nil {
		return 0, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if changed != count {
		return 0, ErrConflict
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

func mgRuleFlag(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
