package writer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// The one MG accepting composition for the four source administration
// commands 808/809/810/818. A compiled descriptor is metadata only: this entry
// reuses MG-02's current owner/source frame for authority, MG-07's ONE public
// UO root adapter for the retained receipt, and only the already-merged
// mg_source effects. Replay rechecks current authority/root and serves the saved
// original with zero effects; an unknown COMMIT is never Applied.
//
// Bounds: one source result, one retained receipt header and one owner advance
// per accepted turn. The result is capped by mgSourceCommandResultCap; one root
// owner retains at most mgSourceOwnerReceipts receipts / mgSourceOwnerBytes.
const (
	mgSourceOwnerReceipts int64 = 64
	mgSourceOwnerBytes    int64 = 1 << 20

	mgSourceCommandRequestVersion = 1
)

// The exact typed original request. Kind selects which fields are meaningful;
// every other field must stay zero so a digest and its body cannot disagree.
type mgSourceCommandRequest struct {
	Kind             CommandID
	Source           string
	ExpectedRevision int64

	Name         string
	Physical     int64
	OwnerLimit   int64
	UsePhysical  bool
	Enabled      bool
	ViewerAccess string
	Removal      string
}

func (q mgSourceCommandRequest) validate() error {
	if !mgSourceCommandKind(q.Kind) || !mpWorkerID(q.Source) || q.ExpectedRevision <= 0 || q.ExpectedRevision == math.MaxInt64 {
		return ErrInvalid
	}
	switch q.Kind {
	case MGConfigureTuner:
		if !mgSourceText(q.Name, true, 256) || q.Physical < 0 || q.Physical > 256 || q.OwnerLimit < 0 || q.OwnerLimit > 256 ||
			q.Enabled || q.ViewerAccess != "" || q.Removal != "" {
			return ErrInvalid
		}
	case MGSetSourceEnabled:
		if q.Name != "" || q.Physical != 0 || q.OwnerLimit != 0 || q.UsePhysical || q.ViewerAccess != "" || q.Removal != "" {
			return ErrInvalid
		}
	case MGSetSourceViewerAccess:
		if q.Name != "" || q.Physical != 0 || q.OwnerLimit != 0 || q.UsePhysical || q.Enabled ||
			(q.ViewerAccess != "owner-only" && q.ViewerAccess != "server-members") || q.Removal != "" {
			return ErrInvalid
		}
	case MGRetireSource:
		if q.Name != "" || q.Physical != 0 || q.OwnerLimit != 0 || q.UsePhysical || q.Enabled || q.ViewerAccess != "" ||
			!mgSourceText(q.Removal, true, 256) {
			return ErrInvalid
		}
	}
	total := len(q.Source) + len(q.Name) + len(q.ViewerAccess) + len(q.Removal)
	if total > maxCanonicalRequestBytes {
		return ErrInvalid
	}
	return nil
}

// A prepared original owns the canonical request digest. Only this constructor
// can create the reference; possession authorizes nothing.
type mgSourceCommand struct {
	request   mgSourceCommandRequest
	reference ReceiptReference
}

func newMGSourceCommand(kind CommandID, id CommandIdentity, q mgSourceCommandRequest) (*mgSourceCommand, error) {
	if q.Kind != kind || id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) || id.Sequence <= 0 {
		return nil, ErrInvalid
	}
	if err := q.validate(); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(kind, mgSourceCommandRequestVersion, id, q)
	if err != nil {
		return nil, err
	}
	q.Source, q.Name, q.ViewerAccess, q.Removal = strings.Clone(q.Source), strings.Clone(q.Name), strings.Clone(q.ViewerAccess), strings.Clone(q.Removal)
	return &mgSourceCommand{request: q, reference: ReceiptReference{Identity: id, RequestDigest: digest}}, nil
}

type mgSourceCommandInvocation struct {
	reference ReceiptReference
	request   mgSourceCommandRequest
	input     *mgSourceCommand
	ack       bool
}

