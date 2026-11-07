package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

type scanDirectoryTerminalLookup struct {
	owner    receiptOwnerFacts
	original scanRunOriginalScope
	decision receiptDecision
	result   scanDirectoryTerminalSaved
	digest   ResultDigest
}

// Current native disclosure is the owner's dispatcher responsibility. History
// precedes every freshness/lease/root/frontier check, in the SAME command turn.
func lookupScanDirectoryTerminal(ctx context.Context, t *commandTurn, q ScanReceiptRequest, kind CommandID) (out scanDirectoryTerminalLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || (kind != InventoryDirectoryRefuse && kind != InventoryTraversalComplete) || t.transaction.descriptor.ID != kind {
		return out, ErrInvalid
	}
	if q.RunID == "" || q.LibraryID <= 0 || q.LibraryKey == "" || q.Receipt.Identity.Sequence <= 0 || len(q.RunID) > maxCanonicalRequestBytes || len(q.LibraryKey) > maxCanonicalRequestBytes-len(q.RunID) || !utf8.ValidString(q.RunID) || !utf8.ValidString(q.LibraryKey) {
		return out, ErrInvalid
	}
	n, err := directoryNativeBytes(ctx, t, scanDirectoryRunLengths, q.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = classifyReceipt(receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}, kind, receiptOwnerFacts{}, receiptFacts{})
		return out, err
	}
	if err != nil {
		return out, err
	}
	if err = directoryMemory(ctx, t, n+uint64(len(q.RunID)+len(q.LibraryKey))); err != nil {
		return out, err
	}
	var inc, op, digest []byte
	var follow int64
	o := scanRunOriginalScope{RunID: q.RunID}
	var owner receiptOwnerFacts
	err = directoryPoint(ctx, t, scanRunHistoryOwner, []any{q.RunID}, &inc, &owner.lastSequence, &owner.acknowledgedSequence, &o.LibraryID, &o.LibraryKey, &o.ResolvedRoot, &follow, &op, &digest, &o.SourceID, &o.SourceGeneration, &o.SourceIncarnation, &o.RootIdentity, &o.PolicyRevision, &o.ScanMode)
	if err != nil {
		return out, err
	}
	if len(inc) != 16 || len(op) != 16 || len(digest) != 32 || (follow != 0 && follow != 1) {
		return out, ErrResolveRequired
	}
	if o.LibraryID != q.LibraryID || o.LibraryKey != q.LibraryKey {
		return out, ErrDisclosureDenied
	}
	copy(owner.incarnation[:], inc)
	owner.exists = true
	copy(o.CreateOperationID[:], op)
	copy(o.CreateRequestDigest[:], digest)
	o.FollowSymlinks = follow == 1
	expected := receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}
	if owner.incarnation != q.Receipt.Identity.Incarnation || q.Receipt.Identity.Sequence <= owner.acknowledgedSequence {
		decision, e := classifyReceipt(expected, kind, owner, receiptFacts{})
		return scanDirectoryTerminalLookup{owner: owner, original: o, decision: decision}, e
	}
	total := n
	n, err = directoryNativeBytes(ctx, t, scanTerminalReceiptLengths, q.RunID, inc, q.Receipt.Identity.Sequence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	total += n
	if err = directoryMemory(ctx, t, total); err != nil {
		return out, err
	}
	var operation, request, resultDigest []byte
	var savedKind, sourceID, sourceInc, next string
	var sourceRevision sql.NullString
	var count, revision, applied int64
	err = directoryPoint(ctx, t, scanReceiptLookup, []any{q.RunID, inc, q.Receipt.Identity.Sequence}, &operation, &request, &savedKind, &revision, &applied, &sourceID, &sourceInc, &sourceRevision, &next, &count, &resultDigest)
	var facts receiptFacts
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(resultDigest) != 32 {
			return out, ErrResolveRequired
		}
		facts.exists = true
		facts.identity.command = q.Receipt.Identity
		copy(facts.identity.command.OperationID[:], operation)
		copy(facts.identity.digest[:], request)
		copy(facts.resultDigest[:], resultDigest)
		facts.resultCount = count
		switch savedKind {
		case "inventory_directory_refuse":
			facts.kind = InventoryDirectoryRefuse
		case "inventory_traversal_complete":
			facts.kind = InventoryTraversalComplete
		}
	}
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(expected, kind, owner, facts)
	if err != nil {
		return out, err
	}
	out = scanDirectoryTerminalLookup{owner: owner, original: o, decision: decision}
	if decision != receiptReplay {
		return out, nil
	}
	if sourceRevision.Valid || count != 0 || sourceID != o.SourceID || sourceInc != o.SourceIncarnation {
		return out, ErrResolveRequired
	}
	v, err := readScanDirectoryTerminalBody(ctx, t, q, kind, o, revision, applied, next)
	if err != nil {
		return out, err
	}
	actual, err := directoryTerminalResultDigest(kind, q.Receipt.Identity, q.Receipt.RequestDigest, v)
	if err != nil || actual != ResultDigest(facts.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	rows, err := t.query(ctx, t.steps[scanReceiptReclaimKey].statementStep, q.RunID, inc, q.Receipt.Identity.Sequence)
	if err != nil {
		return out, err
	}
	child := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil || child {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result, out.digest = v, actual
	return out, nil
}

func readScanDirectoryTerminalBody(ctx context.Context, t *commandTurn, q ScanReceiptRequest, kind CommandID, o scanRunOriginalScope, revision, applied int64, next string) (value scanDirectoryTerminalSaved, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(t), value, err) }()
	v := scanDirectoryTerminalSaved{Original: o, AcceptedRunRevision: revision, AppliedMS: applied}
	var d [5]sql.NullString
	var pages, skipped, blocked, protected sql.NullInt64
	var completed, code, entry, cursor sql.NullString
	var raw, childIdentity, childRevision, parentPath, parentIdentity, parentRevision, payload []byte
	// The native driver returns a typed nil []byte for an empty BLOB. Preserve
	// SQL NULL presence separately: the initial cursor is a valid empty BLOB.
	var old sql.Null[[]byte]
	err = directoryPoint(ctx, t, scanTerminalBody, []any{q.RunID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence}, &d[0], &d[1], &d[2], &d[3], &d[4], &pages, &skipped, &completed, &blocked, &protected, &raw, &code, &entry, &childIdentity, &childRevision, &cursor, &parentPath, &parentIdentity, &parentRevision, &old, &payload)
	if err != nil {
		return scanDirectoryTerminalSaved{}, errors.Join(ErrResolveRequired, err)
	}
	if kind == InventoryDirectoryRefuse {
		for _, s := range d {
			if s.Valid {
				return scanDirectoryTerminalSaved{}, ErrResolveRequired
			}
		}
		if pages.Valid || skipped.Valid || completed.Valid || blocked.Valid || protected.Valid || !code.Valid || code.String != "filename_unrepresentable" || !entry.Valid || entry.String != "directory" || !cursor.Valid || cursor.String != inventoryLinuxCursor || raw == nil || childIdentity == nil || childRevision == nil || parentPath == nil || parentIdentity == nil || parentRevision == nil || !old.Valid || payload == nil || string(payload) != next {
			return scanDirectoryTerminalSaved{}, ErrResolveRequired
		}
		v.RefusedRelativePath, v.RefusedDirectoryIdentity, v.RefusedDirectoryRevision = raw, childIdentity, childRevision
		v.ParentPath, v.ParentIdentity, v.ParentRevision, v.ExpectedCursor, v.NextCursor = string(parentPath), string(parentIdentity), string(parentRevision), string(old.V), string(payload)
	} else if kind == InventoryTraversalComplete {
		for _, s := range d {
			if !s.Valid {
				return scanDirectoryTerminalSaved{}, ErrResolveRequired
			}
		}
		if !pages.Valid || !skipped.Valid || !completed.Valid || !blocked.Valid || !protected.Valid || blocked.Int64 < 0 || blocked.Int64 > 1 || protected.Int64 < 0 || protected.Int64 > 1 || code.Valid || entry.Valid || cursor.Valid || raw != nil || childIdentity != nil || childRevision != nil || parentPath != nil || parentIdentity != nil || parentRevision != nil || old.Valid || payload != nil || next != "" {
			return scanDirectoryTerminalSaved{}, ErrResolveRequired
		}
		v.Directory = scanRootWitness{Path: d[0].String, Identity: d[1].String, Revision: d[2].String, Cursor: d[3].String, State: d[4].String, Pages: pages.Int64, Skipped: skipped.Int64}
		v.InventoryCompletedAt, v.TraversalBlocked, v.TraversalProtected = completed.String, blocked.Int64 == 1, protected.Int64 == 1
	} else {
		return scanDirectoryTerminalSaved{}, ErrInvalid
	}
	if err = preflightDirectoryTerminalResult(kind, v); err != nil {
		return scanDirectoryTerminalSaved{}, errors.Join(ErrResolveRequired, err)
	}
	return v, nil
}

// The ACK entry performs its OWN full original classification. Only the floor
// changes; retained scalar proof and typed NO ACTION issue pins survive ACK.
func acknowledgeScanDirectoryTerminal(ctx context.Context, t *commandTurn, q ScanReceiptRequest, kind CommandID) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(t), value, err) }()
	l, err := lookupScanDirectoryTerminal(ctx, t, q, kind)
	if err != nil {
		return 0, err
	}
	if l.decision == receiptRetired {
		return l.owner.acknowledgedSequence, nil
	}
	if l.decision != receiptReplay || q.Receipt.Identity.Sequence != l.owner.lastSequence {
		return 0, ErrResolveRequired
	}
	changed, err := t.exec(ctx, t.steps[scanReceiptAck].statementStep, q.Receipt.Identity.Sequence, q.RunID, q.Receipt.Identity.Incarnation[:], l.owner.acknowledgedSequence, l.owner.lastSequence)
	if err != nil {
		return 0, err
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return 0, errors.Join(ErrConflict, err)
	}
	return q.Receipt.Identity.Sequence, nil
}
