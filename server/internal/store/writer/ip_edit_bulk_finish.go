package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// Descriptive bulk finish 245 records the truthful terminal. It is accepted
// only when the durable cursor has consumed every captured target and no raw
// target remains; its saved result proves the exact applied/stale split. It
// never re-applies or reclassifies a target and never widens a stale failure
// into success.
type ipEditBulkFinishRequest struct {
	Kind         CommandID
	Root         ipEditBulkRoot
	Revision     int64
	AfterOrdinal int64
}

type ipEditBulkFinishCommand struct {
	identity CommandIdentity
	request  ipEditBulkFinishRequest
	digest   RequestDigest
	valid    bool
}

type ipEditBulkFinishResult struct {
	OwnerID                          string
	Revision, AfterOrdinal, Captured int64
	Processed, Failed, AppliedMS     int64
	Complete                         bool
	Manifest                         [32]byte
}

type ipEditBulkFinishSavedResult struct {
	Kind  CommandID
	Value ipEditBulkFinishResult
}

type ipEditBulkFinishApplied struct {
	value  ipEditBulkFinishResult
	digest ResultDigest
}

type ipEditBulkFinishCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEditBulkFinishResult
	Digest  ResultDigest
}

type ipEditBulkFinishLookup struct {
	decision receiptDecision
	owner    ipEditBulkApplyOwner
	applied  ipEditBulkFinishApplied
}

func (c *ipEditBulkFinishCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipEditBulkFinishCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEditBulkFinishCommand")
}

func (ipEditBulkFinishCommandResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEditBulkFinishCommandResult")
}

