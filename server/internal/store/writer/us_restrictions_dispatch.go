package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The closed accepting entry for the profile-restrictions replacement (542). It
// is private test/harness composition: no public Writer method, no product
// caller and no exported constructor. The harness supplies an already begun
// FULL private turn; this file owns the original/Resolve/ACK classification,
// the retained quota, the one replacement, the complete DTO and the physical
// terminal. It appends no event: the product applies restrictions live on the
// profile's next request and signs nobody out.
//
// Settled here:
//   - Retained receipt quota. An owner retains at most 64 receipts and 1 MiB of
//     variable receipt bytes. A fresh result that would pass either bound is
//     refused with RefusalCapacity BEFORE any source effect. These are exact
//     counters, not a product user cap; reclaim refunds actual bytes.
//   - Every fresh replacement bumps the profile restrictions revision by one,
//     even when the submitted document equals the current one: the product's
//     full replacement always writes and the source trigger owns the +1.
//   - Retired owners. A fresh store or an ACK on an owner whose retired_ms is
//     non-zero is refused as owner-unavailable. A replay of a physically
//     retained original stays readable with zero effects.
//   - original_family_id is provenance, not an equality predicate. The retained
//     owner is addressed by server/account/member incarnation/profile
//     lifetime/installation, so a RENEWED family of the same installation
//     resolves its original command; a different installation cannot read or
//     advance it.
//   - Recheck cost. The dispatcher funds its own authority rechecks. Each
//     recheck re-runs the complete US-01 current-source recipe and needs the
//     64 KiB base workspace, so the parent's funded memory must cover the
//     dispatcher's declared recheck count plus the measured body before the
//     dispatcher reads anything.
const (
	usRestrictionsOwnerReceipts     int64 = 64
	usRestrictionsOwnerReceiptBytes int64 = 1 << 20

	// Ten dispatcher-owned rechecks bound one fresh dispatch: the entry recheck
	// before any owner or payload read, the scoped owner re-read that settles
	// retirement/quota, the lookup custody's entry and deferred pre-terminal
	// rechecks plus the lookup release check, the store's own entry and deferred
	// rechecks plus the store custody's entry and deferred pre-terminal checks,
	// and the final current disclosure before the terminal.
	usRestrictionsDispatchRechecks int64 = 10
	usRestrictionsRecheckBytes     int64 = 64 << 10
)

type usRestrictionsInvocation struct {
	request usRestrictionsReceiptRequest
	input   *usRestrictionsCommand
	ack     bool
}

// usRestrictionsDispatchResult is the closed result of one dispatch call. Value
// is the COMPLETE ProfileRestrictions response DTO. It is present for a fresh
// original and for a replay of the retained original; a replay never reads the
// live row or re-derives the rating ceiling.
type usRestrictionsDispatchResult struct {
	Outcome ReceiptOutcome
	Value   usRestrictionsSavedResult
	Digest  ResultDigest

	// Exact accounting for this command, not a quota claim.
	ChargedBytes int64
	OwnerCount   int64
	OwnerBytes   int64
}

func (usRestrictionsDispatchResult) String() string { return "usRestrictionsDispatchResult{redacted}" }
func (usRestrictionsDispatchResult) GoString() string {
	return "usRestrictionsDispatchResult{redacted}"
}
func (usRestrictionsDispatchResult) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usRestrictionsDispatchResult{redacted}"))
}

type usRestrictionsCompletion struct {
	out        usRestrictionsDispatchResult
	floor      AckResult
	historical bool
}

func (usRestrictionsCompletion) String() string   { return "usRestrictionsCompletion{redacted}" }
func (usRestrictionsCompletion) GoString() string { return "usRestrictionsCompletion{redacted}" }
func (usRestrictionsCompletion) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usRestrictionsCompletion{redacted}"))
}

