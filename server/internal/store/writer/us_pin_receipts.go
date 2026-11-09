package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Private receipt adapter and closed composition for the two profile-PIN
// primitives (532 set, 533 clear). It consumes US-01's current-source authority
// stamp, W's classifier and US-02's bounded scalar fence; it mints no authority,
// registers no accepting command and reconstructs no original from live target
// source. Replay reads ONLY the retained header plus its typed scalars.
//
// Settled here, from the US-07 dispatcher's carry-overs:
//
//   - Retained receipt quota. An owner retains at most 64 receipts and 1 MiB of
//     variable receipt bytes. A fresh result that would pass either bound is
//     refused with RefusalCapacity BEFORE any source effect.
//   - Other sessions are gated WITHOUT enumeration. The product's PINReset
//     rewrites every family's pin_gate; this bounded component instead advances
//     the one scalar profile-PIN fence generation. Every other family's witness
//     then names a stale generation and is gated, while the acting family (only)
//     receives a fresh witness when the edit is its own profile. No family is
//     scanned, rewritten or counted.
//   - No event. A profile-PIN change appends no api_event in the pinned product
//     (only membership changes publish authority.changed); the immutable receipt
//     and the fence generation are the whole co-commit hint.
//   - Hash outside writer. The prepared bcrypt envelope is frozen before the
//     turn; this file never computes or verifies a hash, and no hash leaves the
//     native effect, receipt, result, image, log or error.

const (
	usPINOwnerReceipts     int64 = 64
	usPINOwnerReceiptBytes int64 = 1 << 20

	// Ten dispatcher-owned rechecks bound one fresh dispatch: the entry recheck
	// before any owner or payload read, the scoped owner re-read that settles
	// retirement/quota, the lookup custody's entry and deferred pre-terminal
	// rechecks plus the lookup release check, the store's own entry and deferred
	// rechecks plus the store custody's entry and deferred pre-terminal checks,
	// and the final current disclosure before the terminal.
	usPINDispatchRechecks int64 = 10
	usPINRecheckBytes     int64 = 64 << 10
)

type usPINInvocation struct {
	request usPolicyReceiptRequest
	input   *usPINCommand
	ack     bool
}

// usPINDispatchResult is the closed result of one dispatch call. Image is
// present only for the fresh original response. Replay returns the saved
// complete primitive result and never a source-derived image.
type usPINDispatchResult struct {
	Outcome ReceiptOutcome
	Value   usPINSavedResult
	Digest  ResultDigest
	Image   usPINPublicImage

	// Saved is the closed image codec captured at birth. Replay decodes these
	// exact bytes with zero statements; it never re-reads source or adapts one
	// arm into the other. Durable receipt retention awaits the bounded 532/533
	// result_body DDL (the canonical receipt CHECK still forces result_body NULL).
	Saved []byte

	// Exact accounting for this command, not a quota claim.
	ChargedBytes int64
	ImageBytes   int64
	OwnerCount   int64
	OwnerBytes   int64
}

func (usPINDispatchResult) String() string   { return "usPINDispatchResult{redacted}" }
func (usPINDispatchResult) GoString() string { return "usPINDispatchResult{redacted}" }
func (usPINDispatchResult) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usPINDispatchResult{redacted}"))
}

// usPINOutcome is the closed dispatch completion. It deliberately carries no
// released command result type or bare AckResult: the release rule runs BEFORE
// the physical terminal, so only a LIFETIME-based deferred finish there would
// misreport a committed command as canceled. The deferred final publication in
// runUSPINDispatch uses the CALLER's context instead.
type usPINOutcome struct {
	Result     usPINDispatchResult
	Floor      AckResult
	Historical bool
}

func (usPINOutcome) String() string   { return "usPINOutcome{redacted}" }
func (usPINOutcome) GoString() string { return "usPINOutcome{redacted}" }
func (usPINOutcome) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usPINOutcome{redacted}"))
}

