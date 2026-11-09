package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The COMPLETE private 520 command: the ordinary LOCAL owner password
// replacement with its immutable void-response receipt and retained quota.
// It is a private test/harness composition: no public Writer method, no product
// caller, no exported constructor and no acceptance flow. The harness supplies
// an already begun FULL private turn; this file owns original/replay/ACK
// classification, the receipt, the retained quota, the scalar source CAS and the
// physical terminal.
//
// Password material never enters this file's outputs. The two bcrypt images are
// private request witnesses: the next image is written once to
// accounts.password_hash and neither image is copied into a result, receipt,
// event, error or log. The request digest binds the complete frozen request
// one-way; only SHA-256 output is retained.

const (
	usPasswordCodecName = "password_replace_v1"

	// Retained receipt quota of ONE security owner: at most 64 originals and
	// 1 MiB of variable receipt bytes. A fresh result that would pass either
	// bound is refused with RefusalCapacity BEFORE any source effect. These are
	// exact counters, not a product user cap; US-26 reclaim refunds actual bytes.
	usPasswordOwnerReceipts     int64 = 64
	usPasswordOwnerReceiptBytes int64 = 1 << 20

	// Nine current-authority checks bound one fresh 520 dispatch: the entry
	// recheck before any owner or payload read, the scoped owner re-read that
	// settles retirement/quota, BOTH custody lookups (lookup and store, each
	// with an entry and a deferred pre-CAS recheck), the lookup release check,
	// and the store's entry and deferred (pre-CAS) rechecks. The fresh path's
	// final disclosure is verifyUSPasswordPostState instead of a tenth stamp.
	// This is a funded forecast, not a user quota.
	usPasswordDispatchRechecks int64 = 9
	usPasswordRecheckBytes     int64 = 64 << 10
)

// usPasswordCommand is one frozen ordinary password replacement. It owns no
// writer, grant, receipt verdict or Applied result. The previous image is a
// comparison witness from the caller's independently verified proof; a
// well-formed bcrypt image proves no possession.
type usPasswordCommand struct {
	kind     CommandID
	owner    string // retained security receipt owner, not request authority
	identity CommandIdentity
	request  usPasswordReplaceRequest
	digest   RequestDigest
}

func (usPasswordCommand) String() string   { return "usPasswordCommand{redacted}" }
func (usPasswordCommand) GoString() string { return "usPasswordCommand{redacted}" }
func (usPasswordCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPasswordCommand")
}

// prepareUSPasswordCommand freezes the complete prepared request and its
// canonical one-way digest. It hashes nothing: the caller has already verified
// possession, enforced new-password policy and generated the next bcrypt image
// outside the writer turn.
func prepareUSPasswordCommand(identity CommandIdentity, ownerID string, in usPasswordReplaceRequest) (*usPasswordCommand, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || !usIdentifier(ownerID) {
		return nil, invalid("us_password_identity")
	}
	prepared, err := prepareUSPasswordReplace(in)
	if err != nil {
		return nil, err
	}
	request := prepared.request
	digest, err := canonicalRequestDigest(usLocalPasswordReplace, 1, identity, request)
	if err != nil {
		return nil, err
	}
	// The digest binds the complete frozen request, including the caller's
	// salted next image. A retry that regenerates the salt/next image is a
	// DIFFERENT request and conflicts with the saved receipt instead of
	// replaying it, so product wiring must persist the ReceiptReference (or the
	// whole frozen request) across retries. Replay is defined only for the exact
	// original identity and digest.
	return &usPasswordCommand{kind: usLocalPasswordReplace, owner: ownerID, identity: identity, request: request, digest: digest}, nil
}

// usPasswordSavedResult is the COMPLETE non-secret native post-state of one
// replacement, reconstructed only from its immutable receipt and typed child.
// It is data, never authority, and carries neither password image.
type usPasswordSavedResult struct {
	kind                                                 CommandID
	ownerID                                              string
	account, memberIncarnation, profile, profileLifetime string
	memberRevision, profileRevision                      int64
	previousEpoch, resultEpoch, appliedMS                int64
}

