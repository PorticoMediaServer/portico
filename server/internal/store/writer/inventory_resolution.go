package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The initial adapter supports ONE ordinary member. This count bound is not
// native TEXT/allocation/hold qualification or a caller-selectable resource cap.
const inventoryReceiptMaxMembers = 1

// inventoryReceiptLookup is private protocol data, never an Applied outcome or
// disclosure capability. The sole dispatcher must establish current opaque AND
// native ScanDriver scope BEFORE calling this adapter, and recheck revocation
// before releasing a result. SC's fresh handler must use W's eventual guarded
// resolveInventoryBeforeFresh entry, not invoke this lookup to bypass it.
type inventoryReceiptLookup struct {
	owner    receiptOwnerFacts
	decision receiptDecision
	result   inventoryAcceptResult
	digest   ResultDigest
}

// lookupInventoryAccept reads only the original owner/receipt/cohort through
// the same protected turn. It never reads current source/lease/epoch rows,
// mutates a floor, admits, commits or synthesizes a missing historical result.
// Every error returns ZERO data. The public facade remains unimplemented until
// W composes disclosure, complete byte/native-work recipes and physical terminal
// handling. A two-row LIMIT is not a byte bound or a memory reservation.
func lookupInventoryAccept(ctx context.Context, turn *commandTurn, request ScanReceiptRequest) (inventoryReceiptLookup, error) {
	if ctx == nil || turn == nil || turn.transaction == nil || request.RunID == "" || request.LibraryID <= 0 ||
		len(request.RunID) > maxCanonicalRequestBytes || !utf8.ValidString(request.RunID) || request.Receipt.Identity.Sequence <= 0 {
		return inventoryReceiptLookup{}, ErrInvalid
	}
	for _, id := range []stmt.ID{scanReceiptOwner, scanReceiptLookup, scanReceiptResults} {
		if _, ok := turn.steps[id]; !ok {
			return inventoryReceiptLookup{}, invalid("inventory_receipt_steps")
		}
	}
	expected := receiptIdentity{request.Receipt.Identity, request.Receipt.RequestDigest}
	owner, err := inventoryOwnerFacts(ctx, turn, request.RunID)
	if err != nil {
		return inventoryReceiptLookup{}, err
	}
	if !owner.exists || owner.incarnation != expected.command.Incarnation {
		_, err := classifyReceipt(expected, InventoryAccept, owner, receiptFacts{})
		return inventoryReceiptLookup{}, err
	}
	row, err := turn.queryRow(ctx, turn.steps[scanReceiptLookup].statementStep,
		request.RunID, expected.command.Incarnation[:], expected.command.Sequence)
	if err != nil {
		return inventoryReceiptLookup{}, err
	}
	var operation, digest, resultDigest []byte
	var kind string
	v := inventoryAcceptResult{RunID: request.RunID}
	err = row.Scan(&operation, &digest, &kind, &v.AcceptedRunRevision, &v.AppliedMS,
		&v.SourceID, &v.SourceIncarnation, &v.SourceRevision, &v.NextCursor, &v.ResultCount, &resultDigest)
	var facts receiptFacts
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		if len(operation) != 16 || len(digest) != 32 || len(resultDigest) != 32 {
			return inventoryReceiptLookup{}, ErrResolveRequired
		}
		facts.exists, facts.resultCount = true, v.ResultCount
		facts.identity.command = expected.command
		copy(facts.identity.command.OperationID[:], operation)
		copy(facts.identity.digest[:], digest)
		copy(facts.resultDigest[:], resultDigest)
		if kind == scanInventoryReceiptKind {
			facts.kind = InventoryAccept
		}
	}
	if err != nil {
		return inventoryReceiptLookup{}, err
	}
	decision, err := classifyReceipt(expected, InventoryAccept, owner, facts)
	if err != nil {
		return inventoryReceiptLookup{}, err
	}
	out := inventoryReceiptLookup{owner: owner, decision: decision}
	if decision != receiptReplay {
		if err := errors.Join(ctx.Err(), turn.transaction.ctx.Err()); err != nil {
			return inventoryReceiptLookup{}, err
		}
		return out, nil // floor precedes any possibly drained result-cohort decode
	}
	if v.ResultCount <= 0 {
		return inventoryReceiptLookup{}, ErrResolveRequired
	}
	if v.ResultCount > inventoryReceiptMaxMembers {
		return inventoryReceiptLookup{}, &Refusal{Code: RefusalUnqualified}
	}
	rows, err := turn.query(ctx, turn.steps[scanReceiptResults].statementStep, request.RunID,
		expected.command.Incarnation[:], expected.command.Sequence, v.ResultCount+1)
	if err != nil {
		return inventoryReceiptLookup{}, err
	}
	defer rows.Close()
	readErr := func() error {
		for rows.Next() {
			var member inventoryResultMember
			if err := rows.Scan(&member.InputOrdinal, &member.ChildKind, &member.ChildOrdinal, &member.ObjectID,
				&member.LibraryID, &member.SubjectID, &member.AssetID, &member.AcceptedAssetRevision); err != nil {
				return err
			}
			if int64(len(v.Members)) >= v.ResultCount || member.LibraryID != request.LibraryID {
				return ErrResolveRequired
			}
			v.Members = append(v.Members, member)
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return inventoryReceiptLookup{}, err
	}
	d, err := inventoryAcceptResultDigest(expected.command, expected.digest, v)
	if err != nil || d != ResultDigest(facts.resultDigest) {
		return inventoryReceiptLookup{}, errors.Join(ErrResolveRequired, err)
	}
	if err := errors.Join(ctx.Err(), turn.transaction.ctx.Err()); err != nil {
		return inventoryReceiptLookup{}, err
	}
	out.result, out.digest = v, d
	return out, nil
}

func inventoryOwnerFacts(ctx context.Context, turn *commandTurn, runID string) (receiptOwnerFacts, error) {
	row, err := turn.queryRow(ctx, turn.steps[scanReceiptOwner].statementStep, runID)
	if err != nil {
		return receiptOwnerFacts{}, err
	}
	var inc []byte
	var owner receiptOwnerFacts
	err = row.Scan(&inc, &owner.lastSequence, &owner.acknowledgedSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, nil
	}
	if err != nil {
		return receiptOwnerFacts{}, err
	}
	if len(inc) != 16 {
		return receiptOwnerFacts{}, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], inc)
	return owner, nil
}
