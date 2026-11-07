package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type scanDirectoryLookup struct {
	owner    receiptOwnerFacts
	original scanRunOriginalScope
	decision receiptDecision
	result   scanDirectorySaved
	digest   ResultDigest
}

// Actual native lengths are read BEFORE payloads. Reserve only the actual
// complete envelope, including copies/digest/RETURNING; never a maximum-sized
// speculative allocation. Native/pool/index/WAL Work is separately composed.
func directoryMemory(ctx context.Context, t *commandTurn, n uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if t == nil || t.transaction == nil || (t.transaction.descriptor.ID != InventoryDirectoryDiscover && t.transaction.descriptor.ID != InventoryDirectoryEOF) {
		return ErrInvalid
	}
	if n > (math.MaxUint64-65536)/32 {
		return ErrCap
	}
	return t.requireCommandMemory(ctx, t.transaction.descriptor.ID, 65536+32*n, "directory_memory_partition")
}
func directoryNativeBytes(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (uint64, error) {
	rows, err := t.query(ctx, t.steps[id].statementStep, args...)
	if err != nil {
		return 0, err
	}
	var n int64
	if !rows.Next() {
		return 0, errors.Join(sql.ErrNoRows, rows.Err(), rows.Close())
	}
	err = rows.Scan(&n)
	if err == nil && rows.Next() {
		err = ErrResolveRequired
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return 0, err
	}
	if n < 0 || n > maxCanonicalRequestBytes {
		return 0, ErrCap
	}
	return uint64(n), nil
}
func directoryPoint(ctx context.Context, t *commandTurn, id stmt.ID, args []any, dest ...any) error {
	rows, err := t.query(ctx, t.steps[id].statementStep, args...)
	if err != nil {
		return err
	}
	if !rows.Next() {
		return errors.Join(sql.ErrNoRows, rows.Err(), rows.Close())
	}
	err = rows.Scan(dest...)
	if err == nil && rows.Next() {
		err = ErrResolveRequired
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

func lookupScanDirectory(ctx context.Context, t *commandTurn, q ScanReceiptRequest, kind CommandID) (out scanDirectoryLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || (kind != InventoryDirectoryDiscover && kind != InventoryDirectoryEOF) || t.transaction.descriptor.ID != kind {
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
		return scanDirectoryLookup{owner: owner, original: o, decision: decision}, e
	}
	total := n
	n, err = directoryNativeBytes(ctx, t, scanDirectoryReceiptLengths, q.RunID, inc, q.Receipt.Identity.Sequence)
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
		case "inventory_directory_discover":
			facts.kind = InventoryDirectoryDiscover
		case "inventory_directory_eof":
			facts.kind = InventoryDirectoryEOF
		}
	}
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(expected, kind, owner, facts)
	if err != nil {
		return out, err
	}
	out = scanDirectoryLookup{owner: owner, original: o, decision: decision}
	if decision != receiptReplay {
		return out, nil
	}
	if sourceRevision.Valid || count != 0 || sourceID != o.SourceID || sourceInc != o.SourceIncarnation {
		return out, ErrResolveRequired
	}
	v := scanDirectorySaved{Original: o, AcceptedRunRevision: revision, AppliedMS: applied}
	var parentPages, parentSkipped sql.NullInt64
	var cursorKind string
	var parentPath, parentIdentity, parentRevision, old, nextBytes []byte
	err = directoryPoint(ctx, t, scanDirectoryBody, []any{q.RunID, inc, q.Receipt.Identity.Sequence}, &v.Directory.Path, &v.Directory.Identity, &v.Directory.Revision, &v.Directory.Cursor, &v.Directory.State, &v.Directory.Pages, &v.Directory.Skipped, &parentPages, &parentSkipped, &cursorKind, &parentPath, &parentIdentity, &parentRevision, &old, &nextBytes)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	v.ParentPath, v.ParentIdentity, v.ParentRevision, v.ExpectedCursor, v.NextCursor = string(parentPath), string(parentIdentity), string(parentRevision), string(old), string(nextBytes)
	if cursorKind != inventoryLinuxCursor || v.NextCursor != next || parentPages.Valid != (kind == InventoryDirectoryDiscover) || parentSkipped.Valid != (kind == InventoryDirectoryDiscover) {
		return out, ErrResolveRequired
	}
	v.ParentPages, v.ParentSkipped = parentPages.Int64, parentSkipped.Int64
	actual, err := directoryResultDigest(kind, q.Receipt.Identity, q.Receipt.RequestDigest, v)
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

func acknowledgeScanDirectory(ctx context.Context, t *commandTurn, q ScanReceiptRequest, l scanDirectoryLookup) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(t), value, err) }()
	if l.decision == receiptRetired {
		return l.owner.acknowledgedSequence, nil
	}
	if l.decision != receiptReplay || q.Receipt.Identity.Sequence != l.owner.lastSequence {
		return 0, ErrResolveRequired
	}
	result, err := t.exec(ctx, t.steps[scanReceiptAck].statementStep, q.Receipt.Identity.Sequence, q.RunID, q.Receipt.Identity.Incarnation[:], l.owner.acknowledgedSequence, l.owner.lastSequence)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return 0, errors.Join(ErrConflict, err)
	}
	return q.Receipt.Identity.Sequence, nil // floor ONLY; no delete, mutation, refund or ACK receipt
}
