package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Same-turn private facts, not disclosure or Applied authority. The result keeps
// ZERO Outcome; W alone publishes it after its current-authority/terminal work.
type basicReceiptLookup struct {
	owner    receiptOwnerFacts
	decision receiptDecision
	result   BasicResult
	facts    receiptFacts
	turn     *commandTurn
	request  BasicReceiptRequest
}

// W must establish CURRENT ScanDisclosure and native scope before this call.
// ORIGINAL identity/op/digest and the retained floor precede any body decoding.
// History never reads CJR/assets or gates on fresh source/root/lease/revision.
func lookupBasic(ctx context.Context, turn *commandTurn, q BasicReceiptRequest) (value basicReceiptLookup, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	kind, err := basicResourceCommand(q.Purpose)
	if err != nil || ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind ||
		q.Scan.RunID == "" || q.Scan.LibraryID <= 0 || q.Scan.LibraryKey == "" || q.Scan.Receipt.Identity.Sequence <= 0 {
		return value, ErrInvalid
	}
	required, err := basicReceiptRequiredBytes(q, [4]int64{})
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, q.Purpose, required); err != nil {
		return value, err
	}
	if !utf8.ValidString(q.Scan.RunID) || !utf8.ValidString(q.Scan.LibraryKey) {
		return value, ErrInvalid
	}
	// Only the fixed identity points are prerequisites for fresh/retired;
	// retained Accept must not need its deliberately unimplemented codec.
	for _, id := range []stmt.ID{scanReceiptOwner, basicReceiptScope, basicReceiptIdentity} {
		if _, ok := turn.steps[id]; !ok {
			return value, invalid("basic_receipt_identity_steps")
		}
	}
	var owner receiptOwnerFacts
	var inc []byte
	row, err := turn.query(ctx, turn.steps[scanReceiptOwner].statementStep, q.Scan.RunID)
	if err != nil {
		return value, err
	}
	err = basicReceiptPoint(row, &inc, &owner.lastSequence, &owner.acknowledgedSequence)
	expected := receiptIdentity{q.Scan.Receipt.Identity, q.Scan.Receipt.RequestDigest}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = classifyReceipt(expected, kind, owner, receiptFacts{})
		return value, err
	}
	if err != nil {
		return value, err
	}
	if len(inc) != 16 {
		return value, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], inc)
	if owner.incarnation != expected.command.Incarnation {
		_, err = classifyReceipt(expected, kind, owner, receiptFacts{})
		return value, err
	}
	row, err = turn.query(ctx, turn.steps[basicReceiptScope].statementStep, q.Scan.RunID, q.Scan.LibraryID, q.Scan.LibraryKey)
	if err != nil {
		return value, err
	}
	var runID string
	if err = basicReceiptPoint(row, &runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return value, ErrDisclosureDenied
		}
		return value, err
	}
	if runID != q.Scan.RunID {
		return value, ErrDisclosureDenied
	}
	row, err = turn.query(ctx, turn.steps[basicReceiptIdentity].statementStep, q.Scan.RunID, inc, expected.command.Sequence)
	if err != nil {
		return value, err
	}
	var facts receiptFacts
	var operation, request, digest []byte
	var purpose string
	err = basicReceiptPoint(row, &operation, &request, &purpose, &facts.resultCount, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(digest) != 32 {
			return value, ErrResolveRequired
		}
		facts.exists = true
		facts.identity.command = expected.command
		copy(facts.identity.command.OperationID[:], operation)
		copy(facts.identity.digest[:], request)
		copy(facts.resultDigest[:], digest)
		// Kind FIRST. A different native variant is a conflict, never decoded
		// as Basic or used to construct a new request/result.
		if purpose == string(q.Purpose) {
			facts.kind = kind
		}
	}
	if err != nil {
		return value, err
	}
	decision, err := classifyReceipt(expected, kind, owner, facts)
	if err != nil {
		return value, err
	}
	out := basicReceiptLookup{owner: owner, decision: decision, facts: facts, turn: turn, request: q}
	if decision != receiptReplay {
		return out, nil
	}
	if q.Purpose == BasicPurposeAccept {
		return value, ErrNotImplemented
	}
	if facts.resultCount != 0 {
		return value, ErrResolveRequired
	}
	for _, id := range []stmt.ID{basicReceiptHeaderLengths, basicReceiptHeader, scanReceiptReclaimKey} {
		if _, ok := turn.steps[id]; !ok {
			return value, invalid("basic_receipt_body_steps")
		}
	}
	row, err = turn.query(ctx, turn.steps[basicReceiptHeaderLengths].statementStep, q.Scan.RunID, inc, expected.command.Sequence)
	if err != nil {
		return value, err
	}
	var lengths [4]int64
	if err = basicReceiptPoint(row, &lengths[0], &lengths[1], &lengths[2], &lengths[3]); err != nil {
		return value, errors.Join(ErrResolveRequired, err)
	}
	if lengths[0] <= 0 || lengths[1] <= 0 || lengths[2] <= 0 || lengths[3] != 0 {
		return value, ErrResolveRequired
	}
	required, err = basicReceiptRequiredBytes(q, lengths)
	if err != nil {
		return value, err
	}
	// Recheck the SAME grant against the whole simultaneous envelope, using
	// actual native octets BEFORE any header TEXT is copied or hashed. This
	// checker neither allocates a second ledger nor reserves/refunds memory.
	if err = turn.requireBasicMemory(ctx, q.Purpose, required); err != nil {
		return value, err
	}
	row, err = turn.query(ctx, turn.steps[basicReceiptHeader].statementStep, q.Scan.RunID, inc, expected.command.Sequence)
	if err != nil {
		return value, err
	}
	v := BasicResult{Purpose: q.Purpose, RunID: q.Scan.RunID, Members: []InventoryMember{}}
	if err = basicReceiptPoint(row, &v.AcceptedRunRevision, &v.AppliedMS, &v.SourceID, &v.SourceIncarnation, &v.SourceRevision,
		&v.Continuation.NextCursor, &v.ChangeID, &v.ExpectedEpoch, &v.Continuation.NextFamily, &v.Continuation.AfterEntity); err != nil {
		return value, errors.Join(ErrResolveRequired, err)
	}
	for n, s := range [...]string{v.SourceID, v.SourceIncarnation, v.SourceRevision, v.Continuation.NextCursor} {
		if int64(len(s)) != lengths[n] {
			return value, ErrResolveRequired
		}
	}
	// FULL original key prefix, LIMIT 1: Stage/Seal's complete cohort is empty.
	// No count-only proof or truncated successful member projection is served.
	children, err := turn.query(ctx, turn.steps[scanReceiptReclaimKey].statementStep, q.Scan.RunID, inc, expected.command.Sequence)
	if err != nil {
		return value, err
	}
	hasChild := children.Next()
	err = errors.Join(children.Err(), children.Close())
	if err != nil || hasChild {
		return value, errors.Join(ErrResolveRequired, err)
	}
	v.Digest, err = basicResultDigest(expected.command, expected.digest, v)
	if err != nil || v.Digest != ResultDigest(facts.resultDigest) {
		return value, errors.Join(ErrResolveRequired, err)
	}
	out.result = v
	return out, nil
}

