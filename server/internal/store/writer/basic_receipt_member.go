package writer

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// FIRST Accept has one ordinary child. The shared statement definitions are
// reused verbatim; this adapter checks Basic's OWN exact purpose and memory.
// It never calls Inventory's reader, memory helper, authority or result codec.
func readBasicReceiptMember(ctx context.Context, turn *commandTurn, q BasicReceiptRequest, header [7]int64) (value InventoryMember, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || q.Purpose != BasicPurposeAccept || turn.transaction.descriptor.ID != BasicAccept {
		return value, ErrInvalid
	}
	initial, err := basicReceiptRequiredBytes(q, header)
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, q.Purpose, initial); err != nil {
		return value, err
	}
	for _, id := range []stmt.ID{scanMemberKeys, scanMemberByteLengths, scanReceiptResults} {
		if _, ok := turn.steps[id]; !ok {
			return value, invalid("basic_receipt_member_steps")
		}
	}
	// Only scalar keys are read before the full TEXT allocation envelope.
	rows, err := turn.query(ctx, turn.steps[scanMemberKeys].statementStep, q.Scan.RunID, q.Scan.Receipt.Identity.Incarnation[:], q.Scan.Receipt.Identity.Sequence, int64(2))
	if err != nil {
		return value, err
	}
	var input, kind, child int64
	if err = basicReceiptPoint(rows, &input, &kind, &child); err != nil {
		return value, errors.Join(ErrResolveRequired, err)
	}
	if input != 0 || kind != 0 || child != 0 {
		return value, ErrResolveRequired
	}
	rows, err = turn.query(ctx, turn.steps[scanMemberByteLengths].statementStep, q.Scan.RunID, q.Scan.Receipt.Identity.Incarnation[:], q.Scan.Receipt.Identity.Sequence, input, kind, child)
	if err != nil {
		return value, err
	}
	var objectBytes int64
	if err = basicReceiptPoint(rows, &objectBytes); err != nil {
		return value, errors.Join(ErrResolveRequired, err)
	}
	required, err := basicReceiptMemberRequiredBytes(q, header, objectBytes)
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, q.Purpose, required); err != nil {
		return value, err
	}
	rows, err = turn.query(ctx, turn.steps[scanReceiptResults].statementStep, q.Scan.RunID, q.Scan.Receipt.Identity.Incarnation[:], q.Scan.Receipt.Identity.Sequence, int64(2))
	if err != nil {
		return value, err
	}
	if err = basicReceiptPoint(rows, &value.InputOrdinal, &value.ChildKind, &value.ChildOrdinal, &value.ObjectID, &value.LibraryID, &value.SubjectID, &value.AssetID, &value.AcceptedAssetRevision); err != nil {
		return value, errors.Join(ErrResolveRequired, err)
	}
	if value.InputOrdinal != input || value.ChildKind != kind || value.ChildOrdinal != child || int64(len(value.ObjectID)) != objectBytes || !utf8.ValidString(value.ObjectID) || value.LibraryID != q.Scan.LibraryID || value.SubjectID <= 0 || value.AssetID <= 0 || value.AcceptedAssetRevision <= 0 {
		return value, ErrResolveRequired
	}
	return value, nil
}

func basicReceiptMemberRequiredBytes(q BasicReceiptRequest, header [7]int64, objectBytes int64) (uint64, error) {
	if q.Purpose != BasicPurposeAccept || objectBytes <= 0 {
		return 0, ErrResolveRequired
	}
	required, err := basicReceiptRequiredBytes(q, header)
	if err != nil {
		return 0, err
	}
	// Complete result wire: fixed header, all seven native header strings and
	// one member's 64 fixed bytes plus exact native object TEXT. No prefix cap.
	remaining := int64(maxCanonicalResultBytes - basicResultFixedBytes(q.Purpose) - len(q.Scan.RunID) - 64)
	for _, n := range header {
		if n < 0 || n > remaining {
			return 0, ErrCap
		}
		remaining -= n
	}
	if objectBytes > remaining {
		return 0, ErrCap
	}
	return required + 8*uint64(objectBytes) + 8*64, nil
}

func basicReceiptMemberDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, d := range scanBytePreflightDefinitions() {
		if d.ID == scanMemberKeys || d.ID == scanMemberByteLengths {
			defs = append(defs, d)
		}
	}
	for _, d := range scanReceiptDefinitions() {
		if d.ID == scanReceiptResults {
			defs = append(defs, d)
		}
	}
	return defs
}
