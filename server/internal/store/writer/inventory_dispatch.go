package writer

import (
	"context"
	"sync/atomic"
	"unicode/utf8"
)

// FIRST is an internal ScanDriver lifetime, never an account/session role or
// caller-selected system flag. There is no public capability constructor.
type scanScope struct {
	runID, libraryKey string
	libraryID         int64
	incarnation       [16]byte
	lifetime          context.Context
	revoked           atomic.Bool
}

type inventoryInvocation struct {
	disclosure *ScanDisclosure
	request    ScanReceiptRequest
	accept     bool
}

func (r *effectiveRuntime) currentRunDisclosure(w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, scan *inventoryInvocation) error {
	if scan == nil {
		return r.currentDisclosure(w, cap, request)
	}
	c, q := scan.disclosure, scan.request
	if c == nil || c.issuer != w || c.scope == nil || c.scope.lifetime == nil || c.scope.revoked.Load() || c.scope.lifetime.Err() != nil ||
		q.RunID == "" || q.LibraryID <= 0 || q.LibraryKey == "" || q.Receipt.Identity.Sequence <= 0 ||
		len(q.RunID) > maxCanonicalRequestBytes || len(q.LibraryKey) > maxCanonicalRequestBytes-len(q.RunID) ||
		!utf8.ValidString(q.RunID) || !utf8.ValidString(q.LibraryKey) ||
		c.scope.runID != q.RunID || c.scope.libraryID != q.LibraryID || c.scope.libraryKey != q.LibraryKey || c.scope.incarnation != q.Receipt.Identity.Incarnation {
		return ErrDisclosureDenied
	}
	return nil
}

func inventoryHistoricalResult(q ScanReceiptRequest, lookup inventoryReceiptLookup) InventoryResult {
	out := InventoryResult{Outcome: ReceiptOutcome{Reference: q.Receipt}}
	switch lookup.decision {
	case receiptFresh:
		out.Outcome.State = Pending
	case receiptRetired:
		out.Outcome.State = ResultRetired
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		v := lookup.result
		out.RunID, out.AcceptedRunRevision, out.AppliedMS = v.RunID, v.AcceptedRunRevision, v.AppliedMS
		out.SourceID, out.SourceIncarnation, out.SourceRevision, out.NextCursor = v.SourceID, v.SourceIncarnation, v.SourceRevision, v.NextCursor
		out.ResultCount, out.Digest = v.ResultCount, lookup.digest
		out.Members = make([]InventoryMember, len(v.Members))
		for i, m := range v.Members {
			out.Members[i] = InventoryMember{m.InputOrdinal, m.ChildKind, m.ChildOrdinal, m.ObjectID, m.LibraryID, m.SubjectID, m.AssetID, m.AcceptedAssetRevision}
		}
	}
	return out
}

func acknowledgeInventory(ctx context.Context, turn *commandTurn, q ScanReceiptRequest, lookup inventoryReceiptLookup) (int64, error) {
	owner := lookup.owner
	if lookup.decision == receiptRetired {
		return owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.Receipt.Identity.Sequence != owner.lastSequence {
		return 0, ErrResolveRequired
	}
	result, err := turn.exec(ctx, turn.steps[scanReceiptAck].statementStep, q.Receipt.Identity.Sequence,
		q.RunID, q.Receipt.Identity.Incarnation[:], owner.acknowledgedSequence, owner.lastSequence)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, ErrConflict
	}
	return q.Receipt.Identity.Sequence, nil
}
