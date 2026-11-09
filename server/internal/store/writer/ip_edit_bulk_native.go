package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Descriptive bulk birth/capture. Current authority precedes historical receipt
// disclosure; a saved result replays without any fresh source/catalogue probe.
// Admission, complete original-input proof and COMMIT stay with the closed
// dispatcher; this body issues no public Applied capability and no provider
// worker authority.
type ipEditBulkOwnerFacts struct {
	incarnation                                []byte
	manifest, capturedDigest                   []byte
	last, ack, revision                        int64
	expected, captured, complete, reclaimAfter sql.NullInt64
	expectedBytes, capturedBytes               sql.NullInt64
	algorithm                                  sql.NullString
	processed, failed                          int64
	status, intent, kind, library, actor, base string
	entity                                     int64
}

type ipEditBulkLookup struct {
	decision receiptDecision
	owner    receiptOwnerFacts
	create   bool
	applied  ipEditBulkApplied
}

func (c *ipEditBulkCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipBulkRows(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	step, ok := t.steps[id]
	if !ok {
		return nil, ErrInvalid
	}
	return t.query(ctx, step.statementStep, args...)
}

func ipBulkOne(rows *commandRows, scan func() error) (err error) {
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if e := rows.Err(); e != nil {
			return e
		}
		return sql.ErrNoRows
	}
	if err = scan(); err != nil {
		return err
	}
	if rows.Next() {
		return ErrResolveRequired
	}
	return rows.Err()
}

func ipBulkExpectOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	rows, err := ipBulkRows(ctx, t, id, args...)
	if err != nil {
		return err
	}
	var n int64
	if err = ipBulkOne(rows, func() error { return rows.Scan(&n) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func ipBulkExecOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, ok := t.steps[id]
	if !ok {
		return ErrInvalid
	}
	result, err := t.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// ipEditBulkOwnerFacts reads the ONE descriptive repair owner and refuses a
// partially populated or mixed-intent descriptive header. A missing owner is a
// real birth state, never an error.
func ipEditBulkReadOwner(ctx context.Context, t *commandTurn, operation string) (f ipEditBulkOwnerFacts, found bool, err error) {
	rows, err := ipBulkRows(ctx, t, ipBulkOwner, operation)
	if err != nil {
		return f, false, err
	}
	err = ipBulkOne(rows, func() error {
		return rows.Scan(&f.incarnation, &f.last, &f.ack, &f.revision, &f.expected, &f.captured,
			&f.complete, &f.algorithm, &f.manifest, &f.expectedBytes, &f.capturedBytes,
			&f.capturedDigest, &f.reclaimAfter, &f.processed, &f.failed, &f.status, &f.intent,
			&f.kind, &f.entity, &f.library, &f.actor, &f.base)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	if len(f.incarnation) != 16 || f.last < 0 || f.ack < 0 || f.ack > f.last || f.last-f.ack > 1 ||
		f.revision <= 0 || !f.expected.Valid || !f.captured.Valid || !f.complete.Valid ||
		!f.expectedBytes.Valid || !f.capturedBytes.Valid || !f.reclaimAfter.Valid ||
		len(f.manifest) != 32 || len(f.capturedDigest) != 32 || !f.algorithm.Valid ||
		f.intent != ipEditBulkIntent || f.algorithm.String != ipEditBulkManifest ||
		f.complete.Int64 != 1 || f.entity <= 0 || f.kind == "" || f.library == "" ||
		f.actor == "" || f.base == "" || f.status == "" {
		return f, false, ErrResolveRequired
	}
	return f, true, nil
}

// ipEditBulkOwnerMatches proves the retained owner is the SAME complete header
// this request declared. Counts, bytes and the chained manifest cannot drift.
func ipEditBulkOwnerMatches(r ipEditBulkRoot, f ipEditBulkOwnerFacts) bool {
	return f.kind == r.Kind && f.entity == r.RootEntity && f.library == r.Library &&
		f.actor == r.Actor && f.base == r.BaseRevision &&
		f.expected.Int64 == r.ExpectedCount && f.captured.Int64 == r.ExpectedCount &&
		f.expectedBytes.Int64 == r.ExpectedBytes && f.capturedBytes.Int64 == r.ExpectedBytes &&
		bytes.Equal(f.manifest, r.Manifest[:]) && bytes.Equal(f.capturedDigest, r.Manifest[:])
}

func ipEditBulkLookupReceipt(ctx context.Context, turn *commandTurn, c *ipEditBulkCommand) (out ipEditBulkLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEditBulkBegin || c == nil || !c.valid {
		return out, ErrInvalid
	}
	q := c.request
	f, found, err := ipEditBulkReadOwner(ctx, turn, q.Root.Operation)
	if err != nil {
		return out, err
	}
	var operation, request, result []byte
	var variant int64
	var receipt receiptFacts
	if found {
		if !ipEditBulkOwnerMatches(q.Root, f) {
			return out, ErrConflict
		}
		out.owner = receiptOwnerFacts{exists: true, lastSequence: f.last, acknowledgedSequence: f.ack}
		copy(out.owner.incarnation[:], f.incarnation)
		rows, e := ipBulkRows(ctx, turn, ipBulkReceiptHead, q.Root.Operation, c.identity.Incarnation[:], c.identity.Sequence)
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
			receipt.kind = IPEditBulkBegin
			receipt.resultCount = 1
			receipt.identity.command = c.identity
			copy(receipt.identity.command.OperationID[:], operation)
			copy(receipt.identity.digest[:], request)
			copy(receipt.resultDigest[:], result)
		}
		out.decision, err = classifyReceipt(receiptIdentity{c.identity, c.digest}, IPEditBulkBegin, out.owner, receipt)
		if err != nil {
			return out, err
		}
		if out.decision == receiptReplay {
			out.applied, err = ipEditBulkReadSaved(ctx, turn, c)
			if err != nil {
				return out, err
			}
			if out.applied.digest != ResultDigest(receipt.resultDigest) {
				return out, ErrResolveRequired
			}
		}
		return out, nil
	}
	if receipt.exists {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if q.Revision != 0 || c.identity.Sequence != 1 {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	out.create = true
	out.owner = receiptOwnerFacts{exists: true, incarnation: c.identity.Incarnation}
	out.decision = receiptFresh
	return out, nil
}

// ipEditBulkReadSaved reconstructs the COMPLETE original result from the saved
// receipt, the exact owner header and the ordered captured children.
func ipEditBulkReadSaved(ctx context.Context, turn *commandTurn, c *ipEditBulkCommand) (out ipEditBulkApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil || !c.valid {
		return out, ErrInvalid
	}
	r := c.request.Root
	f, found, err := ipEditBulkReadOwner(ctx, turn, r.Operation)
	if err != nil {
		return out, err
	}
	if !found || !ipEditBulkOwnerMatches(r, f) {
		return out, ErrResolveRequired
	}
	rows, err := ipBulkRows(ctx, turn, ipBulkReceipt, r.Operation, c.identity.Incarnation[:], c.identity.Sequence)
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
		accepted <= 0 || after < 0 || captured != f.expected.Int64 || processed < 0 || failed < 0 ||
		processed+failed > captured || processed > captured || failed > captured-processed ||
		complete != 1 || algorithm != ipEditBulkManifest || len(manifest) != 32 || applied < 0 ||
		after != captured || !bytes.Equal(manifest, r.Manifest[:]) {
		return out, ErrResolveRequired
	}
	if !bytes.Equal(operation, c.identity.OperationID[:]) || !bytes.Equal(request, c.digest[:]) {
		return out, ErrResolveRequired
	}
	value := ipEditBulkResult{OwnerID: r.Operation, Revision: accepted, AfterOrdinal: after,
		Captured: captured, Processed: processed, Failed: failed, Complete: true, AppliedMS: applied}
	copy(value.Manifest[:], manifest)
	if err = ipEditBulkReadChildren(ctx, turn, c); err != nil {
		return out, err
	}
	digest, err := ipEditBulkResultDigest(c.identity, c.digest, value)
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

// ipEditBulkReadChildren proves the retained field/target cohort is exactly the
// ORIGINAL request: no extra ordinal, no rewritten value and no re-fetch.
func ipEditBulkReadChildren(ctx context.Context, turn *commandTurn, c *ipEditBulkCommand) error {
	r := c.request.Root
	rows, err := ipBulkRows(ctx, turn, ipBulkFieldRead, r.Operation)
	if err != nil {
		return err
	}
	fields := 0
	for rows.Next() {
		var name, action string
		var value sql.NullString
		var locked int64
		if err = rows.Scan(&name, &action, &value, &locked); err != nil {
			rows.Close()
			return err
		}
		if fields >= len(r.Fields) {
			rows.Close()
			return ErrResolveRequired
		}
		want := r.Fields[fields]
		if name != string(want.Field) || action != ipEditBulkFieldAction(want.Edit) ||
			locked != int64(want.Edit.Lock) || !ipEditBulkValueMatches(want.Edit, value) {
			rows.Close()
			return ErrResolveRequired
		}
		fields++
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if fields != len(r.Fields) {
		return ErrResolveRequired
	}
	rows, err = ipBulkRows(ctx, turn, ipBulkTargetRead, r.Operation)
	if err != nil {
		return err
	}
	targets := 0
	for rows.Next() {
		var ordinal, entity, revision int64
		var incarnation []byte
		if err = rows.Scan(&ordinal, &entity, &incarnation, &revision); err != nil {
			rows.Close()
			return err
		}
		if targets >= len(c.request.Targets) {
			rows.Close()
			return ErrResolveRequired
		}
		want := c.request.Targets[targets]
		if ordinal != want.Ordinal || entity != want.EntityID || revision != want.Revision ||
			!bytes.Equal(incarnation, want.Incarnation[:]) {
			rows.Close()
			return ErrResolveRequired
		}
		targets++
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if targets != int(r.ExpectedCount) || targets != len(c.request.Targets) {
		return ErrResolveRequired
	}
	return nil
}

func ipEditBulkValueMatches(edit fieldmeta.TextEdit, value sql.NullString) bool {
	if edit.Action == fieldmeta.SetText {
		return value.Valid && value.String == edit.Value
	}
	return !value.Valid
}

// ipEditBulkCheckEntity proves the exact current subject/incarnation/revision
// for one target before any write. A foreign, retired or stale target denies
// the whole capture.
func ipEditBulkCheckEntity(ctx context.Context, turn *commandTurn, r ipEditBulkRoot, t ipEditBulkTarget) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := ipBulkRows(ctx, turn, ipBulkEntity, t.EntityID, r.LibraryID, t.Incarnation[:], t.Revision, r.Library)
	if err != nil {
		return err
	}
	var kind int64
	if err = ipBulkOne(rows, func() error { return rows.Scan(&kind) }); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDisclosureDenied
		}
		return err
	}
	if ipEntryOwnerKind(kind) != r.Kind {
		return ErrDisclosureDenied
	}
	return nil
}

func ipEditBulkWriteFresh(ctx context.Context, turn *commandTurn, c *ipEditBulkCommand) (out ipEditBulkApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil || !c.valid {
		return out, ErrInvalid
	}
	r := c.request.Root
	for _, t := range c.request.Targets {
		if err = ipEditBulkCheckEntity(ctx, turn, r, t); err != nil {
			return out, err
		}
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return out, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return out, ErrInvalid
	}
	stamp := time.UnixMilli(appliedMS).UTC().Format(time.RFC3339Nano)
	rows, err := ipBulkRows(ctx, turn, ipBulkInsert, r.Operation, r.Kind, r.RootEntity, r.Library,
		r.Actor, r.BaseRevision, c.identity.Incarnation[:], c.identity.Sequence,
		r.ExpectedCount, r.ExpectedCount, ipEditBulkManifest, r.Manifest[:], r.ExpectedBytes,
		r.ExpectedBytes, r.Manifest[:], stamp)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	var revision int64
	if err = ipBulkOne(rows, func() error { return rows.Scan(&revision) }); err != nil || revision != 1 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	for _, f := range r.Fields {
		value := any(nil)
		if f.Edit.Action == fieldmeta.SetText {
			value = f.Edit.Value
		}
		if err = ipBulkExecOne(ctx, turn, ipBulkFieldWrite, r.Operation, string(f.Field),
			ipEditBulkFieldAction(f.Edit), value, int64(f.Edit.Lock)); err != nil {
			return out, err
		}
		if err = ipBulkExpectOne(ctx, turn, ipBulkFieldMatch, r.Operation, string(f.Field),
			ipEditBulkFieldAction(f.Edit), value, int64(f.Edit.Lock)); err != nil {
			return out, err
		}
	}
	for _, t := range c.request.Targets {
		if err = ipBulkExecOne(ctx, turn, ipBulkTargetWrite, r.Operation, t.Ordinal, t.EntityID,
			t.Incarnation[:], t.Revision); err != nil {
			return out, err
		}
		if err = ipBulkExpectOne(ctx, turn, ipBulkTargetMatch, r.Operation, t.Ordinal, t.EntityID,
			t.Incarnation[:], t.Revision); err != nil {
			return out, err
		}
	}
	var fields, targets int64
	rows, err = ipBulkRows(ctx, turn, ipBulkFieldCount, r.Operation)
	if err == nil {
		err = ipBulkOne(rows, func() error { return rows.Scan(&fields) })
	}
	if err != nil {
		return out, err
	}
	rows, err = ipBulkRows(ctx, turn, ipBulkTargetCount, r.Operation)
	if err == nil {
		err = ipBulkOne(rows, func() error { return rows.Scan(&targets) })
	}
	if err != nil {
		return out, err
	}
	if fields != int64(len(r.Fields)) || targets != r.ExpectedCount {
		return out, ErrResolveRequired
	}
	value := ipEditBulkResult{OwnerID: r.Operation, Revision: revision, AfterOrdinal: r.ExpectedCount,
		Captured: r.ExpectedCount, Complete: true, AppliedMS: appliedMS, Manifest: r.Manifest}
	digest, err := ipEditBulkResultDigest(c.identity, c.digest, value)
	if err != nil {
		return out, err
	}
	if err = ipBulkExpectOne(ctx, turn, ipBulkReceiptWrite, r.Operation, c.identity.Incarnation[:],
		c.identity.Sequence, c.identity.OperationID[:], c.digest[:], appliedMS, digest[:],
		revision, r.ExpectedCount, r.ExpectedCount, 0, 0, ipEditBulkManifest, r.Manifest[:]); err != nil {
		return out, err
	}
	saved, err := ipEditBulkReadSaved(ctx, turn, c)
	if err != nil || saved.digest != digest || !ipEditBulkResultEqual(saved.value, value) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return saved, nil
}

// applyIPEditBulkBegin is the closed command body. A fresh bundle births the
// repair owner and captures the complete original cohort atomically; a saved
// receipt returns its exact original result even after the catalogue moved.
func applyIPEditBulkBegin(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipEntryBudget, c *ipEditBulkCommand) (out ipEditBulkCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEditBulkCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEditBulkBegin {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPEditBulkBegin, 1, c.identity, c.request)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPEntryOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipEditBulkLookupReceipt(ctx, turn, c)
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
	applied, err := ipEditBulkWriteFresh(ctx, turn, c)
	if err != nil {
		return zero, err
	}
	out.Value, out.Digest, out.Outcome.State = applied.value, applied.digest, Applied
	return out, nil
}
