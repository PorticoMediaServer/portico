package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

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

func readUSPolicyOwner(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, ownerID string) (out usPolicyOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	i := a.issuer
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

// Current stamped authority MUST precede every native owner/receipt read. The
// shared classifier sees only the ORIGINAL identity; fresh target CAS is absent.
func lookupUSPolicy(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest) (out usPolicyReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	if err = a.check(turn); err != nil {
		return out, err
	}
	if !usIdentifier(q.ownerID) || q.kind != turn.transaction.descriptor.ID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = a.requireMemory(ctx, turn, 0); err != nil {
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
	if err != nil || out.decision != receiptReplay {
		return out, err
	}
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
	if err = a.requireMemory(ctx, turn, bytes); err != nil {
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
	if err = a.requireMemory(ctx, turn, bytes); err != nil {
		return out, err
	}
	out.result, err = readUSPolicyHeader(ctx, turn, q, binding.resultCount)
	if err != nil {
		return out, err
	}
	out.result.libraries, err = readUSPolicyLibraries(ctx, turn, q, lengths)
	if err != nil {
		return out, err
	}
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
	if kind != int64(q.kind) || version != 1 || codec != wantCodec || actualCount != count || (inherits != 0 && inherits != 1) || (changed != 0 && changed != 1) || (profile.Valid != lifetime.Valid) || (q.kind == USProfileLibraries) != profile.Valid {
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