type mgSourceCommandOutcome struct {
	Outcome      ReceiptOutcome
	Value        mgSourceCommandResult
	Digest       ResultDigest
	ChargedBytes int64
	OwnerCount   int64
	OwnerBytes   int64
}

func (mgSourceCommandOutcome) String() string   { return "mgSourceCommandOutcome{redacted}" }
func (mgSourceCommandOutcome) GoString() string { return "mgSourceCommandOutcome{redacted}" }
func (v mgSourceCommandOutcome) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("mgSourceCommandOutcome{redacted}"))
}

type mgSourceCommandCompletion struct {
	out   mgSourceCommandOutcome
	floor AckResult
}

func (mgSourceCommandCompletion) String() string   { return "mgSourceCommandCompletion{redacted}" }
func (mgSourceCommandCompletion) GoString() string { return "mgSourceCommandCompletion{redacted}" }
func (v mgSourceCommandCompletion) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("mgSourceCommandCompletion{redacted}"))
}

// MG-07 owns the ONE root adapter. This entry consumes its session, lookup,
// payload and persistence primitives unchanged; only the ACK floor statement is
// MG-03's own (MG-07 has no ACK owner yet).
func mgSourceCommandSession(ctx context.Context, turn *commandTurn, proof *mgActorProof, lifetime context.Context, root mgPersonalRootSeam) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if proof == nil {
		return nil, ErrDisclosureDenied
	}
	frozen, err := mgPersonalFreezeWitness(&proof.native)
	if err != nil {
		return nil, err
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
	if len(session.mgPersonalRootID()) != 22 || !session.mgPersonalRootIdentity().complete() {
		return nil, ErrDisclosureDenied
	}
	return session, nil
}