// newIPEditBulkFinishCommand freezes the complete original terminal request.
func newIPEditBulkFinishCommand(identity CommandIdentity, in ipEditBulkFinishRequest) (*ipEditBulkFinishCommand, error) {
	if !ipTextEditIdentityValid(identity) || in.Kind != IPEditBulkFinish ||
		in.Revision <= 0 || in.Revision == math.MaxInt64 || in.AfterOrdinal < 0 ||
		in.AfterOrdinal != in.Root.ExpectedCount || ipEditBulkRootShape(in.Root) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(in.Kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	in.Root = cloneIPEditBulkRoot(in.Root)
	return &ipEditBulkFinishCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func ipEditBulkFinishReadOwner(ctx context.Context, turn *commandTurn, operation string) (out ipEditBulkApplyOwner, found bool, err error) {
	rows, err := ipBulkRows(ctx, turn, ipBulkFinishOwner, operation)
	if err != nil {
		return out, false, err
	}
	err = ipBulkOne(rows, func() error {
		return rows.Scan(&out.incarnation, &out.revision, &out.last, &out.ack, &out.after,
			&out.processed, &out.failed, &out.status, &out.intent, &out.kind, &out.entity,
			&out.library, &out.actor, &out.base, &out.expected, &out.captured, &out.complete,
			&out.algorithm, &out.manifest, &out.expectedBytes, &out.capturedBy,
			&out.capturedDigest)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if len(out.incarnation) != 16 || out.revision <= 0 || out.last < 0 || out.ack < 0 || out.ack > out.last ||
		out.last-out.ack > 1 || !out.expected.Valid || !out.captured.Valid || !out.complete.Valid ||
		!out.expectedBytes.Valid || !out.capturedBy.Valid || !out.algorithm.Valid ||
		len(out.manifest) != 32 || len(out.capturedDigest) != 32 ||
		out.processed < 0 || out.failed < 0 || out.processed+out.failed > out.expected.Int64 ||
		out.intent != ipEditBulkIntent || out.algorithm.String != ipEditBulkManifest ||
		out.complete.Int64 != 1 || out.entity <= 0 || out.kind == "" || out.library == "" ||
		out.actor == "" || out.base == "" || out.status == "" {
		return out, false, ErrResolveRequired
	}
	return out, true, nil
}

func ipEditBulkFinishLookupReceipt(ctx context.Context, turn *commandTurn, c *ipEditBulkFinishCommand) (out ipEditBulkFinishLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEditBulkFinish || c == nil || !c.valid {
		return out, ErrInvalid
	}
	q := c.request
	f, found, err := ipEditBulkFinishReadOwner(ctx, turn, q.Root.Operation)
	if err != nil {
		return out, err
	}
	if !found {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if !ipEditBulkApplyOwnerMatches(q.Root, f) {
		return out, ErrConflict
	}
	out.owner = f
	var operation, request, result []byte
	var variant int64
	var receipt receiptFacts
	rows, e := ipBulkRows(ctx, turn, ipBulkFinishReceiptHead, q.Root.Operation, c.identity.Incarnation[:], c.identity.Sequence)
	if e != nil {
		return out, e
	}
	e = ipBulkOne(rows, func() error { return rows.Scan(&operation, &request, &result, &variant) })
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 || variant != 1 {
			return out, ErrResolveRequired
		}
		receipt.exists = true
		receipt.kind = IPEditBulkFinish
		receipt.resultCount = 1
		receipt.identity.command = c.identity
		copy(receipt.identity.command.OperationID[:], operation)
		copy(receipt.identity.digest[:], request)
		copy(receipt.resultDigest[:], result)
	}
	out.decision, err = classifyReceipt(receiptIdentity{c.identity, c.digest}, IPEditBulkFinish, f.ownerFacts(), receipt)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		out.applied, err = ipEditBulkFinishReadSaved(ctx, turn, c)
		if err != nil {
			return out, err
		}
		if out.applied.digest != ResultDigest(receipt.resultDigest) {
			return out, ErrResolveRequired
		}
	}
	return out, nil
}

func ipEditBulkFinishReadSaved(ctx context.Context, turn *commandTurn, c *ipEditBulkFinishCommand) (out ipEditBulkFinishApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil || !c.valid {
		return out, ErrInvalid
	}
	r := c.request.Root
	f, found, err := ipEditBulkFinishReadOwner(ctx, turn, r.Operation)
	if err != nil {
		return out, err
	}
	if !found || !ipEditBulkApplyOwnerMatches(r, f) {
		return out, ErrResolveRequired
	}
	rows, err := ipBulkRows(ctx, turn, ipBulkFinishReceipt, r.Operation, c.identity.Incarnation[:], c.identity.Sequence)
	if err != nil {
		return out, err
	}
	var operation, request, result, manifest []byte
	var variant, accepted, after, captured, processed, failed, complete, applied int64
	var algorithm string
	if err = ipBulkOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &variant, &accepted, &after, &captured,
			&processed, &failed, &complete, &algorithm, &manifest, &applied)
	}); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if len(operation) != 16 || len(request) != 32 || len(result) != 32 || variant != 1 ||
		accepted <= 0 || captured != f.expected.Int64 || after != captured || processed < 0 || failed < 0 ||
		processed+failed != captured || complete != 1 || algorithm != ipEditBulkManifest ||
		len(manifest) != 32 || applied < 0 || !bytes.Equal(manifest, r.Manifest[:]) {
		return out, ErrResolveRequired
	}
	if !bytes.Equal(operation, c.identity.OperationID[:]) || !bytes.Equal(request, c.digest[:]) {
		return out, ErrResolveRequired
	}
	value := ipEditBulkFinishResult{OwnerID: r.Operation, Revision: accepted, AfterOrdinal: after,
		Captured: captured, Processed: processed, Failed: failed, Complete: true, AppliedMS: applied}
	copy(value.Manifest[:], manifest)
	digest, err := ipEditBulkFinishResultDigest(c.identity, c.digest, value)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	var saved ResultDigest
	copy(saved[:], result)
	if saved != digest {
		return out, ErrResolveRequired
	}
	out.value, out.digest = value, digest
	return out, nil
}

// ipEditBulkFinishTargets proves the terminal only over an exact cohort whose
// every member was actually applied or explicitly marked stale.
func ipEditBulkFinishTargets(ctx context.Context, turn *commandTurn, operation string, expected, processed, failed int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := ipBulkRows(ctx, turn, ipBulkFinishTargets, operation)
	if err != nil {
		return err
	}
	var count, applied, stale, remaining int64
	if err = ipBulkOne(rows, func() error {
		return rows.Scan(&count, &applied, &stale, &remaining)
	}); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	if count != expected || applied != processed || stale != failed || remaining != 0 ||
		applied+stale != count {
		return ErrResolveRequired
	}
	return nil
}

