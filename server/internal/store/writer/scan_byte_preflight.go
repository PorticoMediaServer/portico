package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const scanByteFixedWorkspace = uint64(64 << 10)

// Lengths are private same-snapshot data, never authority or an admitted budget.
// A fixed array bounds preflight storage independently of native payload size.
type scanByteVector struct {
	exists bool
	count  int
	cells  [22]sql.NullInt64
	total  uint64
}

func scanByteColumnCount(id stmt.ID) int {
	switch id {
	case scanRunByteLengths:
		return 12
	case scanJobByteLengths, scanRootByteLengths:
		return 4
	case scanReceiptByteLengths:
		return 22
	case scanMemberByteLengths:
		return 1
	default:
		return 0
	}
}

func readScanByteLengths(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (value scanByteVector, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return value, ErrInvalid
	}
	switch turn.transaction.descriptor.ID {
	case InventoryCreate, InventoryClaim, InventoryAccept:
	default:
		return value, ErrInvalid
	}
	count := scanByteColumnCount(id)
	step, ok := turn.steps[id]
	if count == 0 || !ok || step.kind != stmt.Read {
		return value, invalid("scan_byte_preflight_step")
	}
	if err = requireScanByteMemory(ctx, turn, scanByteFixedWorkspace); err != nil {
		return value, err
	}
	rows, err := turn.query(ctx, step.statementStep, args...)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		return value, rows.Err()
	}
	var destinations [22]any
	for i := 0; i < count; i++ {
		destinations[i] = &value.cells[i]
	}
	if err = rows.Scan(destinations[:count]...); err != nil {
		return value, err
	}
	if rows.Next() {
		return value, ErrResolveRequired
	}
	if err = rows.Err(); err != nil {
		return value, err
	}
	for _, n := range value.cells[:count] {
		if !n.Valid {
			continue
		}
		if n.Int64 < 0 || uint64(n.Int64) > math.MaxUint64-value.total {
			return value, ErrCap
		}
		value.total += uint64(n.Int64)
	}
	value.exists, value.count = true, count
	return value, nil
}

// The multiplier covers retained request, native copy/readback and canonical
// encoding workspaces already used by lifecycle preparation. It is applied to
// actual checked bytes, never an unconditional maximum or a second ledger.
func scanActualByteForecast(vectors ...scanByteVector) (uint64, error) {
	total := uint64(0)
	for _, v := range vectors {
		if !v.exists {
			return 0, ErrConflict
		}
		if v.total > math.MaxUint64-total {
			return 0, ErrCap
		}
		total += v.total
	}
	if total > (math.MaxUint64-scanByteFixedWorkspace)/32 {
		return 0, ErrCap
	}
	return scanByteFixedWorkspace + 32*total, nil
}

func requireScanByteMemory(ctx context.Context, turn *commandTurn, required uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	switch turn.transaction.descriptor.ID {
	case InventoryCreate, InventoryClaim:
		return turn.requireScanLifecycleMemory(ctx, required)
	case InventoryAccept:
		return turn.requireInventoryMemory(ctx, required)
	default:
		return ErrInvalid
	}
}

// Same snapshot provides value stability; this echo check additionally refuses
// projection/driver drift, including NULL versus empty and byte-count changes.
func (v scanByteVector) matches(values ...any) bool {
	if !v.exists || len(values) != v.count {
		return false
	}
	for i, value := range values {
		expected := v.cells[i]
		var size int
		present := true
		switch x := value.(type) {
		case nil:
			present = false
		case string:
			size = len(x)
		case []byte:
			present = x != nil
			size = len(x)
		case sql.NullString:
			present = x.Valid
			size = len(x.String)
			if !x.Valid && x.String != "" {
				return false
			}
		default:
			return false
		}
		if expected.Valid != present || present && (expected.Int64 < 0 || uint64(expected.Int64) != uint64(size)) {
			return false
		}
	}
	return true
}

func (v scanByteVector) matchesInventoryHeader(operation, request []byte, kind, source, incarnation string, revision sql.NullString, cursor string, digest []byte) bool {
	if v.count != 22 {
		return false
	}
	// The remainder belongs to other closed receipt purposes; N1 never copies
	// those fields. Its exact eight copied payloads still match NULL and length.
	prefix := v
	prefix.count = 8
	return prefix.matches(operation, request, kind, source, incarnation, revision, cursor, digest)
}

func (v scanByteVector) matchesCell(index int, value any) bool {
	if index < 0 || index >= v.count {
		return false
	}
	cell := scanByteVector{exists: v.exists, count: 1}
	cell.cells[0] = v.cells[index]
	return cell.matches(value)
}