// usRestrictionsDispatchFund charges the dispatcher's declared authority
// rechecks and the measured body bytes against the parent's funded memory. It
// never opens a second ledger: the parent's grant already reserves this memory.
func usRestrictionsDispatchFund(a *usOwnerAuthorization, body int64) error {
	if a == nil || body < 0 || body > maxCanonicalResultBytes {
		return ErrInvalid
	}
	need := uint64(usRestrictionsDispatchRechecks)*uint64(usRestrictionsRecheckBytes) + uint64(body)*32
	if need > a.budget.memory {
		return &Refusal{Code: RefusalUnqualified}
	}
	return nil
}

// usRestrictionsAppliedMS samples the trusted writer clock once for the
// immutable applied_ms and the quota decision. No real clock is read here and no
// lease is invented: US purposes declare no command clock, so the transaction
// high-water is the only admissible source.
func usRestrictionsAppliedMS(turn *commandTurn) (int64, error) {
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	now := tx.writer.now()
	ms := now.UnixMilli()
	if ms < 0 || !time.UnixMilli(ms).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	if err := tx.observeTime(now); err != nil {
		return 0, err
	}
	if deadline, ok := tx.ctx.Deadline(); !ok || !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return ms, nil
}

// usRestrictionsQuotaFacts is the scoped owner re-read with the exact retained
// counters and the original family provenance.
type usRestrictionsQuotaFacts struct {
	usRestrictionsOwnerFacts
	family string
}

func readUSRestrictionsQuota(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usRestrictionsQuotaFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	if !usIdentifier(ownerID) || turn == nil || a == nil {
		return out, ErrInvalid
	}
	// The owner is read under the CURRENT issuer scope, before any payload.
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	i := a.proof.witness.issuer
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsOwner, ownerID, i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.family, &out.lastSequence, &out.acknowledgedSequence, &out.reclaimed, &out.count, &out.bytes, &out.retiredMS); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(inc) != 16 || !usIdentifier(out.family) || out.lastSequence < 0 || out.acknowledgedSequence < 0 || out.acknowledgedSequence > out.lastSequence || out.lastSequence-out.acknowledgedSequence > 1 || out.reclaimed < 0 || out.reclaimed > out.acknowledgedSequence || out.count < out.lastSequence-out.acknowledgedSequence || out.bytes < 0 || out.retiredMS < 0 || (out.count == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	out.exists = true
	copy(out.incarnation[:], inc)
	return out, turn.checkLifetimes(ctx)
}