// Only the captured same-turn classifier/owner facts may advance the floor.
// This has no semantic producer work, reclamation, header deletion or refund.
func acknowledgeBasic(ctx context.Context, turn *commandTurn, q BasicReceiptRequest, lookup basicReceiptLookup) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	kind, err := basicResourceCommand(q.Purpose)
	if err != nil || ctx == nil || turn == nil || lookup.turn != turn || lookup.request != q {
		return 0, ErrInvalid
	}
	if err = checkBasicGrant(turn, kind); err != nil {
		return 0, err
	}
	decision, err := classifyReceipt(receiptIdentity{q.Scan.Receipt.Identity, q.Scan.Receipt.RequestDigest}, kind, lookup.owner, lookup.facts)
	if err != nil {
		return 0, err
	}
	if decision != lookup.decision {
		return 0, ErrResolveRequired
	}
	if decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if decision != receiptReplay || q.Scan.Receipt.Identity.Sequence != lookup.owner.lastSequence ||
		lookup.result.Purpose != q.Purpose || lookup.result.Digest != ResultDigest(lookup.facts.resultDigest) {
		return 0, ErrResolveRequired
	}
	step, ok := turn.steps[scanReceiptAck]
	if !ok {
		return 0, invalid("basic_receipt_ack_step")
	}
	changed, err := turn.exec(ctx, step.statementStep, q.Scan.Receipt.Identity.Sequence, q.Scan.RunID,
		q.Scan.Receipt.Identity.Incarnation[:], lookup.owner.acknowledgedSequence, lookup.owner.lastSequence)
	if err != nil {
		return 0, err
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return 0, errors.Join(ErrConflict, err)
	}
	return q.Scan.Receipt.Identity.Sequence, nil
}

// Exact zero-or-one projection, including EOF/close errors. A fault-injected
// second row must not become a silently accepted point proof.
func basicReceiptPoint(rows *commandRows, dest ...any) error {
	if rows == nil {
		return ErrInvalid
	}
	var err error
	if !rows.Next() {
		err = rows.Err()
		if err == nil {
			err = sql.ErrNoRows
		}
	} else {
		err = rows.Scan(dest...)
		if err == nil && rows.Next() {
			err = ErrResolveRequired
		}
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

// Prospective complete history peak: retained request and bound/copy values,
// native header/driver copies, saved projection, shared digest's wire scratch,
// and fixed scalar/row bookkeeping. All variable bytes are actual facts. The
// native reserve remains separately protected by requireBasicMemory.
func basicReceiptRequiredBytes(q BasicReceiptRequest, lengths [4]int64) (uint64, error) {
	requestBytes := uint64(len(q.Scan.RunID))
	if requestBytes > maxCanonicalRequestBytes || uint64(len(q.Scan.LibraryKey)) > maxCanonicalRequestBytes-requestBytes {
		return 0, ErrCap
	}
	requestBytes += uint64(len(q.Scan.LibraryKey))
	remaining := int64(maxCanonicalResultBytes - basicResultFixedBytes(q.Purpose) - len(q.Scan.RunID))
	if remaining < 0 {
		return 0, ErrCap
	}
	var nativeBytes uint64
	for _, n := range lengths {
		if n < 0 || n > remaining {
			return 0, ErrCap
		}
		remaining -= n
		nativeBytes += uint64(n)
	}
	return 8192 + 8*requestBytes + 8*nativeBytes + 4*uint64(basicResultFixedBytes(q.Purpose)), nil
}
