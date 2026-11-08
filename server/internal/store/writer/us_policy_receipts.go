package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private receipt adapter for the two policy primitives (540/541). It consumes
// US-01's current-source authority stamp and W's classifier; it mints no
// authority, registers no accepting command and reconstructs no original from
// live target source. Replay reads ONLY the retained header plus its ordered
// cohort. US-07 owns the dispatch/composition that calls these functions.
type usPolicyReceiptRequest struct {
	ownerID   string
	kind      CommandID
	reference ReceiptReference
}

type usPolicyOwnerFacts struct {
	receiptOwnerFacts
	reclaimed, count, bytes, retiredMS int64
}

type usPolicyReceiptLookup struct {
	owner    usPolicyOwnerFacts
	decision receiptDecision
	result   usPolicySavedResult
	digest   ResultDigest
}

func usPolicyReceiptKind(kind CommandID) error {
	if _, err := usPolicyCodec(kind); err != nil {
		return err
	}
	return nil
}

func usPolicyIssuerFacts(a *usOwnerAuthorization) usPolicyIssuer {
	if a == nil {
		return usPolicyIssuer{}
	}
	return a.proof.witness.issuer
}

// Small fixed-width binding header, before floor/result decoding. The three
// BLOB widths are exact CHECK invariants, not declared maximum TEXT lengths.
func readUSPolicyBinding(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest) (out receiptFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usPolicyRow(ctx, turn, usPolicyReceiptBinding, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
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
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || kind < 0 || kind > math.MaxUint16 {
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
// with. A different current issuer cannot read or advance someone else's
// retained cohort merely because it can name the owner ID.
func readUSPolicyOwner(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usPolicyOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	i := usPolicyIssuerFacts(a)
	row, err := usPolicyRow(ctx, turn, usSecurityOwner, ownerID, i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.lastSequence, &out.acknowledgedSequence, &out.reclaimed, &out.count, &out.bytes, &out.retiredMS); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(inc) != 16 || out.lastSequence < 0 || out.acknowledgedSequence < 0 || out.acknowledgedSequence > out.lastSequence || out.lastSequence-out.acknowledgedSequence > 1 || out.reclaimed < 0 || out.reclaimed > out.acknowledgedSequence || out.count < out.lastSequence-out.acknowledgedSequence || out.bytes < 0 || out.retiredMS < 0 || (out.count == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	out.exists = true
	copy(out.incarnation[:], inc)
	return out, nil
}

type usPolicyCustody struct {
	owner    usPolicyOwnerFacts
	decision receiptDecision
	binding  receiptFacts
}

// Shared fixed-width custody only. Each concrete purpose owns its own native
// result codec/decoder. Namespace reservation grants no result-schema authority.
func lookupUSPolicyCustody(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest) (out usPolicyCustody, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.recheck(ctx, turn)))
	}()
	// An earlier prefix in the same frame may have advanced a source row.
	// Recheck native current authority before any original owner or payload read.
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	if !usIdentifier(q.ownerID) || usPolicyReceiptKind(q.kind) != nil || q.kind != turn.transaction.descriptor.ID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) {
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

// Current stamped authority MUST precede every native owner/receipt read. The
// shared classifier sees only the ORIGINAL identity; fresh target CAS is absent,
// so replay never touches current policy source.
func lookupUSPolicy(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest) (out usPolicyReceiptLookup, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.recheck(ctx, turn)))
	}()
	if err = usPolicyReceiptKind(q.kind); err != nil {
		return out, err
	}
	custody, e := lookupUSPolicyCustody(ctx, turn, a, q)
	if e != nil {
		return out, e
	}
	out.owner, out.decision = custody.owner, custody.decision
	if out.decision != receiptReplay {
		return out, nil
	}
	binding := custody.binding
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if binding.resultCount < 0 || binding.resultCount > usMaxPolicyLibraries {
		return out, ErrResolveRequired
	}
	var bytes int64
	row, err := usPolicyRow(ctx, turn, usPolicyReceiptOctets, q.ownerID, expected.command.Incarnation[:], expected.command.Sequence)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = a.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	// Header count is bounded before allocating its numeric preflight vector.
	lengths, err := readUSPolicyLibraryOctets(ctx, turn, q, binding.resultCount)
	if err != nil {
		return out, err
	}
	for _, n := range lengths {
		if n < 0 || n > maxCanonicalResultBytes-bytes {
			return out, &Refusal{Code: RefusalCapacity}
		}
		bytes += n
	}
	if err = a.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	out.result, err = readUSPolicyHeader(ctx, turn, q, binding.resultCount)
	if err != nil {
		return out, err
	}
	libraries, err := readUSPolicyLibraries(ctx, turn, q, lengths)
	if err != nil {
		return out, err
	}
	// Only profile inherit saves SQL NULL. A member result is always the
	// explicit list (DDL forces policy_inherits=0 for 540), so an empty saved
	// cohort must decode as non-nil `[]` exactly as it was returned live.
	if out.result.inherits {
		libraries = nil
	}
	out.result.libraries = libraries
	out.digest, err = usPolicyResultDigest(expected.command, expected.digest, out.result)
	if err != nil || out.digest != ResultDigest(binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return out, turn.checkLifetimes(ctx)
}