func ipEditBulkFinishNoNext(ctx context.Context, turn *commandTurn, operation string, after int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := ipBulkRows(ctx, turn, ipBulkFinishRaw, operation, after)
	if err != nil {
		return err
	}
	var one int64
	err = ipBulkOne(rows, func() error { return rows.Scan(&one) })
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

// applyIPEditBulkFinish is the closed terminal body. Current authority precedes
// historical receipt disclosure; a saved terminal returns its exact original
// result with zero new effects.
func applyIPEditBulkFinish(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipEntryBudget, c *ipEditBulkFinishCommand) (out ipEditBulkFinishCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEditBulkFinishCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEditBulkFinish {
		return zero, ErrInvalid
	}
	q := c.request
	actual, err := canonicalRequestDigest(IPEditBulkFinish, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPEntryOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipEditBulkFinishLookupReceipt(ctx, turn, c)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.applied.value, lookup.applied.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.recheck(ctx, turn); err != nil {
		return zero, err
	}
	f := lookup.owner
	cursor, err := ipEditBulkCursor(f.after)
	if err != nil {
		return zero, err
	}
	if f.revision != q.Revision || f.revision == math.MaxInt64 {
		return zero, ErrConflict
	}
	if cursor != q.AfterOrdinal || f.processed+f.failed != q.Root.ExpectedCount ||
		f.expected.Int64 != q.Root.ExpectedCount {
		return zero, ErrConflict
	}
	if f.status != "ready" && f.status != "running" {
		return zero, ErrConflict
	}
	if err = ipEditBulkFinishNoNext(ctx, turn, q.Root.Operation, q.AfterOrdinal); err != nil {
		return zero, err
	}
	if err = ipEditBulkFinishTargets(ctx, turn, q.Root.Operation, f.expected.Int64, f.processed, f.failed); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	revision := f.revision + 1
	if err = ipBulkExpectOne(ctx, turn, ipBulkFinishWrite, c.identity.Sequence, q.Root.Operation,
		c.identity.Incarnation[:], f.revision, f.last, f.ack, f.after, f.processed, f.failed); err != nil {
		return zero, err
	}
	value := ipEditBulkFinishResult{OwnerID: q.Root.Operation, Revision: revision,
		AfterOrdinal: f.expected.Int64, Captured: f.expected.Int64, Processed: f.processed,
		Failed: f.failed, Complete: true, AppliedMS: appliedMS, Manifest: q.Root.Manifest}
	digest, err := ipEditBulkFinishResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipBulkExpectOne(ctx, turn, ipBulkFinishReceiptWrite, q.Root.Operation,
		c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:],
		appliedMS, digest[:], revision, f.expected.Int64, f.expected.Int64, f.processed,
		f.failed, ipEditBulkManifest, q.Root.Manifest[:]); err != nil {
		return zero, err
	}
	saved, err := ipEditBulkFinishReadSaved(ctx, turn, c)
	if err != nil || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

func ipEditBulkFinishResultDigest(identity CommandIdentity, request RequestDigest, value ipEditBulkFinishResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipEditBulkFinishSavedResult{IPEditBulkFinish, value})
}

func preflightIPEditBulkFinishResult(v ipEditBulkFinishSavedResult) error {
	if v.Kind != IPEditBulkFinish {
		return ErrInvalid
	}
	r := v.Value
	if !ipEditBulkValidOperation(r.OwnerID) || r.Revision <= 0 || r.Captured <= 0 ||
		r.AfterOrdinal != r.Captured || r.Processed < 0 || r.Failed < 0 ||
		r.Processed+r.Failed != r.Captured || !r.Complete || r.AppliedMS < 0 ||
		r.Manifest == [32]byte{} {
		return ErrInvalid
	}
	return nil
}

func writeIPEditBulkFinishResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v ipEditBulkFinishSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_edit_bulk")
	e.text("finish")
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	r := v.Value
	e.text(r.OwnerID)
	for _, n := range []int64{r.Revision, r.AfterOrdinal, r.Captured, r.Processed, r.Failed} {
		e.number(uint64(n))
	}
	e.flag(r.Complete)
	e.bytes(r.Manifest[:])
	e.number(uint64(r.AppliedMS))
	return e.err
}

func ipEditBulkFinishResultEqual(a, b ipEditBulkFinishResult) bool { return a == b }