func mgSourceCommandQuotaAdmit(owner uoRootOwner, charge int64) error {
	if !owner.facts.exists {
		return ErrOwnerUnavailable
	}
	if charge <= 0 || charge > mgSourceOwnerBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.rows >= mgSourceOwnerReceipts || owner.variableBytes > mgSourceOwnerBytes-charge {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.facts.lastSequence != owner.facts.acknowledgedSequence {
		return ErrResolveRequired
	}
	return nil
}

// runMGSourceCommand is the accepting entry. It returns Pending for a fresh
// effect: only the caller's physical terminal may declare Applied. A replay
// returns Applied with Replayed=true. Every error returns a completely zero
// outcome, including the saved value and digest.
func runMGSourceCommand(ctx context.Context, turn *commandTurn, proof *mgActorProof, lifetime context.Context, root mgPersonalRootSeam, budget mgAuthorityBudget, inv *mgSourceCommandInvocation) (out mgSourceCommandOutcome, floor AckResult, err error) {
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), mgSourceCommandCompletion{out, floor}, err)
		out, floor, err = value.out, value.floor, e
	}()
	if proof == nil {
		return out, floor, ErrDisclosureDenied
	}
	if turn == nil || turn.transaction == nil || lifetime == nil || root == nil || inv == nil {
		return out, floor, ErrInvalid
	}
	kind := turn.transaction.descriptor.ID
	if !mgSourceCommandKind(kind) || inv.request.Kind != kind || !mpWorkerID(inv.request.Source) {
		return out, floor, ErrInvalid
	}
	q := mgSourceCommandRequest{Kind: kind, Source: inv.request.Source, ExpectedRevision: inv.request.ExpectedRevision,
		Name: inv.request.Name, Physical: inv.request.Physical, OwnerLimit: inv.request.OwnerLimit,
		UsePhysical: inv.request.UsePhysical, Enabled: inv.request.Enabled, ViewerAccess: inv.request.ViewerAccess, Removal: inv.request.Removal}
	if err = q.validate(); err != nil {
		return out, floor, err
	}
	reference := inv.reference
	if reference.Identity.Incarnation == ([16]byte{}) || reference.Identity.OperationID == ([16]byte{}) ||
		reference.Identity.Sequence <= 0 || reference.RequestDigest == (RequestDigest{}) {
		return out, floor, ErrInvalid
	}
	if inv.input != nil {
		rebuilt, e := newMGSourceCommand(kind, inv.input.reference.Identity, inv.input.request)
		if e != nil || rebuilt.reference != inv.input.reference || rebuilt.request != q || inv.input.reference != reference {
			return out, floor, errors.Join(ErrConflict, e)
		}
	}
	// Current owner/source authority precedes ANY receipt owner or payload read.
	frame, err := openMGSourceFrame(ctx, turn, proof, lifetime, q.Source, budget)
	if err != nil {
		return out, floor, err
	}
	session, err := mgSourceCommandSession(ctx, turn, proof, lifetime, root)
	if err != nil {
		return out, floor, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: kind, identity: reference.Identity, request: reference.RequestDigest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, floor, err
	}
	if inv.ack {
		if inv.input != nil {
			return mgSourceCommandOutcome{}, floor, ErrInvalid
		}
		floor.AcknowledgedThrough, err = acknowledgeMGSourceCommand(ctx, turn, frame, session, ref, lookup)
		return out, floor, err
	}
	out.Outcome.Reference = reference
	out.OwnerCount, out.OwnerBytes = lookup.owner.rows, lookup.owner.variableBytes
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		if err = mgSourceCommandRecheck(ctx, turn, frame, session); err != nil {
			return mgSourceCommandOutcome{}, floor, err
		}
		return out, floor, nil
	case receiptReplay:
		value, digest, e := replayMGSourceCommandResult(ctx, turn, frame, session, lookup, reference)
		if e != nil {
			return mgSourceCommandOutcome{}, floor, e
		}
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = value, digest
		return out, floor, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, ErrResolveRequired
	}
	if inv.input == nil {
		// Preparation/status only: no effect, no receipt, no advance.
		return out, floor, nil
	}
	if inv.input.reference != reference {
		return mgSourceCommandOutcome{}, floor, ErrConflict
	}
	if err = mgSourceCommandQuotaAdmit(lookup.owner, mgSourceCommandResultCap); err != nil {
		return mgSourceCommandOutcome{}, floor, err
	}
	before, e := mgReadSource(ctx, turn, q.Source)
	if e != nil {
		return mgSourceCommandOutcome{}, floor, e
	}
	if before.revision != q.ExpectedRevision {
		return mgSourceCommandOutcome{}, floor, ErrConflict
	}
	result, e := applyMGSourceCommand(ctx, turn, before, inv.input.request)
	if e != nil {
		return mgSourceCommandOutcome{}, floor, e
	}
	value := mgSourceCommandResult{kind: kind, source: result.source, appliedMS: result.appliedMS}
	if e = preflightMGSourceCommandResult(value); e != nil {
		return mgSourceCommandOutcome{}, floor, e
	}
	if err = budget.check(ctx, turn, mgSourceCommandResultCap); err != nil {
		return mgSourceCommandOutcome{}, floor, err
	}
	payload, _, e := mgSourceCommandPayload(reference.Identity, reference.RequestDigest, value)
	if e != nil {
		return mgSourceCommandOutcome{}, floor, e
	}
	digest, e := mgPersonalRootPersist(ctx, turn, lookup, value, 1)
	if e != nil {
		return mgSourceCommandOutcome{}, floor, e
	}
	if err = mgSourceCommandRecheck(ctx, turn, frame, session); err != nil {
		return mgSourceCommandOutcome{}, floor, err
	}
	out.Value, out.Digest = value, digest
	out.ChargedBytes = int64(len(payload)) + uoRootFixedBytes
	out.OwnerCount, out.OwnerBytes = lookup.owner.rows+1, lookup.owner.variableBytes+out.ChargedBytes
	return out, floor, nil
}

// mgSourceCommandRecheck is the single current-authority/root release gate.
// Both stamps are current-turn objects; either denial zeroes every result.
func mgSourceCommandRecheck(ctx context.Context, t *commandTurn, frame *mgSourceFrame, session mgPersonalRootAuthorization) error {
	if frame == nil || session == nil {
		return ErrDisclosureDenied
	}
	if err := frame.recheck(ctx, t); err != nil {
		return err
	}
	return session.recheck(ctx, t)
}