func usPINReceiptForCommand(c *usPINCommand) (usPolicyReceiptRequest, error) {
	if c == nil {
		return usPolicyReceiptRequest{}, ErrInvalid
	}
	return usPolicyReceiptRequest{ownerID: c.request.ownerID, kind: c.request.prepared.kind,
		reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}, nil
}

// usPINDispatchFund charges the dispatcher's declared authority rechecks and the
// measured body bytes against the parent's funded memory. It never opens a
// second ledger: the parent's grant already reserves this memory.
func usPINDispatchFund(a *usOwnerAuthorization, body int64) error {
	if a == nil || body < 0 || body > maxCanonicalResultBytes {
		return ErrInvalid
	}
	need := uint64(usPINDispatchRechecks)*uint64(usPINRecheckBytes) + uint64(body)*32
	if need > a.budget.memory {
		return &Refusal{Code: RefusalUnqualified}
	}
	return nil
}

// usPINQuotaAdmit refuses a fresh result that would pass either retained bound
// BEFORE any source effect. count/bytes are W's exact counters.
func usPINQuotaAdmit(owner usPolicyOwnerFacts, charge int64) error {
	if !owner.exists || owner.retiredMS != 0 {
		return ErrOwnerUnavailable
	}
	if charge <= 0 || charge > usPINOwnerReceiptBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.count >= usPINOwnerReceipts || owner.bytes > usPINOwnerReceiptBytes-charge {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.lastSequence != owner.acknowledgedSequence {
		// The next sequence requires a durable predecessor ACK; that is an
		// ordinary protocol condition, not a capacity refusal.
		return ErrResolveRequired
	}
	return nil
}

// readUSPINQuota re-reads the retained owner under the CURRENT issuer scope
// before classification, so retirement and capacity are settled before any
// source effect. It reuses US-06's scoped owner point and count/byte facts.
func readUSPINQuota(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usPolicyOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !usIdentifier(ownerID) || turn == nil || a == nil {
		return out, ErrInvalid
	}
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	return readUSPolicyOwner(ctx, turn, a, ownerID)
}

// usPINCustody is the shared fixed-width owner/binding custody for one PIN
// original. Each purpose owns its own native header/result decoder.
type usPINCustody struct {
	owner    usPolicyOwnerFacts
	decision receiptDecision
	binding  receiptFacts
}

// Current stamped authority MUST precede every native owner/receipt read. A
// final denial REPLACES the body outcome and zeroes the output.
func lookupUSPINCustody(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest) (out usPINCustody, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usPINCustody{}, denial
		}
	}()
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	if _, e := usPINCodec(q.kind); e != nil || !usIdentifier(q.ownerID) || turn == nil ||
		q.kind != turn.transaction.descriptor.ID || q.reference.Identity.Sequence <= 0 ||
		q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) ||
		q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, err
	}
	out.owner, err = readUSPolicyOwner(ctx, turn, a, q.ownerID)
	if err != nil {
		return out, err
	}
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if !out.owner.exists || out.owner.incarnation != expected.command.Incarnation {
		_, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, receiptFacts{})
		return out, err
	}
	binding, err := readUSPolicyBinding(ctx, turn, q)
	if err != nil {
		return out, err
	}
	out.decision, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, binding)
	if err == nil && out.decision == receiptFresh && out.owner.retiredMS != 0 {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	out.binding = binding
	return out, nil
}

type usPINReceiptLookup struct {
	custody usPINCustody
	result  usPINSavedResult
	digest  ResultDigest
}

