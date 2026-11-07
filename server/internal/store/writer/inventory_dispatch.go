package writer

import (
	"context"
	"errors"
	"sync/atomic"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const inventoryFreshLibrary stmt.ID = "scan.inventory_accept.fresh_library"

// Retirement is fresh eligibility ONLY, never a historical disclosure filter.
func inventoryFreshLibraryDefinition() stmt.Definition {
	return stmt.Definition{ID: inventoryFreshLibrary, Owner: "store/writer Inventory fresh gate", CallSites: []string{"store/writer/inventory_dispatch.go"}, Kind: stmt.Read,
		SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
		Params: []stmt.Param{{Name: "library_id", Type: stmt.Integer}, {Name: "library_key", Type: stmt.Text}},
		Cohorts: []stmt.Cohort{
			{Name: "active", Bindings: []any{int64(7), "scan-library"}},
			{Name: "retired", Bindings: []any{int64(8), "retired-library"}},
			{Name: "missing", Bindings: []any{int64(9), "scan-library"}},
		}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}}
}

func checkInventoryFreshLibrary(ctx context.Context, turn *commandTurn, q ScanReceiptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := turn.query(ctx, turn.steps[inventoryFreshLibrary].statementStep, q.LibraryID, q.LibraryKey)
	if err != nil {
		return err
	}
	readErr := func() error {
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return ErrConflict // fresh library is no longer eligible; never poison
		}
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if id != q.LibraryID || rows.Next() {
			return ErrResolveRequired
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return err
	}
	return turn.checkLifetimes(ctx)
}

// FIRST is an internal ScanDriver lifetime, never an account/session role or
// caller-selected system flag. There is no public capability constructor.
type scanScope struct {
	runID, libraryKey string
	libraryID         int64
	incarnation       [16]byte
	lifetime          context.Context
	revoked           atomic.Bool
	driver            *ScanDriver
}

type inventoryInvocation struct {
	disclosure             *ScanDisclosure
	request                ScanReceiptRequest
	input                  *inventoryAcceptCommand
	lifecycle              CommandID // zero is ordinary N1; create/claim remain distinct
	issuer                 *scanIssuerInvocation
	directory              CommandID
	directoryInput         *scanDirectoryCommand
	directoryTerminalInput *scanDirectoryTerminalCommand
}

func (r *effectiveRuntime) currentRunDisclosure(w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, scan *inventoryInvocation) error {
	if scan == nil {
		return r.currentDisclosure(w, cap, request)
	}
	c, q := scan.disclosure, scan.request
	if c != nil && c.scope != nil && c.scope.driver != nil {
		if err := c.scope.driver.current(); err != nil {
			return errors.Join(ErrDisclosureDenied, err)
		}
	}
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

func acknowledgeInventory(ctx context.Context, turn *commandTurn, q ScanReceiptRequest, lookup inventoryReceiptLookup) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	owner := lookup.owner
	if err := turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
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
	if err := turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	return q.Receipt.Identity.Sequence, nil
}
