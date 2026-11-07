package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
)

type ipEditBulkFacts struct {
	revision, last, ack, processed, failed, state              int64
	leaseOwner, leaseGeneration, leaseUntilMS                  int64
	incarnation                                                []byte
	expected, captured, complete, expectedBytes, capturedBytes sql.NullInt64
	manifest, chain                                            []byte
}

// Capture commits immutable original input only. It never mutates a target or
// rereads an uncaptured remainder. Native prefix insertion+chain/counters+receipt
// commit atomically. The outer dispatcher supplies current scope, trusted time,
// physical admission and COMMIT proof. It is not a production Applied entry.
func applyIPEditBulkCapture(ctx context.Context, t *commandTurn, c ipEditBulkCommand, appliedMS int64) (out ipEditBulkNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	q := c.request
	r := q.Root
	i := q.Identity
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != q.Action.command() || appliedMS < 0 || (q.Action != ipEditBulkBegin && q.Action != ipEditBulkCapture && q.Action != ipEditBulkSeal) {
		return out, ErrInvalid
	}
	checked, e := newIPEditBulkCommand(q)
	if e != nil || checked.digest != c.digest || checked.seed != c.seed || checked.fieldBytes != c.fieldBytes {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	f, out, e := resolveIPEditBulk(ctx, t, c)
	if e != nil || out.decision != receiptFresh {
		return out, e
	}
	decision := out.decision
	if decision != receiptFresh || f.revision != q.Revision || f.processed != 0 || f.failed != 0 || f.state != 1 {
		return ipEditBulkNative{}, ErrConflict
	}
	if err = ipTextExpectOne(ctx, t, ipBulkRoot, r.Operation, r.Kind, r.RootEntity, r.Library, r.Actor, r.BaseRevision); err != nil {
		return ipEditBulkNative{}, err
	}
	next := ipEditBulkResult{Revision: q.Revision + 1, AppliedMS: appliedMS, Manifest: r.Manifest}
	if q.Action == ipEditBulkBegin {
		if f.expected.Valid || f.captured.Valid || f.complete.Valid || f.expectedBytes.Valid || f.capturedBytes.Valid || len(f.manifest) != 0 || len(f.chain) != 0 {
			return ipEditBulkNative{}, ErrConflict
		}
		if err = ipTextExpectOne(ctx, t, ipBulkInitialize, r.ExpectedCount, r.Manifest[:], r.ExpectedBytes, c.fieldBytes, c.seed[:], i.Sequence, r.Operation, q.Revision, i.Incarnation[:], f.last, f.ack); err != nil {
			return ipEditBulkNative{}, err
		}
		for _, field := range r.Fields {
			if err = ipTextExecOne(ctx, t, ipBulkFieldInsert, ipEditBulkFieldArgs(r.Operation, field)...); err != nil {
				return ipEditBulkNative{}, err
			}
			if err = ipTextExpectOne(ctx, t, ipBulkFieldMatch, ipEditBulkFieldArgs(r.Operation, field)...); err != nil {
				return ipEditBulkNative{}, err
			}
		}
	} else {
		if !f.expected.Valid || !f.captured.Valid || !f.complete.Valid || !f.expectedBytes.Valid || !f.capturedBytes.Valid || f.expected.Int64 != r.ExpectedCount || f.expectedBytes.Int64 != r.ExpectedBytes || !bytes.Equal(f.manifest, r.Manifest[:]) || len(f.chain) != 32 || f.complete.Int64 != 0 || f.captured.Int64 != q.AfterOrdinal || f.capturedBytes.Int64 != c.fieldBytes+40*f.captured.Int64 {
			return ipEditBulkNative{}, ErrConflict
		}
		for _, field := range r.Fields {
			if err = ipTextExpectOne(ctx, t, ipBulkFieldMatch, ipEditBulkFieldArgs(r.Operation, field)...); err != nil {
				return ipEditBulkNative{}, err
			}
		}
		next.Captured = f.captured.Int64
		next.AfterOrdinal = next.Captured
		if q.Action == ipEditBulkCapture {
			var chain [32]byte
			copy(chain[:], f.chain)
			for _, target := range q.Targets {
				if err = ipTextExecOne(ctx, t, ipBulkTargetInsert, r.Operation, target.Ordinal, target.EntityID, target.Incarnation[:], target.Revision); err != nil {
					return ipEditBulkNative{}, err
				}
				if err = ipTextExpectOne(ctx, t, ipBulkTargetMatch, r.Operation, target.Ordinal, target.EntityID, target.Incarnation[:], target.Revision); err != nil {
					return ipEditBulkNative{}, err
				}
				chain = ipEditBulkChain(chain, target)
			}
			next.Captured += int64(len(q.Targets))
			next.AfterOrdinal = next.Captured
			if err = ipTextExpectOne(ctx, t, ipBulkAdvance, next.Captured, c.fieldBytes+40*next.Captured, chain[:], i.Sequence, r.Operation, i.Incarnation[:], q.Revision, f.last, f.ack, f.captured.Int64, f.capturedBytes.Int64, f.chain); err != nil {
				return ipEditBulkNative{}, err
			}
		} else {
			if f.captured.Int64 != r.ExpectedCount || f.capturedBytes.Int64 != r.ExpectedBytes || !bytes.Equal(f.chain, r.Manifest[:]) {
				return ipEditBulkNative{}, ErrConflict
			}
			if err = ipTextExpectOne(ctx, t, ipBulkSeal, i.Sequence, r.Operation, i.Incarnation[:], q.Revision, f.last, f.ack); err != nil {
				return ipEditBulkNative{}, err
			}
			next.Complete = true
		}
	}
	out.value = next
	out.digest, err = canonicalResultDigest(i, c.digest, ipEditBulkSavedResult{q.Action, next})
	if err != nil {
		return ipEditBulkNative{}, err
	}
	if err = ipTextExecOne(ctx, t, ipBulkSave, r.Operation, i.Incarnation[:], i.Sequence, i.OperationID[:], c.digest[:], appliedMS, out.digest[:], q.Action.text(), next.Revision, next.AfterOrdinal, next.Captured, next.Processed, next.Failed, ipTextBool(next.Complete), r.Manifest[:]); err != nil {
		return ipEditBulkNative{}, err
	}
	return out, t.checkLifetimes(ctx)
}
func ipEditBulkFieldArgs(owner string, f ipEditBulkField) []any {
	var value any
	if f.Edit.Action == fieldmeta.SetText {
		value = f.Edit.Value
	}
	return []any{owner, string(f.Field), ipEditBulkFieldAction(f.Edit), value, int64(f.Edit.Lock)}
}

func resolveIPEditBulk(ctx context.Context, t *commandTurn, c ipEditBulkCommand) (ipEditBulkFacts, ipEditBulkNative, error) {
	q := c.request
	r := q.Root
	i := q.Identity
	var out ipEditBulkNative
	var f ipEditBulkFacts
	rows, e := ipTextRows(ctx, t, ipBulkOwner, r.Operation)
	if e != nil {
		return f, out, e
	}
	e = ipTextOne(rows, func() error {
		return rows.Scan(&f.incarnation, &f.last, &f.ack, &f.revision, &f.expected, &f.captured, &f.complete, &f.expectedBytes, &f.capturedBytes, &f.manifest, &f.chain, &f.processed, &f.failed, &f.state, &f.leaseOwner, &f.leaseGeneration, &f.leaseUntilMS)
	})
	if errors.Is(e, sql.ErrNoRows) {
		return f, out, ErrOwnerUnavailable
	}
	if e != nil {
		return f, out, e
	}
	if len(f.incarnation) != 16 {
		return f, out, ErrResolveRequired
	}
	owner := receiptOwnerFacts{exists: true, lastSequence: f.last, acknowledgedSequence: f.ack}
	copy(owner.incarnation[:], f.incarnation)
	var found receiptFacts
	var operation, request, result, manifest []byte
	var variant, complete, algorithm int64
	var saved ipEditBulkResult
	rows, e = ipTextRows(ctx, t, ipBulkReceipt, r.Operation, i.Incarnation[:], i.Sequence)
	if e != nil {
		return f, out, e
	}
	e = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &variant, &saved.Revision, &saved.AfterOrdinal, &saved.Captured, &saved.Processed, &saved.Failed, &complete, &manifest, &saved.AppliedMS, &algorithm)
	})
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return f, out, e
	}
	if e == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 || len(manifest) != 32 || variant < 1 || variant > 5 || complete < 0 || complete > 1 || algorithm != 1 {
			return f, out, ErrResolveRequired
		}
		found.exists = true
		found.kind = ipEditBulkAction(variant).command()
		found.resultCount = 1
		found.identity.command = i
		copy(found.identity.command.OperationID[:], operation)
		copy(found.identity.digest[:], request)
		copy(found.resultDigest[:], result)
		copy(saved.Manifest[:], manifest)
		saved.Complete = complete == 1
	}
	decision, e := classifyReceipt(receiptIdentity{i, c.digest}, q.Action.command(), owner, found)
	if e != nil {
		return f, out, e
	}
	out.decision = decision
	if decision == receiptRetired {
		return f, out, nil
	}
	if decision == receiptReplay {
		if variant != int64(q.Action) {
			return f, ipEditBulkNative{}, ErrConflict
		}
		out.digest, e = canonicalResultDigest(i, c.digest, ipEditBulkSavedResult{q.Action, saved})
		if e != nil || [32]byte(out.digest) != found.resultDigest {
			return f, ipEditBulkNative{}, ErrResolveRequired
		}
		out.value = saved
		return f, out, nil
	}

	return f, out, nil
}
