package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"
)

// SecurityValue is the COMPLETE immutable permanent scalar. The original
// identity and request digest are encoded separately in the common envelope.
// No current issuer/target role, mutable family head or clock reconstructs it.
type SecurityValue struct {
	FamilyID, CommandKind                            string
	ResultVersion, AppliedMS                         int64
	IssuerAccountID, IssuerProfileID, IssuerFamilyID string
	IssuerMemberIncarnation, IssuerProfileLifetime   string
	Reason, RevokedAt                                string
	ResultCount                                      int64
}

// Fixed bytes outside the nine LP text payloads: shared header, version,
// purpose/variant, identity/request, nine LP lengths, and three scalar integers.
const wcSecurityResultFixedBytes = len(canonicalResultCodec) + 2 + 8 + len("security_accept") + 8 + len("admin_revoke") + 16 + 8 + 16 + 32 + 9*8 + 3*8

func preflightSecurityValue(v SecurityValue) error {
	remaining := maxCanonicalResultBytes - wcSecurityResultFixedBytes
	for _, s := range [...]string{v.FamilyID, v.CommandKind, v.IssuerAccountID, v.IssuerProfileID, v.IssuerFamilyID, v.IssuerMemberIncarnation, v.IssuerProfileLifetime, v.Reason, v.RevokedAt} {
		if len(s) > remaining {
			return ErrResolveRequired
		}
		remaining -= len(s)
	}
	for _, s := range [...]string{v.FamilyID, v.IssuerAccountID, v.IssuerProfileID, v.IssuerFamilyID, v.IssuerMemberIncarnation, v.IssuerProfileLifetime, v.RevokedAt} {
		if s == "" || !utf8.ValidString(s) {
			return ErrResolveRequired
		}
	}
	if v.CommandKind != "admin_revoke" || v.ResultVersion != 1 || v.AppliedMS < 0 || v.ResultCount != 1 ||
		v.Reason != "admin_revoke" || v.IssuerFamilyID == v.FamilyID || len(v.IssuerMemberIncarnation) != 32 || len(v.IssuerProfileLifetime) != 32 ||
		v.RevokedAt != time.UnixMilli(v.AppliedMS).UTC().Format(time.RFC3339) {
		return ErrResolveRequired
	}
	return nil
}

// The parent adds only the SecurityValue cases to preflightCanonicalResult and
// writeCanonicalResult. This is a closed result schema over shared primitives,
// never reflection, a pluggable codec, or a second canonical-digest path.
func writeSecurityValue(e *requestEncoder, id CommandIdentity, request RequestDigest, v SecurityValue) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("security_accept")
	e.text("admin_revoke")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.FamilyID)
	e.text(v.CommandKind)
	e.number(uint64(v.ResultVersion))
	e.number(uint64(v.AppliedMS))
	e.text(v.IssuerAccountID)
	e.text(v.IssuerProfileID)
	e.text(v.IssuerFamilyID)
	e.text(v.IssuerMemberIncarnation)
	e.text(v.IssuerProfileLifetime)
	e.text(v.Reason)
	e.text(v.RevokedAt)
	e.number(uint64(v.ResultCount))
	return e.err
}

func readWCSecurityOriginal(ctx context.Context, turn *commandTurn, in wcSecurityReceiptLookup) (out wcSecurityReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out = in
	q := in.request
	i := q.Receipt.Identity
	row, err := wc008DraftQuery(ctx, turn, wcSecurityNativeResult, q.FamilyID, in.snapshot.server, i.Incarnation[:], i.Sequence)
	if err != nil {
		return out, err
	}
	v := SecurityValue{FamilyID: q.FamilyID}
	var operation, request, digest []byte
	err = row.Scan(&operation, &request, &v.CommandKind, &v.ResultVersion, &v.IssuerAccountID, &v.IssuerProfileID, &v.IssuerMemberIncarnation, &v.IssuerProfileLifetime, &v.IssuerFamilyID, &v.Reason, &v.RevokedAt, &v.AppliedMS, &v.ResultCount, &digest)
	found := receiptFacts{}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		if len(operation) != 16 || len(request) != 32 || len(digest) != 32 {
			return out, ErrResolveRequired
		}
		actual := receiptIdentity{command: i}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		copy(out.digest[:], digest)
		found = receiptFacts{exists: true, identity: actual, resultCount: v.ResultCount, resultDigest: out.digest}
		if v.CommandKind == "admin_revoke" {
			found.kind = SecurityAccept
		}
	}
	out.decision, err = classifyReceipt(receiptIdentity{i, q.Receipt.RequestDigest}, SecurityAccept, in.owner, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		computed, e := canonicalResultDigest(i, q.Receipt.RequestDigest, v)
		if e != nil || computed != out.digest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.value = v
	}
	return out, nil
}

// Floor-only ACK, refund zero. The permanent scalar, native denial, teardown
// debt and original issuer witnesses remain intact. No source deletion/reclaim.
// The parent rechecks CURRENT disclosure before this SAME-turn original lookup.
func acknowledgeWCSecurity(ctx context.Context, turn *commandTurn, q SecurityReceiptRequest, lookup wcSecurityReceiptLookup) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SecurityAccept || !validWCSecurityRequest(q) || lookup.turn != turn || lookup.request != q {
		return 0, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	o, i := lookup.owner, q.Receipt.Identity
	if !o.exists || o.incarnation != i.Incarnation || o.lastSequence < 0 || o.acknowledgedSequence < 0 || o.acknowledgedSequence > o.lastSequence || o.lastSequence-o.acknowledgedSequence > 1 {
		return 0, ErrResolveRequired
	}
	if lookup.decision == receiptRetired && i.Sequence <= o.acknowledgedSequence {
		return o.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || i.Sequence != o.lastSequence || i.Sequence <= o.acknowledgedSequence || lookup.value.FamilyID != q.FamilyID {
		return 0, ErrResolveRequired
	}
	// Validate the complete original again before the mutation; a lookup from
	// another request/turn cannot lend its result or floor to this command.
	digest, err := canonicalResultDigest(i, q.Receipt.RequestDigest, lookup.value)
	if err != nil || digest != lookup.digest {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	floor, err = wc008DraftInteger(ctx, turn, wcSecurityNativeAck, i.Sequence, q.FamilyID, lookup.snapshot.server, i.Incarnation[:], o.lastSequence, o.acknowledgedSequence, i.Sequence, i.OperationID[:], q.Receipt.RequestDigest[:], digest[:])
	if err != nil {
		return 0, err
	}
	if floor != i.Sequence {
		return 0, ErrResolveRequired
	}
	return floor, nil
}