// usRestrictionsQuotaAdmit refuses a fresh result that would pass either
// retained bound BEFORE any source effect. count/bytes are W's exact counters.
func usRestrictionsQuotaAdmit(owner usRestrictionsOwnerFacts, charge int64) error {
	if !owner.exists || owner.retiredMS != 0 {
		return ErrOwnerUnavailable
	}
	if charge <= 0 || charge > usRestrictionsOwnerReceiptBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.count >= usRestrictionsOwnerReceipts || owner.bytes > usRestrictionsOwnerReceiptBytes-charge {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.lastSequence != owner.acknowledgedSequence {
		// The next sequence requires a durable predecessor ACK; that is an
		// ordinary protocol condition, not a capacity refusal.
		return ErrResolveRequired
	}
	return nil
}

// executeUSRestrictionsInvocation returns Pending for a fresh effect: only the
// physical terminal may declare Applied.
func executeUSRestrictionsInvocation(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usRestrictionsInvocation) (out usRestrictionsDispatchResult, floor AckResult, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), usRestrictionsCompletion{out, floor, historical}, err)
		out, floor, historical, err = v.out, v.floor, v.historical, e
	}()
	if turn == nil || turn.transaction == nil || a == nil || inv == nil {
		return out, floor, false, ErrInvalid
	}
	kind := turn.transaction.descriptor.ID
	q := inv.request
	if q.kind != kind || usRestrictionsReceiptKind(kind) != nil || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.Identity.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) {
		return out, floor, false, ErrInvalid
	}
	// An earlier prefix in this same frame may have advanced a source row, so
	// current authority is rechecked before ANY owner or payload read.
	if err = a.recheck(ctx, turn); err != nil {
		return out, floor, false, err
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, floor, false, err
	}
	if err = usRestrictionsDispatchFund(a, 0); err != nil {
		return out, floor, false, err
	}
	if inv.ack {
		if inv.input != nil {
			return out, floor, false, ErrInvalid
		}
		floor.AcknowledgedThrough, err = acknowledgeUSRestrictionsAfterAuthority(ctx, turn, a, q)
		return out, floor, true, err
	}
	// A submission to apply is admitted against the CURRENT retained owner
	// before classification: retirement may not be resurrected by a write, and
	// the quota is refused before any source effect. A bare Resolve/replay reads
	// the owner through the receipt adapter instead.
	if inv.input != nil {
		quota, e := readUSRestrictionsQuota(ctx, turn, a, q.ownerID)
		if e != nil {
			return out, floor, false, e
		}
		if quota.exists && quota.retiredMS != 0 && q.reference.Identity.Sequence > quota.lastSequence {
			return out, floor, false, ErrOwnerUnavailable
		}
	}
	lookup, err := lookupUSRestrictions(ctx, turn, a, q)
	if err != nil {
		return out, floor, false, err
	}
	out.Outcome.Reference = q.reference
	out.OwnerCount, out.OwnerBytes = lookup.owner.count, lookup.owner.bytes
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, floor, true, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		return out, floor, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, false, ErrResolveRequired
	}
	if inv.input == nil {
		// Preparation/status only: no effect, no receipt.
		return out, floor, false, nil
	}
	input := inv.input
	if input.kind != q.kind || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest || input.request.ownerID != q.ownerID || input.request.issuer != a.proof.witness.issuer {
		return out, floor, false, ErrConflict
	}
	// The product edits only a profile of the caller's own account. A foreign
	// account target is a denial, never a cross-account power.
	if input.request.account != a.proof.witness.issuer.account {
		return out, floor, false, ErrDisclosureDenied
	}
	// A fresh submission must be the exact frozen preparation. The historical
	// lookup above is deliberately independent of a now-stale preparation.
	digest, err := canonicalRequestDigest(input.kind, 1, input.identity, input.request)
	if err != nil || digest != input.digest {
		return out, floor, false, errors.Join(ErrConflict, err)
	}
	if err = a.budget.check(ctx, turn, int64(len(input.blockedJSON))); err != nil {
		return out, floor, false, err
	}
	if err = validateUSRestrictionsProjection(input); err != nil {
		return out, floor, false, err
	}
	before, err := readUSRestrictionsFreshTarget(ctx, turn, a, input)
	if err != nil {
		return out, floor, false, err
	}
	nowMS, err := usRestrictionsAppliedMS(turn)
	if err != nil {
		return out, floor, false, err
	}
	v := usRestrictionsSavedResult{kind: q.kind, ownerID: q.ownerID, account: input.request.account, memberIncarnation: input.request.memberIncarnation,
		profile: input.request.profile, profileLifetime: input.request.profileLifetime, revision: before.revision + 1, appliedMS: nowMS,
		ratingSystem: input.request.ratingSystem, maximumAgeRating: input.request.maximumAgeRating, maximumAge: input.maximumAge,
		allowUnrated: input.request.allowUnrated, allowDownloads: input.request.allowDownloads, allowLiveTV: input.request.allowLiveTV,
		allowDVR: input.request.allowDVR, allowWatchTogether: input.request.allowWatchTogether, labels: input.request.blockedLabels}
	// Reserve the complete prospective immutable result BEFORE any source effect.
	charge, err := usRestrictionsResultVariableBytes(v)
	if err != nil {
		return out, floor, false, err
	}
	if err = usRestrictionsQuotaAdmit(lookup.owner, charge); err != nil {
		return out, floor, false, err
	}
	if err = usRestrictionsDispatchFund(a, charge); err != nil {
		return out, floor, false, err
	}
	effect, e := applyUSRestrictions(ctx, turn, input, before)
	if e != nil {
		return out, floor, false, e
	}
	if effect.revision != v.revision || effect.ratingSystem != v.ratingSystem || effect.maximumAgeRating != v.maximumAgeRating ||
		effect.maximumAge != v.maximumAge || effect.blocked != input.blockedJSON {
		return out, floor, false, ErrResolveRequired
	}
	out.Digest, err = usRestrictionsResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, floor, false, err
	}
	charged, err := storeUSRestrictionsResult(ctx, turn, a, q, v, out.Digest)
	if err != nil || charged != charge {
		return out, floor, false, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.ChargedBytes = v, charged
	out.OwnerCount, out.OwnerBytes = lookup.owner.count+1, lookup.owner.bytes+charged
	return out, floor, false, turn.checkLifetimes(ctx)
}