func (v scanByteVector) matchesHistoryOriginal(o scanRunOriginalScope, inc []byte) bool {
	return v.count == 12 && v.matchesCell(0, o.RunID) && v.matchesCell(1, o.LibraryKey) &&
		v.matchesCell(2, o.ResolvedRoot) && v.matchesCell(3, o.SourceID) && v.matchesCell(4, o.SourceIncarnation) &&
		v.matchesCell(5, o.RootIdentity) && v.matchesCell(8, o.ScanMode) && v.matchesCell(9, o.CreateOperationID[:]) &&
		v.matchesCell(10, o.CreateRequestDigest[:]) && v.matchesCell(11, inc)
}

type scanLifecycleByteProof struct {
	prefix      scanReceiptPrefix
	facts       receiptFacts
	decision    receiptDecision
	run, header scanByteVector
}

func readScanLifecycleByteProof(ctx context.Context, turn *commandTurn, q ScanReceiptRequest, kind CommandID) (value scanLifecycleByteProof, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	value.prefix, err = readScanReceiptOwnerPrefix(ctx, turn, q)
	if err != nil {
		return value, err
	}
	expected := receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}
	if !value.prefix.owner.exists || value.prefix.owner.incarnation != q.Receipt.Identity.Incarnation {
		_, err = classifyReceipt(expected, kind, value.prefix.owner, receiptFacts{})
		return value, err
	}
	if value.prefix.libraryID != q.LibraryID {
		return value, ErrDisclosureDenied
	}
	if kind == InventoryCreate && (q.Receipt.Identity.Sequence != 1 || value.prefix.createOperation != q.Receipt.Identity.OperationID || value.prefix.createDigest != q.Receipt.RequestDigest) {
		return value, ErrConflict
	}
	value.facts, err = readScanReceiptIdentity(ctx, turn, q)
	if err != nil {
		return value, err
	}
	value.decision, err = classifyReceipt(expected, kind, value.prefix.owner, value.facts)
	if err != nil {
		return value, err
	}
	if value.decision != receiptReplay {
		return value, nil
	}
	value.run, err = readScanByteLengths(ctx, turn, scanRunByteLengths, q.RunID)
	if err != nil {
		return value, err
	}
	value.header, err = readScanByteLengths(ctx, turn, scanReceiptByteLengths, q.RunID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return value, err
	}
	required, err := scanActualByteForecast(value.run, value.header)
	if err != nil {
		return value, err
	}
	err = requireScanByteMemory(ctx, turn, required)
	return value, err
}

func scanReceiptIdentityMatches(facts receiptFacts, operation, request, digest []byte, kind string, count int64) bool {
	if !facts.exists || len(operation) != 16 || len(request) != 32 || len(digest) != 32 || count != facts.resultCount {
		return false
	}
	var op [16]byte
	var req RequestDigest
	var result ResultDigest
	copy(op[:], operation)
	copy(req[:], request)
	copy(result[:], digest)
	var purpose CommandID
	switch kind {
	case scanInventoryReceiptKind:
		purpose = InventoryAccept
	case "inventory_create":
		purpose = InventoryCreate
	case "inventory_claim":
		purpose = InventoryClaim
	}
	return op == facts.identity.command.OperationID && req == facts.identity.digest && result == facts.resultDigest && purpose == facts.kind
}

type scanSnapshotByteProof struct{ run, job, root scanByteVector }

func readScanSnapshotByteProof(ctx context.Context, turn *commandTurn, runID string) (value scanSnapshotByteProof, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if value.run, err = readScanByteLengths(ctx, turn, scanRunByteLengths, runID); err != nil {
		return value, err
	}
	if value.job, err = readScanByteLengths(ctx, turn, scanJobByteLengths, runID); err != nil {
		return value, err
	}
	if value.root, err = readScanByteLengths(ctx, turn, scanRootByteLengths, runID, "."); err != nil {
		return value, err
	}
	required, err := scanActualByteForecast(value.run, value.job, value.root)
	if err != nil {
		return value, err
	}
	if err = requireScanByteMemory(ctx, turn, required); err != nil {
		return value, err
	}
	return value, nil
}

func (p scanSnapshotByteProof) matches(v scanRunSnapshot) bool {
	r, s := v.run, v.run.scope
	return p.run.matches(s.runID, s.libraryKey, s.resolvedRoot, s.sourceID, s.incarnation, s.rootIdentity, r.phase, r.finishedAt, s.scanMode, s.createOperation[:], s.createDigest[:], s.commandIncarnation[:]) &&
		p.job.matches(v.job.libraryKey, v.job.status, v.job.createdAt, v.job.errorText) &&
		p.root.matches(v.root.identity, v.root.revision, v.root.cursor, v.root.state)
}