func usPasswordCodec(kind CommandID) (string, error) {
	if kind != usLocalPasswordReplace {
		return "", ErrInvalid
	}
	return usPasswordCodecName, nil
}

func validateUSPasswordResult(v usPasswordSavedResult) error {
	if _, err := usPasswordCodec(v.kind); err != nil {
		return err
	}
	if !usIdentifier(v.ownerID) || !usIdentifier(v.account) || !usHex(v.memberIncarnation, 32) ||
		!usIdentifier(v.profile) || !usHex(v.profileLifetime, 32) ||
		v.memberRevision < 1 || v.memberRevision > usMaxRevision ||
		v.profileRevision < 1 || v.profileRevision > usMaxRevision ||
		v.previousEpoch < 1 || v.previousEpoch >= math.MaxInt64 ||
		v.resultEpoch != v.previousEpoch+1 || v.appliedMS < 0 {
		return ErrResolveRequired
	}
	return nil
}

func cloneUSPasswordResult(v usPasswordSavedResult) usPasswordSavedResult {
	return v
}

func (usPasswordSavedResult) String() string   { return "usPasswordSavedResult{redacted}" }
func (usPasswordSavedResult) GoString() string { return "usPasswordSavedResult{redacted}" }
func (usPasswordSavedResult) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPasswordSavedResult")
}