func readUSPolicyHeader(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest, count int64) (out usPolicySavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usPolicyRow(ctx, turn, usPolicyReceiptHeader, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, version, actualCount, bytes int64
	var codec string
	var profile, lifetime sql.NullString
	var inherits, changed int64
	out.ownerID = q.ownerID
	out.kind = q.kind
	if err = row.Scan(&op, &request, &kind, &version, &codec, &out.appliedMS, &digest, &out.account, &out.memberIncarnation, &profile, &lifetime, &out.revision, &inherits, &changed, &actualCount, &bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	wantCodec, _ := usPolicyCodec(q.kind)
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || !equalBytes(op, q.reference.Identity.OperationID[:]) || !equalBytes(request, q.reference.RequestDigest[:]) || kind != int64(q.kind) || version != 1 || codec != wantCodec || actualCount != count || (inherits != 0 && inherits != 1) || (changed != 0 && changed != 1) || (profile.Valid != lifetime.Valid) || (q.kind == USProfileLibraries) != profile.Valid {
		return out, ErrResolveRequired
	}
	out.profile, out.profileLifetime = profile.String, lifetime.String
	out.inherits, out.changed = inherits == 1, changed == 1
	return out, nil
}

func readUSPolicyLibraryOctets(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest, count int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := usPolicyRows(ctx, turn, usPolicyReceiptLibraryOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, count+1)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]int64, 0, int(count))
	for rows.Next() {
		var ordinal, n int64
		if err = rows.Scan(&ordinal, &n); err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= count || n <= 0 {
			return nil, ErrResolveRequired
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(out)) != count {
		return nil, ErrResolveRequired
	}
	return out, nil
}

func readUSPolicyLibraries(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest, lengths []int64) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := usPolicyRows(ctx, turn, usPolicyReceiptLibraries, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, int64(len(lengths)+1))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]string, 0, len(lengths))
	for rows.Next() {
		var ordinal int64
		var id string
		if err = rows.Scan(&ordinal, &id); err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= int64(len(lengths)) || int64(len(q.ownerID)+16+len("policy")+len(id)) != lengths[len(out)] {
			return nil, ErrResolveRequired
		}
		out = append(out, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(lengths) {
		return nil, ErrResolveRequired
	}
	return out, nil
}

// Native immutable save: receipt header, ordered cohort, then the exact owner
// byte/count debt in ONE frame. No event, source CAS or ACK here. US-07 owns
// those effects and the final FULL release.
func storeUSPolicyResult(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest, v usPolicySavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() {
		charged, err = finishCommand(ctx, commandLifetime(turn), charged, errors.Join(err, a.recheck(ctx, turn)))
	}()
	if turn == nil || turn.transaction == nil || q.kind != v.kind || turn.transaction.descriptor.ID != q.kind || v.ownerID != q.ownerID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) || digest == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if err = a.recheck(ctx, turn); err != nil {
		return 0, err
	}
	if err = usPolicyReceiptKind(q.kind); err != nil {
		return 0, err
	}
	// Owner facts are never caller-supplied. Re-read the owner under the
	// CURRENT issuer scope and require the original identity to classify fresh
	// in the same frame, so a stale prefix or a foreign issuer cannot write.
	custody, err := lookupUSPolicyCustody(ctx, turn, a, q)
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
	codec, _ := usPolicyCodec(q.kind)
	expectedBytes, err := usPolicyResultVariableBytes(v)
	if err != nil {
		return 0, err
	}
	if expectedBytes <= 0 {
		return 0, ErrResolveRequired
	}
	if actual, err := usPolicyResultDigest(q.reference.Identity, q.reference.RequestDigest, v); err != nil || actual != digest {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	// The SAME operation may not re-store its result at another retained
	// sequence. Detect that explicitly so a UNIQUE violation never surfaces as
	// a raw SQLite error, and never overwrite the original receipt.
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
	var profile, lifetime any
	if q.kind == USProfileLibraries {
		profile, lifetime = v.profile, v.profileLifetime
	}
	row, err := usPolicyRow(ctx, turn, usPolicyReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:], v.account, v.memberIncarnation, profile, lifetime, v.revision, usPolicyBool(v.inherits), usPolicyBool(v.changed), int64(len(v.libraries)))
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&charged); err != nil {
		return 0, err
	}
	if charged <= 0 || charged > expectedBytes {
		return 0, ErrResolveRequired
	}
	for ordinal, id := range v.libraries {
		want := int64(len(q.ownerID) + 16 + len("policy") + len(id))
		row, err = usPolicyRow(ctx, turn, usPolicyReceiptLibraryInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, "policy", int64(ordinal), id)
		if err != nil {
			return 0, err
		}
		var n int64
		if err = row.Scan(&n); err != nil {
			return 0, err
		}
		if n <= 0 || n != want || n > math.MaxInt64-charged {
			return 0, ErrResolveRequired
		}
		charged += n
	}
	row, err = usPolicyRow(ctx, turn, usPolicyOwnerAdvance, q.reference.Identity.Sequence, charged, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+charged || charged != expectedBytes {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}

// Floor-only ACK: it advances one durable sequence and refunds/deletes nothing.
// It cannot prove or return the original result; retrieval stays a separate
// replay classification before and after the floor.
func acknowledgeUSPolicyAfterAuthority(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usPolicyReceiptRequest) (ack int64, err error) {
	defer func() {
		ack, err = finishCommand(ctx, commandLifetime(turn), ack, errors.Join(err, a.recheck(ctx, turn)))
	}()
	lookup, err := lookupUSPolicy(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.reference.Identity.Sequence != lookup.owner.lastSequence {
		return 0, ErrConflict
	}
	row, err := usPolicyRow(ctx, turn, usPolicyOwnerAck, q.reference.Identity.Sequence, q.ownerID, q.reference.Identity.Incarnation[:], lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
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

func usPolicyBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func usPolicyRow(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if turn == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return commandRow{}, invalid("us_policy_statement")
	}
	return turn.queryRow(ctx, step.statementStep, args...)
}

func usPolicyRows(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	if turn == nil {
		return nil, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return nil, invalid("us_policy_statement")
	}
	return turn.query(ctx, step.statementStep, args...)
}