// runUSRestrictionsDispatch is the closed test/harness entry. It owns the final
// current disclosure and the FULL physical terminal; the caller supplies only
// the already admitted turn.
func runUSRestrictionsDispatch(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usRestrictionsInvocation) (out usRestrictionsDispatchResult, floor AckResult, err error) {
	var historical bool
	defer func() {
		v, e := finishCommand(ctx, ctx, usRestrictionsCompletion{out, floor, historical}, err)
		out, floor, err = v.out, v.floor, e
	}()
	out, floor, historical, err = executeUSRestrictionsInvocation(ctx, turn, a, inv)
	if err != nil {
		return withdrawUSRestrictions(turn, err)
	}
	// Final current disclosure before the terminal: a release-time denial
	// REPLACES the body outcome and rolls the whole prefix back.
	if denial := a.recheck(ctx, turn); denial != nil {
		return withdrawUSRestrictions(turn, denial)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUSRestrictions(turn, ctxErr)
	}
	// The release rule runs BEFORE the terminal: the transaction cancels its
	// own lifetime at commit/rollback, so this release uses the live frame
	// lifetime. The deferred final publication rechecks the CALLER's context
	// after physical return. It mirrors dispatchCommand's caller-context recheck
	// but does not join currentCommandDisclosure / tx.lifetimeErr; before any
	// production entry that defer must also check the in-memory a.lifetime.Err()
	// and turn.transaction.lifetimeErr. Do NOT call a.recheck there: it denies
	// once transaction.done is set (us_authority.go:296).
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), usRestrictionsCompletion{out, floor, historical}, nil)
	if releaseErr != nil {
		return withdrawUSRestrictions(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUSRestrictions(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		// finishCommand's release rule: no error, including a late cleanup
		// failure or a committed safety overrun, may carry output. Physical truth
		// stays in the private frame; retry must resolve the ORIGINAL.
		if commitErr != nil {
			return usRestrictionsDispatchResult{}, AckResult{}, commitErr
		}
		if inv.ack {
			return usRestrictionsDispatchResult{}, floor, nil
		}
		if historical {
			return out, floor, nil
		}
		// Only a submission that actually composed effects is Applied. A bare
		// Resolve of a never-applied command (input == nil) stays Pending, so its
		// zero Value/Digest can never be delivered as an applied original.
		if inv.input != nil && out.Outcome.State == Pending {
			out.Outcome.State = Applied
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return usRestrictionsDispatchResult{}, AckResult{}, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return usRestrictionsDispatchResult{}, AckResult{}, commitErr
	}
}

// withdrawUSRestrictions rolls back and returns the zero closed result: no
// partial output, receipt or source effect survives any failure.
func withdrawUSRestrictions(turn *commandTurn, err error) (usRestrictionsDispatchResult, AckResult, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return usRestrictionsDispatchResult{}, AckResult{}, err
}

// usRestrictionsReceiptForCommand builds the receipt reference of a frozen
// preparation. It is a comparison value, never proof.
func usRestrictionsReceiptForCommand(c *usRestrictionsCommand) (usRestrictionsReceiptRequest, error) {
	if c == nil {
		return usRestrictionsReceiptRequest{}, ErrInvalid
	}
	return usRestrictionsReceiptRequest{ownerID: c.request.ownerID, kind: c.kind, reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}, nil
}