// Closed v1 codec with an explicit field order over W's bounded primitives. The
// frozen request digest is bound as the ORIGINAL identity; no later source read
// contributes. No hash image is encoded.
func usPasswordResultDigest(identity CommandIdentity, request RequestDigest, v usPasswordSavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSPasswordResult(v); err != nil {
		return ResultDigest{}, err
	}
	codec, _ := usPasswordCodec(v.kind)
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	var header [4]byte
	binary.BigEndian.PutUint16(header[:2], uint16(v.kind))
	binary.BigEndian.PutUint16(header[2:], 1)
	e.bytes(header[:])
	e.text("users_security")
	e.text(codec)
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	for _, s := range []string{v.ownerID, v.account, v.memberIncarnation, v.profile, v.profileLifetime} {
		e.text(s)
	}
	for _, n := range []int64{v.memberRevision, v.profileRevision, v.previousEpoch, v.resultEpoch, v.appliedMS} {
		e.number(uint64(n))
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// Exact logical variable-column charge of the header plus its typed child, NOT
// index/page/WAL bytes or a quota.
const (
	usPasswordHeaderFixedBytes = 16 + 16 + 32 + 32 // incarnation, operation, request digest, result digest
	usPasswordChildFixedBytes  = 16 + 32 + 32      // incarnation, member incarnation, profile lifetime
)

func usPasswordResultVariableBytes(v usPasswordSavedResult) (int64, error) {
	if err := validateUSPasswordResult(v); err != nil {
		return 0, err
	}
	codec, _ := usPasswordCodec(v.kind)
	return int64(2*len(v.ownerID) + usPasswordHeaderFixedBytes + usPasswordChildFixedBytes +
		len(codec) + len(v.account) + len(v.profile)), nil
}

// usPasswordReceiptRequest is the ORIGINAL retained identity, never a retry
// identity and never authority.
type usPasswordReceiptRequest struct {
	ownerID   string
	kind      CommandID
	reference ReceiptReference
}

type usPasswordOwnerFacts struct {
	receiptOwnerFacts
	// originalFamily is retained provenance, not an equality predicate: the
	// owner is addressed by the exact issuer scope, so a renewed family of the
	// same installation still resolves.
	originalFamily                     string
	reclaimed, count, bytes, retiredMS int64
}

type usPasswordReceiptLookup struct {
	owner    usPasswordOwnerFacts
	decision receiptDecision
	result   usPasswordSavedResult
	digest   ResultDigest
}

func usPasswordReceiptKind(kind CommandID) error {
	_, err := usPasswordCodec(kind)
	return err
}

func usPasswordIssuerFacts(a *usOwnerAuthorization) usPolicyIssuer {
	if a == nil {
		return usPolicyIssuer{}
	}
	return a.proof.witness.issuer
}

func usPasswordReceiptForCommand(c *usPasswordCommand) (usPasswordReceiptRequest, error) {
	if c == nil || c.kind != usLocalPasswordReplace || !usIdentifier(c.owner) {
		return usPasswordReceiptRequest{}, ErrInvalid
	}
	return usPasswordReceiptRequest{ownerID: c.owner, kind: c.kind, reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}, nil
}

// usPasswordReceiptBinding reads only the fixed-width binding header before
// floor/result decoding.
func readUSPasswordBinding(ctx context.Context, turn *commandTurn, q usPasswordReceiptRequest) (out receiptFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	row, err := usPasswordQuery(ctx, turn, usPasswordReceiptBinding, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, count int64
	if err = row.Scan(&op, &request, &kind, &count, &digest); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || kind != int64(usLocalPasswordReplace) || count != 0 {
		return out, ErrResolveRequired
	}
	out.exists = true
	out.identity.command = q.reference.Identity
	copy(out.identity.command.OperationID[:], op)
	copy(out.identity.digest[:], request)
	copy(out.resultDigest[:], digest)
	out.kind = CommandID(kind)
	out.resultCount = count
	return out, nil
}

// The owner is addressed by BOTH its PK and the exact issuer/scope it was born
// with, so a different current issuer cannot read or advance another cohort.
func readUSPasswordOwner(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usPasswordOwnerFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	i := usPasswordIssuerFacts(a)
	row, err := usPasswordQuery(ctx, turn, usPasswordOwner, ownerID, i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.originalFamily, &out.lastSequence, &out.acknowledgedSequence, &out.reclaimed, &out.count, &out.bytes, &out.retiredMS); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(inc) != 16 || !usIdentifier(out.originalFamily) || out.lastSequence < 0 || out.acknowledgedSequence < 0 || out.acknowledgedSequence > out.lastSequence || out.lastSequence-out.acknowledgedSequence > 1 || out.reclaimed < 0 || out.reclaimed > out.acknowledgedSequence || out.count < out.lastSequence-out.acknowledgedSequence || out.bytes < 0 || out.retiredMS < 0 || (out.count == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	out.exists = true
	copy(out.incarnation[:], inc)
	return out, nil
}

type usPasswordCustody struct {
	owner    usPasswordOwnerFacts
	decision receiptDecision
	binding  receiptFacts
}

// Current stamped authority MUST precede every native owner/receipt read. The
// shared classifier sees only the ORIGINAL identity; fresh target CAS is absent,
// so replay never touches current password/account source.
func lookupUSPasswordCustody(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPasswordReceiptRequest) (out usPasswordCustody, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		// A final current-source denial REPLACES the body outcome. Joining it
		// would let a benign conflict also match the denial class.
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usPasswordCustody{}, denial
		}
	}()
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	if !usIdentifier(q.ownerID) || usPasswordReceiptKind(q.kind) != nil || q.kind != turn.transaction.descriptor.ID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, err
	}
	out.owner, err = readUSPasswordOwner(ctx, turn, a, q.ownerID)
	if err != nil {
		return out, err
	}
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if !out.owner.exists || out.owner.incarnation != expected.command.Incarnation {
		_, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, receiptFacts{})
		return out, err
	}
	binding, err := readUSPasswordBinding(ctx, turn, q)
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

func lookupUSPassword(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPasswordReceiptRequest) (out usPasswordReceiptLookup, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usPasswordReceiptLookup{}, denial
		}
	}()
	if err = usPasswordReceiptKind(q.kind); err != nil {
		return out, err
	}
	custody, e := lookupUSPasswordCustody(ctx, turn, a, q)
	if e != nil {
		return out, e
	}
	out.owner, out.decision = custody.owner, custody.decision
	if out.decision != receiptReplay {
		return out, nil
	}
	binding := custody.binding
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	header, err := readUSPasswordHeader(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if err = a.budget.check(ctx, turn, header.bytes); err != nil {
		return out, err
	}
	// Integer-only child preflight before any variable child text is read.
	childBytes, err := readUSPasswordChildOctets(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if childBytes <= 0 || header.bytes <= 0 {
		return out, ErrResolveRequired
	}
	if err = a.budget.check(ctx, turn, childBytes); err != nil {
		return out, err
	}
	child, err := readUSPasswordChild(ctx, turn, q)
	if err != nil {
		return out, err
	}
	v := usPasswordSavedResult{kind: q.kind, ownerID: q.ownerID, account: child.account, memberIncarnation: child.memberIncarnation,
		profile: child.profile, profileLifetime: child.profileLifetime, memberRevision: child.memberRevision,
		profileRevision: child.profileRevision, previousEpoch: child.previousEpoch, resultEpoch: child.resultEpoch,
		appliedMS: header.appliedMS}
	charge, err := usPasswordResultVariableBytes(v)
	if err != nil {
		return out, err
	}
	// The header and its typed child are separate generated columns; their exact
	// sum is the complete immutable charge.
	if charge != header.bytes+childBytes {
		return out, ErrResolveRequired
	}
	if err = a.budget.check(ctx, turn, charge); err != nil {
		return out, err
	}
	out.digest, err = usPasswordResultDigest(expected.command, expected.digest, v)
	if err != nil || out.digest != ResultDigest(binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result = v
	return out, turn.checkLifetimes(ctx)
}

type usPasswordHeaderFacts struct {
	appliedMS int64
	bytes     int64
}

func readUSPasswordHeader(ctx context.Context, turn *commandTurn, q usPasswordReceiptRequest) (out usPasswordHeaderFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	row, err := usPasswordQuery(ctx, turn, usPasswordReceiptHeader, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, version, count int64
	var codec string
	var targetAccount, targetMember sql.NullString
	var resultRevision sql.NullInt64
	if err = row.Scan(&op, &request, &kind, &version, &codec, &out.appliedMS, &digest, &targetAccount, &targetMember, &resultRevision, &count, &out.bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	wantCodec, _ := usPasswordCodec(q.kind)
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || !equalBytes(op, q.reference.Identity.OperationID[:]) || !equalBytes(request, q.reference.RequestDigest[:]) || kind != int64(q.kind) || version != 1 || codec != wantCodec || out.appliedMS < 0 || targetAccount.Valid || targetMember.Valid || resultRevision.Valid || count != 0 || out.bytes <= 0 {
		return out, ErrResolveRequired
	}
	return out, nil
}

func readUSPasswordChildOctets(ctx context.Context, turn *commandTurn, q usPasswordReceiptRequest) (out int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	row, err := usPasswordQuery(ctx, turn, usPasswordReceiptChildOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return out, nil
}

func readUSPasswordChild(ctx context.Context, turn *commandTurn, q usPasswordReceiptRequest) (out usPasswordSavedResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	row, err := usPasswordQuery(ctx, turn, usPasswordReceiptChild, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var account, member, profile, lifetime string
	if err = row.Scan(&account, &member, &profile, &lifetime, &out.memberRevision, &out.profileRevision, &out.previousEpoch, &out.resultEpoch); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if !usIdentifier(account) || !usHex(member, 32) || !usIdentifier(profile) || !usHex(lifetime, 32) {
		return out, ErrResolveRequired
	}
	out.account, out.memberIncarnation, out.profile, out.profileLifetime = account, member, profile, lifetime
	return out, nil
}

// Native immutable save: receipt header, typed child, then the exact owner
// count/byte debt in ONE frame. No event, source CAS or ACK here.
func storeUSPasswordResult(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPasswordReceiptRequest, v usPasswordSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() {
		charged, err = finishCommand(ctx, commandLifetime(turn), charged, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			charged, err = 0, denial
		}
	}()
	if turn == nil || turn.transaction == nil || q.kind != v.kind || turn.transaction.descriptor.ID != q.kind || v.ownerID != q.ownerID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) || digest == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if err = a.recheck(ctx, turn); err != nil {
		return 0, err
	}
	if err = usPasswordReceiptKind(q.kind); err != nil {
		return 0, err
	}
	// Owner facts are never caller-supplied. Re-read the owner under the
	// CURRENT issuer scope and require the original to classify fresh in the
	// same frame, so a stale prefix or a foreign issuer cannot write.
	custody, err := lookupUSPasswordCustody(ctx, turn, a, q)
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
	codec, _ := usPasswordCodec(q.kind)
	expectedBytes, err := usPasswordResultVariableBytes(v)
	if err != nil {
		return 0, err
	}
	if expectedBytes <= 0 {
		return 0, ErrResolveRequired
	}
	if actual, err := usPasswordResultDigest(q.reference.Identity, q.reference.RequestDigest, v); err != nil || actual != digest {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	// The SAME operation may not re-store its result at another retained
	// sequence. Detect it explicitly so a UNIQUE violation never surfaces as a
	// raw SQLite error, and never overwrite the original receipt.
	opRow, err := usPasswordQuery(ctx, turn, usPasswordReceiptOperation, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.OperationID[:])
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
	row, err := usPasswordQuery(ctx, turn, usPasswordReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:], int64(0))
	if err != nil {
		return 0, err
	}
	var headerBytes int64
	if err = row.Scan(&headerBytes); err != nil {
		return 0, err
	}
	child, err := usPasswordQuery(ctx, turn, usPasswordChildInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		v.account, v.memberIncarnation, v.profile, v.profileLifetime, v.memberRevision, v.profileRevision, v.previousEpoch, v.resultEpoch)
	if err != nil {
		return 0, err
	}
	var childBytes int64
	if err = child.Scan(&childBytes); err != nil {
		return 0, err
	}
	if headerBytes <= 0 || childBytes <= 0 || headerBytes > expectedBytes || childBytes != expectedBytes-headerBytes {
		return 0, ErrResolveRequired
	}
	row, err = usPasswordQuery(ctx, turn, usPasswordOwnerAdvance, q.reference.Identity.Sequence, headerBytes+childBytes, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+headerBytes+childBytes || headerBytes+childBytes != expectedBytes {
		return 0, ErrResolveRequired
	}
	return headerBytes + childBytes, turn.checkLifetimes(ctx)
}

// Floor-only ACK: it advances one durable sequence and refunds/deletes nothing.
func acknowledgeUSPasswordAfterAuthority(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPasswordReceiptRequest) (ack int64, err error) {
	defer func() {
		ack, err = finishCommand(ctx, commandLifetime(turn), ack, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			ack, err = 0, denial
		}
	}()
	lookup, err := lookupUSPassword(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if lookup.owner.retiredMS != 0 {
		return 0, ErrOwnerUnavailable
	}
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.reference.Identity.Sequence != lookup.owner.lastSequence {
		return 0, ErrConflict
	}
	row, err := usPasswordQuery(ctx, turn, usPasswordOwnerAck, q.reference.Identity.Sequence, q.ownerID, q.reference.Identity.Incarnation[:], lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
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

// usPasswordQuotaFacts is the scoped owner re-read with exact retained counters.
type usPasswordQuotaFacts struct {
	usPasswordOwnerFacts
	family string
}

func readUSPasswordQuota(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usPasswordQuotaFacts, err error) {
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
	i := usPasswordIssuerFacts(a)
	row, err := usPasswordQuery(ctx, turn, usPasswordOwner, ownerID, i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation)
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

// usPasswordQuotaAdmit refuses a fresh result that would pass either retained
// bound BEFORE any source effect.
func usPasswordQuotaAdmit(owner usPasswordOwnerFacts, charge int64) error {
	if !owner.exists || owner.retiredMS != 0 {
		return ErrOwnerUnavailable
	}
	if charge <= 0 || charge > usPasswordOwnerReceiptBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.count >= usPasswordOwnerReceipts || owner.bytes > usPasswordOwnerReceiptBytes-charge {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.lastSequence != owner.acknowledgedSequence {
		return ErrResolveRequired
	}
	return nil
}

// usPasswordDispatchFund charges the dispatcher's declared authority rechecks
// and the measured body bytes against the parent's funded memory.
func usPasswordDispatchFund(a *usOwnerAuthorization, body int64) error {
	if a == nil || body < 0 || body > maxCanonicalResultBytes {
		return ErrInvalid
	}
	need := uint64(usPasswordDispatchRechecks)*uint64(usPasswordRecheckBytes) + uint64(body)*32
	if need > a.budget.memory {
		return &Refusal{Code: RefusalUnqualified}
	}
	return nil
}

// usPasswordAppliedMS samples the trusted writer clock once for the immutable
// applied_ms and the quota decision. No real clock and no invented lease.
func usPasswordAppliedMS(turn *commandTurn) (int64, error) {
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	now := tx.writer.now()
	ms := now.UnixMilli()
	if ms < 0 {
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

type usPasswordInvocation struct {
	request usPasswordReceiptRequest
	input   *usPasswordCommand
	ack     bool
}

// usPasswordDispatchResult is the closed result of one dispatch call. A void
// public response carries no image: only the saved private result and digest.
type usPasswordDispatchResult struct {
	Outcome ReceiptOutcome
	Value   usPasswordSavedResult
	Digest  ResultDigest

	// Exact accounting for this command, not a quota claim.
	ChargedBytes int64
	OwnerCount   int64
	OwnerBytes   int64

	// historical marks an original resolved from its receipt (replay/retired)
	// rather than composed in this frame. It is never Applied evidence.
	historical bool
	// epochAdvanced marks the one fresh effect that moved accounts.epoch. The
	// last US-01 recheck ran immediately before it; release proves the exact
	// post-effect scalar state instead of re-running a now-stale stamp.
	epochAdvanced bool
}

func (usPasswordDispatchResult) String() string   { return "usPasswordDispatchResult{redacted}" }
func (usPasswordDispatchResult) GoString() string { return "usPasswordDispatchResult{redacted}" }
func (usPasswordDispatchResult) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usPasswordDispatchResult{redacted}"))
}

type usPasswordCompletion struct {
	out   usPasswordDispatchResult
	floor AckResult
}

func (usPasswordCompletion) String() string   { return "usPasswordCompletion{redacted}" }
func (usPasswordCompletion) GoString() string { return "usPasswordCompletion{redacted}" }
func (usPasswordCompletion) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usPasswordCompletion{redacted}"))
}

// executeUSPasswordInvocation is the composition of the merged US-01 owner
// stamp, the 520 source CAS and the retained receipt. It returns Pending for a
// fresh effect: only the physical terminal may declare Applied.
func executeUSPasswordInvocation(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usPasswordInvocation) (out usPasswordDispatchResult, floor AckResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), usPasswordCompletion{out, floor}, err)
		out, floor, err = v.out, v.floor, e
	}()
	if turn == nil || turn.transaction == nil || a == nil || inv == nil {
		return out, floor, ErrInvalid
	}
	kind := turn.transaction.descriptor.ID
	q := inv.request
	if kind != usLocalPasswordReplace || q.kind != kind || usPasswordReceiptKind(kind) != nil ||
		q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.Identity.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) {
		return out, floor, ErrInvalid
	}
	// An earlier prefix in this same frame may have advanced a source row, so
	// current authority is rechecked before ANY owner or payload read.
	if err = a.recheck(ctx, turn); err != nil {
		return out, floor, err
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, floor, err
	}
	if err = usPasswordDispatchFund(a, 0); err != nil {
		return out, floor, err
	}
	if inv.ack {
		if inv.input != nil {
			return out, floor, ErrInvalid
		}
		floor.AcknowledgedThrough, err = acknowledgeUSPasswordAfterAuthority(ctx, turn, a, q)
		return out, floor, err
	}
	// A submission to apply is admitted against the CURRENT retained owner
	// before classification: retirement may not be resurrected by a write, and
	// the quota is refused before any source effect.
	if inv.input != nil {
		quota, e := readUSPasswordQuota(ctx, turn, a, q.ownerID)
		if e != nil {
			return out, floor, e
		}
		if quota.exists && quota.retiredMS != 0 && q.reference.Identity.Sequence > quota.lastSequence {
			return out, floor, ErrOwnerUnavailable
		}
	}
	lookup, err := lookupUSPassword(ctx, turn, a, q)
	if err != nil {
		return out, floor, err
	}
	out.Outcome.Reference = q.reference
	out.OwnerCount, out.OwnerBytes = lookup.owner.count, lookup.owner.bytes
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		out.historical = true
		return out, floor, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		out.historical = true
		return out, floor, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, ErrResolveRequired
	}
	if inv.input == nil {
		// Preparation/status only: no effect, no receipt, no event.
		return out, floor, nil
	}
	input := inv.input
	if input.kind != q.kind || input.owner != q.ownerID || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest {
		return out, floor, ErrConflict
	}
	// The fresh submission must be the exact frozen preparation. The historical
	// lookup above is deliberately independent of a now-stale preparation.
	digest, err := canonicalRequestDigest(input.kind, 1, input.identity, input.request)
	if err != nil || digest != input.digest {
		return out, floor, errors.Join(ErrConflict, err)
	}
	// Ordinary OWN-ACCOUNT owner replacement only. The US-01 stamp proves a
	// current direct-owner main-profile session; a different target account,
	// profile lifetime, member incarnation or epoch is a different operation and
	// is refused rather than silently composed. This explicit narrowing is what
	// keeps the recipe from becoming a general cross-account product rule. This
	// equality is the ONLY barrier against replacing another account's password:
	// neither the source target read nor the scalar CAS ever sees the issuer, so
	// removing this predicate would let a caller's own verified images (and the
	// receipt below) be aimed at a foreign account whose current hash the caller
	// supplied as its "previous" witness.
	issuer := a.proof.witness.issuer
	t := input.request.target
	if t.account != issuer.account || t.profile != issuer.profile || t.memberIncarnation != issuer.memberIncarnation || t.profileLifetime != issuer.profileLifetime {
		return out, floor, ErrDisclosureDenied
	}
	if t.epoch != issuer.epoch {
		return out, floor, ErrConflict
	}
	nowMS, err := usPasswordAppliedMS(turn)
	if err != nil {
		return out, floor, err
	}
	v := usPasswordSavedResult{kind: q.kind, ownerID: q.ownerID, account: t.account, memberIncarnation: t.memberIncarnation,
		profile: t.profile, profileLifetime: t.profileLifetime, memberRevision: t.memberRevision, profileRevision: t.profileRevision,
		previousEpoch: t.epoch, resultEpoch: t.epoch + 1, appliedMS: nowMS}
	// Reserve the complete prospective immutable result BEFORE any source effect.
	charge, err := usPasswordResultVariableBytes(v)
	if err != nil {
		return out, floor, err
	}
	if err = usPasswordQuotaAdmit(lookup.owner, charge); err != nil {
		return out, floor, err
	}
	if err = usPasswordDispatchFund(a, charge); err != nil {
		return out, floor, err
	}
	// ORDER IS SECURITY-RELEVANT. The receipt (with its deferred current-source
	// recheck and exact owner scope) is stored while accounts.epoch is still the
	// stamped value; the scalar CAS runs immediately afterwards and is the LAST
	// authority-bearing act. A receipt failure therefore rolls back before any
	// source replacement, and the stale stamp cannot be re-run after the
	// command's own epoch advance.
	out.Digest, err = usPasswordResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, floor, err
	}
	charged, err := storeUSPasswordResult(ctx, turn, a, q, v, out.Digest)
	if err != nil || charged != charge {
		// Inherited US-07 dispatch behaviour: the policy dispatcher's equivalent
		// store site joins ErrResolveRequired with the store error
		// (us_policy_dispatch.go), and US-07's follow-up replaced only the
		// deferred-recheck joins, not this one. A deferred store denial therefore
		// reaches the caller as both classes here, but the frame is rolled back
		// by runUSPasswordDispatch with zero output, so nothing is applied or
		// disclosed. Mirror US-07 if its dispatch site changes.
		return out, floor, errors.Join(ErrResolveRequired, err)
	}
	effect, e := applyUSPasswordReplace(ctx, turn, input)
	if e != nil {
		return out, floor, e
	}
	if effect.account != v.account || effect.profile != v.profile || effect.epoch != v.resultEpoch ||
		effect.memberIncarnation != v.memberIncarnation || effect.profileLifetime != v.profileLifetime ||
		effect.memberRevision != v.memberRevision || effect.profileRevision != v.profileRevision {
		return out, floor, ErrResolveRequired
	}
	out.Value, out.ChargedBytes, out.epochAdvanced = v, charged, true
	out.OwnerCount, out.OwnerBytes = lookup.owner.count+1, lookup.owner.bytes+charged
	// 520 appends no api_event and raises no notice in this component: the
	// product's profile-inbox security notice is not a writer producer here, and
	// inventing one would be a new producer family. Nothing else is written.
	return out, floor, turn.checkLifetimes(ctx)
}

// runUSPasswordDispatch is the closed test/harness entry. It owns the final
// current disclosure and the FULL physical terminal; the caller supplies only
// the already admitted turn.
func runUSPasswordDispatch(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usPasswordInvocation) (out usPasswordDispatchResult, floor AckResult, err error) {
	// The shared dispatcher's final publication is deferred on the CALLER's
	// context: transaction.commit/rollback cancel the frame lifetime, so a
	// lifetime-based finish here would misreport a committed command as
	// canceled. The explicit release below still runs on the live frame
	// lifetime, immediately before the terminal.
	defer func() {
		v, e := finishCommand(ctx, ctx, usPasswordCompletion{out, floor}, err)
		out, floor, err = v.out, v.floor, e
	}()
	out, floor, err = executeUSPasswordInvocation(ctx, turn, a, inv)
	if err != nil {
		return withdrawUSPassword(turn, err)
	}
	// Final current disclosure before the terminal: a release-time denial
	// REPLACES the body outcome and rolls the whole prefix back. A fresh 520
	// original has already advanced accounts.epoch, which is the US-01 stamp's
	// own epoch witness, so that one case proves the exact expected post-effect
	// state instead (see verifyUSPasswordPostState).
	if out.epochAdvanced {
		if verifyErr := verifyUSPasswordPostState(ctx, turn, inv.input, out.Value); verifyErr != nil {
			return withdrawUSPassword(turn, verifyErr)
		}
	} else if denial := a.recheck(ctx, turn); denial != nil {
		return withdrawUSPassword(turn, denial)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUSPassword(turn, ctxErr)
	}
	// The release rule runs BEFORE the terminal: the transaction cancels its
	// own lifetime at commit/rollback, so this release uses the live frame
	// lifetime. The deferred publication at function exit rechecks the CALLER's
	// context after physical return, exactly like the shared dispatcher.
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), usPasswordCompletion{out, floor}, nil)
	if releaseErr != nil {
		return withdrawUSPassword(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUSPassword(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		// finishCommand's release rule: no error, including a late cleanup
		// failure or a committed safety overrun, may carry output. Retry must
		// resolve the ORIGINAL.
		if commitErr != nil {
			return usPasswordDispatchResult{}, AckResult{}, commitErr
		}
		if inv.ack {
			return usPasswordDispatchResult{}, floor, nil
		}
		if out.historical {
			return out, floor, nil
		}
		// Only a submission that actually composed effects is Applied. A bare
		// Resolve of a never-applied command stays Pending.
		if inv.input != nil && out.Outcome.State == Pending {
			out.Outcome.State = Applied
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return usPasswordDispatchResult{}, AckResult{}, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return usPasswordDispatchResult{}, AckResult{}, commitErr
	}
}

// withdrawUSPassword rolls back and returns the zero closed result: no partial
// output, receipt or source effect survives any failure.
func withdrawUSPassword(turn *commandTurn, err error) (usPasswordDispatchResult, AckResult, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return usPasswordDispatchResult{}, AckResult{}, err
}