// applyMGSourceCommand dispatches to the merged MG-02 native effects. It never
// authorizes; the caller already read this exact source row and ran the current
// source frame this turn.
func applyMGSourceCommand(ctx context.Context, turn *commandTurn, before mgSourceFacts, q mgSourceCommandRequest) (out mgSourceResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	switch q.Kind {
	case MGConfigureTuner:
		return mgConfigureSource(ctx, turn, before, mgSourceConfiguration{name: q.Name, physical: q.Physical, ownerLimit: q.OwnerLimit, usePhysical: q.UsePhysical})
	case MGSetSourceEnabled:
		return mgSetSourceEnabled(ctx, turn, before, q.Enabled)
	case MGSetSourceViewerAccess:
		return mgSetSourceAccess(ctx, turn, before, q.ViewerAccess)
	case MGRetireSource:
		return mgRetireSource(ctx, turn, before, q.Removal)
	}
	return out, ErrInvalid
}

// replayMGSourceCommandResult reads the retained payload through MG-07's root
// adapter after current authority/root, and returns ONLY the decoded original.
// It never reads live source.
func replayMGSourceCommandResult(ctx context.Context, turn *commandTurn, frame *mgSourceFrame, session mgPersonalRootAuthorization, lookup mgPersonalRootLookup, reference ReceiptReference) (out mgSourceCommandResult, digest ResultDigest, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			value  mgSourceCommandResult
			digest ResultDigest
		}{out, digest}, err)
		out, digest, err = v.value, v.digest, e
	}()
	if lookup.decision != receiptReplay || lookup.resultCount != 1 || lookup.payloadBytes < 1 || lookup.payloadBytes > mgSourceCommandResultCap {
		return out, digest, ErrResolveRequired
	}
	payload, stored, err := mgPersonalRootReadPayload(ctx, turn, lookup)
	if err != nil {
		return out, digest, err
	}
	out, err = decodeMGSourceCommandResult(reference.Identity, reference.RequestDigest, payload, stored)
	if err != nil {
		return out, digest, err
	}
	digest, err = canonicalResultDigest(reference.Identity, reference.RequestDigest, out)
	if err != nil || digest != stored {
		return out, digest, errors.Join(ErrResolveRequired, err)
	}
	if denial := mgSourceCommandRecheck(ctx, turn, frame, session); denial != nil {
		return mgSourceCommandResult{}, ResultDigest{}, denial
	}
	return out, digest, nil
}

// acknowledgeMGSourceCommand advances only the durable owner floor. It cannot
// decode, serve or refund the original and is idempotent at an existing floor.
// MG-26 owns receipt reclaim; this is the source-family ACK cell.
func acknowledgeMGSourceCommand(ctx context.Context, turn *commandTurn, frame *mgSourceFrame, session mgPersonalRootAuthorization, ref uoRootReference, lookup mgPersonalRootLookup) (ack int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), ack, err)
		ack, err = v, e
	}()
	if err = session.recheck(ctx, turn); err != nil {
		return 0, err
	}
	owner := lookup.owner
	switch lookup.decision {
	case receiptRetired:
		return owner.facts.acknowledgedSequence, nil
	case receiptReplay:
	default:
		return 0, ErrConflict
	}
	if ref.identity.Sequence != owner.facts.lastSequence {
		return 0, ErrConflict
	}
	row, e := mgPersonalRootQuery(ctx, turn, mgSourceCommandOwnerAck, ref.identity.Sequence, ref.root, ref.identity.Incarnation[:], owner.facts.lastSequence, owner.facts.acknowledgedSequence)
	if e != nil {
		return 0, e
	}
	if err = row.Scan(&ack); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if ack != ref.identity.Sequence {
		return 0, ErrResolveRequired
	}
	if err = mgSourceCommandRecheck(ctx, turn, frame, session); err != nil {
		return 0, err
	}
	return ack, nil
}