func readCheckedScanRunSnapshot(ctx context.Context, turn *commandTurn, runID string) (value scanRunSnapshot, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	proof, err := readScanSnapshotByteProof(ctx, turn, runID)
	if err != nil {
		return value, err
	}
	value, err = readScanRunSnapshot(ctx, turn, runID)
	if err == nil && !proof.matches(value) {
		err = ErrConflict
	}
	return value, err
}

// These prefixes contain only schema-bounded identity fields and scalar keys.
// They permit floor/kind classification without copying a saved TEXT payload.
type scanReceiptPrefix struct {
	owner           receiptOwnerFacts
	libraryID       int64
	createOperation [16]byte
	createDigest    RequestDigest
}

func readScanReceiptOwnerPrefix(ctx context.Context, turn *commandTurn, q ScanReceiptRequest) (value scanReceiptPrefix, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if err = requireScanByteMemory(ctx, turn, scanByteFixedWorkspace); err != nil {
		return value, err
	}
	step, ok := turn.steps[scanReceiptOwnerPrefix]
	if !ok {
		return value, invalid("scan_byte_owner_step")
	}
	rows, err := turn.query(ctx, step.statementStep, q.RunID, q.LibraryKey)
	if err != nil {
		return value, err
	}
	var inc, operation, digest []byte
	err = scanBytePoint(rows, &inc, &value.owner.lastSequence, &value.owner.acknowledgedSequence, &value.libraryID, &operation, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if len(inc) != 16 || len(operation) != 16 || len(digest) != 32 {
		return value, ErrResolveRequired
	}
	value.owner.exists = true
	copy(value.owner.incarnation[:], inc)
	copy(value.createOperation[:], operation)
	copy(value.createDigest[:], digest)
	return value, nil
}

func readScanReceiptIdentity(ctx context.Context, turn *commandTurn, q ScanReceiptRequest) (value receiptFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if err = requireScanByteMemory(ctx, turn, scanByteFixedWorkspace); err != nil {
		return value, err
	}
	step, ok := turn.steps[scanReceiptIdentityPrefix]
	if !ok {
		return value, invalid("scan_byte_identity_step")
	}
	rows, err := turn.query(ctx, step.statementStep, q.RunID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return value, err
	}
	var operation, request, result []byte
	var kind string
	err = scanBytePoint(rows, &operation, &request, &kind, &value.resultCount, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if len(operation) != 16 || len(request) != 32 || len(result) != 32 {
		return value, ErrResolveRequired
	}
	value.exists, value.identity.command = true, q.Receipt.Identity
	copy(value.identity.command.OperationID[:], operation)
	copy(value.identity.digest[:], request)
	copy(value.resultDigest[:], result)
	switch kind {
	case scanInventoryReceiptKind:
		value.kind = InventoryAccept
	case "inventory_create":
		value.kind = InventoryCreate
	case "inventory_claim":
		value.kind = InventoryClaim
	}
	return value, nil
}

type scanMemberByteProof struct {
	keys  [3]int64
	bytes scanByteVector
}

// Probe only bounded numeric keys first. Even a malformed extra or gapped row
// cannot cause an un-preflighted object_id to be copied by the cohort read.
func readScanMemberByteProof(ctx context.Context, turn *commandTurn, q ScanReceiptRequest, count int64) (value scanMemberByteProof, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if count != inventoryReceiptMaxMembers {
		return value, ErrResolveRequired
	}
	step, ok := turn.steps[scanMemberKeys]
	if !ok {
		return value, invalid("scan_byte_member_keys_step")
	}
	rows, err := turn.query(ctx, step.statementStep, q.RunID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence, count+1)
	if err != nil {
		return value, err
	}
	if err = scanBytePoint(rows, &value.keys[0], &value.keys[1], &value.keys[2]); err != nil {
		return value, errors.Join(ErrResolveRequired, err)
	}
	if value.keys != ([3]int64{}) {
		return value, ErrResolveRequired // FIRST ordinary result has exactly (0,0,0)
	}
	value.bytes, err = readScanByteLengths(ctx, turn, scanMemberByteLengths, q.RunID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence, value.keys[0], value.keys[1], value.keys[2])
	if err == nil && !value.bytes.exists {
		err = ErrResolveRequired
	}
	return value, err
}

// Like the protected point readers, but preserves absence as sql.ErrNoRows so
// the immutable receipt classifier, rather than a fresh gate, owns that case.
func scanBytePoint(rows *commandRows, destinations ...any) (err error) {
	if rows == nil {
		return ErrInvalid
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err = rows.Err(); err == nil {
			err = sql.ErrNoRows
		}
		return err
	}
	if err = rows.Scan(destinations...); err != nil {
		return err
	}
	if rows.Next() {
		return ErrResolveRequired
	}
	return rows.Err()
}