// lookupUSPIN serves only the retained original. Fresh target CAS, source reads
// and image construction are absent here, so replay never touches live source.
func lookupUSPIN(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest) (out usPINReceiptLookup, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usPINReceiptLookup{}, denial
		}
	}()
	if _, err = usPINCodec(q.kind); err != nil {
		return out, err
	}
	custody, e := lookupUSPINCustody(ctx, turn, a, q)
	if e != nil {
		return out, e
	}
	out.custody = custody
	if out.custody.decision != receiptReplay {
		return out, nil
	}
	binding := custody.binding
	if binding.resultCount != 0 {
		return out, ErrResolveRequired
	}
	codec, _ := usPINCodec(q.kind)
	row, err := usPolicyRow(ctx, turn, usPolicyReceiptOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var actualBytes int64
	if err = row.Scan(&actualBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = a.budget.check(ctx, turn, actualBytes); err != nil {
		return out, err
	}
	row, err = usPolicyRow(ctx, turn, usPINReceiptHeader, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var kind, version, pin, changed, count, headerBytes int64
	var actualCodec string
	v := usPINSavedResult{owner: q.ownerID, kind: q.kind}
	if err = row.Scan(&kind, &version, &actualCodec, &v.appliedMS, &v.account, &v.member, &v.profile, &v.lifetime,
		&v.revision, &v.pinRevision, &pin, &changed, &count, &headerBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if kind != int64(q.kind) || version != 1 || actualCodec != codec || count != 0 || changed != 1 ||
		(pin != 0 && pin != 1) || headerBytes != actualBytes {
		return out, ErrResolveRequired
	}
	v.pinRequired = pin == 1
	expectedBytes, err := usPINResultVariableBytes(v)
	if err != nil || expectedBytes != actualBytes {
		return out, errors.Join(ErrResolveRequired, err)
	}
	variant, err := deriveUSPINResultVariant(q.kind, a.proof.witness.issuer.account, v.account)
	if err != nil {
		return out, err
	}
	out.digest, err = usPINResultDigest(q.reference.Identity, q.reference.RequestDigest, variant, v)
	if err != nil || out.digest != ResultDigest(binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result = v
	return out, turn.checkLifetimes(ctx)
}

// storeUSPINResult is the native immutable save: typed receipt header, exact
// owner byte/count debt and nothing else in ONE frame. The fresh source effect
// and the scalar fence were applied earlier in this same frame. No event, ACK or
// discovery of another family happens here.
func storeUSPINResult(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest, v usPINSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() {
		charged, err = finishCommand(ctx, commandLifetime(turn), charged, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			charged, err = 0, denial
		}
	}()
	if turn == nil || turn.transaction == nil || q.kind != v.kind || turn.transaction.descriptor.ID != q.kind || v.owner != q.ownerID ||
		q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) ||
		q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) || digest == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if err = a.recheck(ctx, turn); err != nil {
		return 0, err
	}
	codec, err := usPINCodec(q.kind)
	if err != nil {
		return 0, err
	}
	// Owner facts are never caller-supplied. Re-read the owner under the CURRENT
	// issuer scope and require the original identity to classify fresh in the
	// same frame, so a stale prefix or a foreign issuer cannot write.
	custody, err := lookupUSPINCustody(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if custody.decision != receiptFresh {
		return 0, ErrConflict
	}
	owner := custody.owner
	if !owner.exists || owner.incarnation != q.reference.Identity.Incarnation {
		return 0, ErrResolveRequired
	}
	expectedBytes, err := usPINResultVariableBytes(v)
	if err != nil || expectedBytes <= 0 {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	variant, err := deriveUSPINResultVariant(q.kind, a.proof.witness.issuer.account, v.account)
	if err != nil {
		return 0, err
	}
	if actual, e := usPINResultDigest(q.reference.Identity, q.reference.RequestDigest, variant, v); e != nil || actual != digest {
		return 0, errors.Join(ErrResolveRequired, e)
	}
	// The SAME operation may not re-store its result at another retained
	// sequence. Detect that explicitly so a UNIQUE violation never surfaces as a
	// raw SQLite error, and never overwrite the original receipt.
	opRow, err := usPolicyRow(ctx, turn, usPolicyReceiptOperation, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.OperationID[:])
	if err != nil {
		return 0, err
	}
	var opSequence int64
	switch scanErr := opRow.Scan(&opSequence); {
	case scanErr == nil:
		if opSequence == q.reference.Identity.Sequence {
			return 0, ErrResolveRequired
		}
		return 0, ErrConflict
	case errors.Is(scanErr, sql.ErrNoRows):
	default:
		return 0, scanErr
	}
	row, err := usPolicyRow(ctx, turn, usPINReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:],
		v.account, v.member, v.profile, v.lifetime, v.revision, int64(1), v.pinRevision, usPolicyBool(v.pinRequired), int64(0))
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&charged); err != nil {
		return 0, err
	}
	if charged != expectedBytes {
		return 0, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPolicyOwnerAdvance, q.reference.Identity.Sequence, charged, q.ownerID,
		q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+charged {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}

// Floor-only ACK: it advances one durable sequence and refunds/deletes nothing.
func acknowledgeUSPINAfterAuthority(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest) (ack int64, err error) {
	defer func() {
		ack, err = finishCommand(ctx, commandLifetime(turn), ack, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			ack, err = 0, denial
		}
	}()
	lookup, err := lookupUSPIN(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if lookup.custody.owner.retiredMS != 0 {
		return 0, ErrOwnerUnavailable
	}
	if lookup.custody.decision == receiptRetired {
		return lookup.custody.owner.acknowledgedSequence, nil
	}
	if lookup.custody.decision != receiptReplay || q.reference.Identity.Sequence != lookup.custody.owner.lastSequence {
		return 0, ErrConflict
	}
	row, err := usPolicyRow(ctx, turn, usPolicyOwnerAck, q.reference.Identity.Sequence, q.ownerID,
		q.reference.Identity.Incarnation[:], lookup.custody.owner.lastSequence, lookup.custody.owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&ack); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if ack != q.reference.Identity.Sequence {
		return 0, ErrResolveRequired
	}
	return ack, turn.checkLifetimes(ctx)
}

// executeUSPINInvocation is the closed algorithm of the unpublished
// us_pin_composition draft, adapted to the merged US-01/US-02/US-04/US-06 names
// and to US-07's terminal ordering. It returns Pending for a fresh effect: only
// the physical terminal may declare Applied.
func executeUSPINInvocation(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usPINInvocation) (out usPINOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || a == nil || inv == nil {
		return out, ErrInvalid
	}
	kind := turn.transaction.descriptor.ID
	q := inv.request
	if q.kind != kind || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) ||
		q.reference.Identity.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if _, e := usPINCodec(kind); e != nil {
		return out, ErrInvalid
	}
	// An earlier prefix in this same frame may have advanced a source row, so
	// current authority is rechecked before ANY owner or payload read.
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, err
	}
	if err = usPINDispatchFund(a, 0); err != nil {
		return out, err
	}
	if inv.ack {
		if inv.input != nil {
			return out, ErrInvalid
		}
		out.Floor.AcknowledgedThrough, err = acknowledgeUSPINAfterAuthority(ctx, turn, a, q)
		out.Historical = true
		return out, err
	}
	// A submission to apply is admitted against the CURRENT retained owner
	// before classification: retirement may not be resurrected by a write, and
	// the quota is refused before any source effect. A bare Resolve/replay reads
	// the owner through the receipt adapter instead.
	if inv.input != nil {
		quota, e := readUSPINQuota(ctx, turn, a, q.ownerID)
		if e != nil {
			return out, e
		}
		if quota.exists && quota.retiredMS != 0 && q.reference.Identity.Sequence > quota.lastSequence {
			return out, ErrOwnerUnavailable
		}
	}
	lookup, err := lookupUSPIN(ctx, turn, a, q)
	if err != nil {
		return out, err
	}
	out.Result.Outcome.Reference = q.reference
	out.Result.OwnerCount, out.Result.OwnerBytes = lookup.custody.owner.count, lookup.custody.owner.bytes
	switch lookup.custody.decision {
	case receiptRetired:
		out.Result.Outcome.State = ResultRetired
		out.Historical = true
		return out, nil
	case receiptReplay:
		out.Result.Outcome.State = Applied
		out.Result.Outcome.Replayed = true
		out.Result.Value, out.Result.Digest = lookup.result, lookup.digest
		out.Historical = true
		return out, nil
	case receiptFresh:
		out.Result.Outcome.State = Pending
	default:
		return out, ErrResolveRequired
	}
	if inv.input == nil {
		// Preparation/status only: no effect, no receipt, no image.
		return out, nil
	}
	input := inv.input
	target := input.request.prepared.request
	if input.request.prepared.kind != q.kind || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest ||
		input.request.ownerID != q.ownerID || input.request.issuer != a.proof.witness.issuer {
		return out, ErrConflict
	}
	// A fresh submission must be the exact frozen preparation. The historical
	// lookup above is deliberately independent of a now-stale preparation.
	digest, err := canonicalRequestDigest(input.request.prepared.kind, 1, input.identity, input.request)
	if err != nil || digest != input.digest {
		return out, errors.Join(ErrConflict, err)
	}
	// The US-01 stamp is a current direct-owner main-profile recipe. This first
	// PIN composition admits only that caller's OWN account. A managed reset of
	// another account needs its own reviewed tier authority; a target membership
	// role alone is not proof and is explicitly refused here.
	issuer := a.proof.witness.issuer
	if target.account != issuer.account {
		return out, ErrDisclosureDenied
	}
	if err = a.budget.check(ctx, turn, int64(len(target.hash))); err != nil {
		return out, err
	}
	row, err := usPolicyRow(ctx, turn, usPINTargetSelf, target.account, input.request.memberIncarnation, target.profile, target.profileLifetime)
	if err != nil {
		return out, err
	}
	var revision, pinRevision int64
	if err = row.Scan(&revision, &pinRevision); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if revision != target.expectedRevision || pinRevision != target.expectedPINRevision {
		return out, ErrConflict
	}
	nowMS, err := usPolicyAppliedMS(turn)
	if err != nil {
		return out, err
	}
	v := usPINSavedResult{kind: q.kind, owner: q.ownerID, account: target.account, member: input.request.memberIncarnation,
		profile: target.profile, lifetime: target.profileLifetime, revision: revision + 1, pinRevision: pinRevision + 1,
		pinRequired: q.kind == usPINSet, appliedMS: nowMS}
	// Reserve the complete prospective immutable result BEFORE any source effect.
	charge, err := usPINResultVariableBytes(v)
	if err != nil {
		return out, err
	}
	if err = usPINQuotaAdmit(lookup.custody.owner, charge); err != nil {
		return out, err
	}
	if err = a.budget.check(ctx, turn, charge+int64(len(target.hash))); err != nil {
		return out, err
	}
	if err = usPINDispatchFund(a, charge); err != nil {
		return out, err
	}
	variant, err := deriveUSPINResultVariant(q.kind, issuer.account, target.account)
	if err != nil {
		return out, err
	}
	out.Result.Digest, err = usPINResultDigest(q.reference.Identity, q.reference.RequestDigest, variant, v)
	if err != nil {
		return out, err
	}
	effect, err := applyUSPIN(ctx, turn, &input.request.prepared)
	if err != nil {
		return out, err
	}
	if effect.account != v.account || effect.profile != v.profile || effect.lifetime != v.lifetime ||
		effect.revision != v.revision || effect.pinRevision != v.pinRevision || effect.pinRequired != v.pinRequired {
		return out, ErrResolveRequired
	}
	// One proof-checked keep-family, only when the edit is the acting session's
	// own profile: a SET then leaves that family un-gated at the new generation.
	// Every other family keeps its old witness and is gated by generation alone.
	var keep *usPINKeepProof
	if target.account == issuer.account && target.profile == issuer.profile && target.profileLifetime == issuer.profileLifetime &&
		input.request.memberIncarnation == issuer.memberIncarnation && q.kind == usPINSet {
		keep = &usPINKeepProof{frame: turn.transaction, issuer: issuer}
	}
	scope := usPINFenceScope{account: v.account, member: v.member, profile: v.profile, lifetime: v.lifetime}
	fence, err := advanceUSProfilePINFence(ctx, turn, scope, nowMS, keep)
	if err != nil {
		return out, err
	}
	if fence.generation < 1 || fence.revision < 1 {
		return out, ErrResolveRequired
	}
	charged, err := storeUSPINResult(ctx, turn, a, q, v, out.Result.Digest)
	if err != nil || charged != charge {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.Result.Value, out.Result.ChargedBytes = v, charged
	out.Result.OwnerCount, out.Result.OwnerBytes = lookup.custody.owner.count+1, lookup.custody.owner.bytes+charged
	// The arm is derived from the validated entry/authority/target above, never
	// from request input. Both arms are captured in this SAME frame after the
	// effect; neither is ever rebuilt from live source on replay.
	var image usPINPublicImage
	var imageBytes int64
	switch variant {
	case usPINVariantCallerSnapshot:
		snapshot, bytes, e := captureUSPINSelfSnapshot(ctx, turn, a, variant, scope, v, nowMS)
		if e != nil {
			return out, e
		}
		image, imageBytes = usPINPublicImage{Snapshot: snapshot}, bytes
	case usPINVariantAdministrativeProfile:
		profile, bytes, e := captureUSPINProfileImage(ctx, turn, a, variant, scope, v)
		if e != nil {
			return out, e
		}
		image, imageBytes = usPINPublicImage{Profile: profile}, bytes
	default:
		return out, ErrResolveRequired
	}
	saved, err := image.Encode()
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.Result.Image, out.Result.ImageBytes, out.Result.Saved = image, imageBytes, saved
	return out, turn.checkLifetimes(ctx)
}

// runUSPINDispatch is the closed test/harness entry. It owns the final current
// disclosure and the FULL physical terminal; the caller supplies only the
// already admitted turn.
func runUSPINDispatch(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usPINInvocation) (out usPINOutcome, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, out, err)
		out, err = v, e
	}()
	out, err = executeUSPINInvocation(ctx, turn, a, inv)
	if err != nil {
		return withdrawUSPIN(turn, err)
	}
	// Final current disclosure before the terminal: a release-time denial
	// REPLACES the body outcome and rolls the whole prefix back.
	if denial := a.recheck(ctx, turn); denial != nil {
		return withdrawUSPIN(turn, denial)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUSPIN(turn, ctxErr)
	}
	// The release rule runs BEFORE the terminal: the transaction cancels its own
	// lifetime at commit/rollback, so this release uses the live frame lifetime.
	// The deferred final publication rechecks the CALLER's context after
	// physical return. It mirrors dispatchCommand's caller-context recheck but
	// does not join currentCommandDisclosure / tx.lifetimeErr; before any
	// production entry that defer must also check the in-memory a.lifetime.Err()
	// and turn.transaction.lifetimeErr. Do NOT call a.recheck there: it denies
	// once transaction.done is set (us_authority.go:296).
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), out, nil)
	if releaseErr != nil {
		return withdrawUSPIN(turn, releaseErr)
	}
	out = release
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUSPIN(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		// No error, including a late cleanup failure or a committed safety
		// overrun, may carry output. Retry must resolve the ORIGINAL.
		if commitErr != nil {
			return usPINOutcome{}, commitErr
		}
		if inv.ack {
			return usPINOutcome{Floor: out.Floor}, nil
		}
		if out.Historical {
			return out, nil
		}
		// Only a submission that actually composed effects is Applied. A bare
		// Resolve of a never-applied command stays Pending.
		if inv.input != nil && out.Result.Outcome.State == Pending {
			out.Result.Outcome.State = Applied
		}
		return out, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return usPINOutcome{}, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return usPINOutcome{}, commitErr
	}
}

// withdrawUSPIN rolls back and returns the zero closed result: no partial
// output, receipt, image or source effect survives any failure.
func withdrawUSPIN(turn *commandTurn, err error) (usPINOutcome, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return usPINOutcome{}, err
}
