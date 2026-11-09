package writer

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func iaRetirementReclaimFixture(t *testing.T) (*executor, *checkpointComponent, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	for _, name := range []string{"testdata/ia-retirement-begin-draft.sql", "testdata/ia-retirement-reclaim-indexes-draft.sql"} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	registry, err := stmttest.New(t, iaRetirementReclaimDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, keeper, &iaEntryRegistry{registry, keeper}, proof
}

func iaRetirementReclaimRegistry(t *testing.T, keeper *checkpointComponent, defs []stmt.Definition) *iaEntryRegistry {
	t.Helper()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return &iaEntryRegistry{registry, keeper}
}

func iaRetirementReceiptBytes(kind, resultKind, target, state string) int64 {
	return 200 + int64(len(kind)) + int64(len(resultKind)) + int64(len(target)) + int64(len(state))
}

// Append one exact step receipt after the committed BEGIN receipt.
func iaRetirementReclaimAppendReceipt(t *testing.T, w *executor, operation, incarnation []byte, sequence int64, target string) int64 {
	t.Helper()
	bytes := iaRetirementReceiptBytes("retire_step", "library", target, "retiring")
	marker := make([]byte, 16)
	marker[0], marker[1] = 0x90, byte(sequence)
	wc008NativeExec(t, w, `INSERT INTO catalog_removal_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,
request_digest,command_kind,result_version,accepted_revision,applied_ms,result_kind,result_target,result_phase,
result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state,
result_count,result_digest,retained_bytes)
VALUES(?,?,?,?,zeroblob(32),'retire_step',1,?,?,'library',?,1,0,0,'',0,0,'retiring',0,zeroblob(32),?)`,
		operation, incarnation, sequence, marker, sequence, 1000+sequence, target, bytes)
	return bytes
}

// One committed library BEGIN, then an externally completed walk with `steps`
// exact step receipts. The completed proof is what command 106 may reclaim.
func iaRetirementReclaimSeed(t *testing.T, w *executor, keeper *checkpointComponent, p *usOwnerAuthorityProof, steps int) (ReceiptReference, iaRetirementBeginRequest, iaRetirementReclaimRequest) {
	t.Helper()
	iaRetirementBeginLibrarySeed(t, w, 900, "lib-gc", "/media/gc", "Library", 5)
	begin := iaRetirementReclaimRegistry(t, keeper, iaRetirementBeginDefinitions())
	identity := iaRetirementBeginIdentity(20, 21, 1)
	beginRequest := iaRetirementBeginLibraryRequest("lib-gc", 900, 5, "/media/gc", "Library", "Library")
	ref := iaRetirementBeginPrepared(t, IARetireLibrary, identity, beginRequest)
	turn, out, err := iaRetirementBeginRun(t, w, begin, p, IARetireLibrary, ref, beginRequest)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("seed retirement begin", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var operation, incarnation []byte
	var retained int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT original_operation_id,command_incarnation,receipt_retained_bytes FROM catalog_removal_progress WHERE kind='library' AND target='lib-gc'`).Scan(&operation, &incarnation, &retained); err != nil {
		t.Fatal(err)
	}
	for sequence := int64(2); sequence <= int64(1+steps); sequence++ {
		retained += iaRetirementReclaimAppendReceipt(t, w, operation, incarnation, sequence, "lib-gc")
	}
	last := int64(1 + steps)
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET last_command_sequence=?,acknowledged_command_sequence=?,retirement_state='complete',phase=5,
receipt_retained_count=?,receipt_retained_bytes=? WHERE kind='library' AND target='lib-gc'`, last, last-1, last, retained)
	request := iaRetirementReclaimRequest{kind: "library", target: "lib-gc", libraryID: 900, libraryKey: "lib-gc",
		resolvedRoot: "/media/gc", latestSequence: last}
	return ref, beginRequest, request
}

func iaRetirementReclaimReference(t *testing.T, identity CommandIdentity, request iaRetirementReclaimRequest) ReceiptReference {
	t.Helper()
	command, err := newIARetirementReclaimCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: identity, RequestDigest: command.digest}
}

func iaRetirementReclaimRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, identity CommandIdentity, request iaRetirementReclaimRequest) (iaRetirementReclaimOutcome, error) {
	t.Helper()
	ref := iaRetirementReclaimReference(t, identity, request)
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireReclaim, action: iaEntryApply, reference: ref})
	out, err := reclaimIARetirement(t.Context(), turn, p, t.Context(), budget, ref, request)
	if err != nil {
		turn.transaction.rollback(nil)
		return out, err
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

type iaRetirementReclaimFacts struct {
	last, acknowledged, reclaimed int64
	rows, bytes                   int64
	due, slot                     sql.NullInt64
	state                         string
	phase                         int64
}

func iaRetirementReclaimRead(t *testing.T, w *executor) iaRetirementReclaimFacts {
	t.Helper()
	var f iaRetirementReclaimFacts
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,
receipt_retained_count,receipt_retained_bytes,receipt_reclaim_due_ms,reclaim_command_sequence,retirement_state,phase
FROM catalog_removal_progress WHERE kind='library' AND target='lib-gc'`).Scan(&f.last, &f.acknowledged, &f.reclaimed, &f.rows, &f.bytes, &f.due, &f.slot, &f.state, &f.phase); err != nil {
		t.Fatal(err)
	}
	return f
}

func iaRetirementReclaimCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func TestIARetirementReclaimCompletedReceipts(t *testing.T) {
	w, keeper, r, p := iaRetirementReclaimFixture(t)
	beginRef, beginRequest, request := iaRetirementReclaimSeed(t, w, keeper, p, 3)
	before := iaRetirementReclaimRead(t, w)
	if before.state != "complete" || before.phase != 5 || before.rows != 4 || before.acknowledged != 3 || before.last != 4 {
		t.Fatal("seeded completed driver", before)
	}
	total := before.bytes
	for receipt := int64(1); receipt <= 4; receipt++ {
		charge := iaRetirementReclaimCount(t, w, `SELECT retained_bytes FROM catalog_removal_receipts WHERE command_sequence=?`, receipt)
		identity := iaRetirementBeginIdentity(byte(100+receipt), 21, before.last+1)
		request.latestSequence = before.last
		out, err := iaRetirementReclaimRun(t, w, r, p, identity, request)
		if err != nil || out.decision != receiptFresh || out.result.removedKind != "header" ||
			out.result.removedCount != 1 || !out.result.removedSequence.Valid || out.result.removedSequence.Int64 != receipt ||
			out.result.removedBytes != charge || out.result.resultSequence != receipt ||
			out.result.removedOrdinal.Valid || out.result.resultOrdinal.Valid || out.digest == (ResultDigest{}) {
			t.Fatal("fresh retirement header turn", receipt, out, err)
		}
		after := iaRetirementReclaimRead(t, w)
		if after.last != identity.Sequence || after.acknowledged != identity.Sequence || after.reclaimed != receipt ||
			after.rows != before.rows-1 || after.bytes != before.bytes-charge || after.slot.Int64 != identity.Sequence ||
			after.state != "complete" || after.phase != 5 {
			t.Fatal("retirement header ledger", receipt, after)
		}
		if after.rows > 0 && (!after.due.Valid || after.due.Int64 != out.result.appliedMS) {
			t.Fatal("remaining debt is not due", after)
		}
		if after.rows == 0 && (after.due.Valid || !out.result.noNext || after.bytes != 0) {
			t.Fatal("terminal debt still due", after, out.result)
		}
		if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 4-receipt {
			t.Fatal("receipt retired out of order", got)
		}
		before = after
	}
	if before.rows != 0 || before.bytes != 0 || total <= 0 {
		t.Fatal("retained debt survived", before)
	}
	// The permanent driver, its original request and the reclaimed watermark stay.
	if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_progress WHERE original_request_digest IS NOT NULL AND receipt_reclaim_sequence=4`); got != 1 {
		t.Fatal("permanent driver lost", got)
	}
	// A further ACK removes and refunds nothing.
	identity := iaRetirementBeginIdentity(110, 21, before.last+1)
	request.latestSequence = before.last
	none, err := iaRetirementReclaimRun(t, w, r, p, identity, request)
	if err != nil || none.decision != receiptFresh || none.result.removedKind != "none" || none.result.removedCount != 0 ||
		none.result.removedBytes != 0 || none.result.rows != 0 || none.result.bytes != 0 || !none.result.noNext ||
		none.result.resultSequence != 4 || none.result.removedOrdinal.Valid || none.result.resultOrdinal.Valid {
		t.Fatal("empty retirement ACK", none, err)
	}
	// Post-retirement resolve: the BEGIN original is Retired, never decoded.
	begin := iaRetirementReclaimRegistry(t, keeper, iaRetirementBeginDefinitions())
	turn, retired, err := iaRetirementBeginRun(t, w, begin, p, IARetireLibrary, beginRef, beginRequest)
	turn.transaction.rollback(nil)
	if err != nil || retired.decision != receiptRetired || retired.digest != (ResultDigest{}) || retired.result != (iaRetirementResult{}) {
		t.Fatal("post-retirement begin resolve", retired, err)
	}
}

func TestIARetirementReclaimReplayOldSlotAndRetired(t *testing.T) {
	w, keeper, r, p := iaRetirementReclaimFixture(t)
	_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 2)
	firstRequest := request
	firstIdentity := iaRetirementBeginIdentity(120, 21, 4)
	first, err := iaRetirementReclaimRun(t, w, r, p, firstIdentity, firstRequest)
	if err != nil || first.decision != receiptFresh || first.result.removedSequence.Int64 != 1 {
		t.Fatal(first, err)
	}
	replay, err := iaRetirementReclaimRun(t, w, r, p, firstIdentity, firstRequest)
	if err != nil || replay.decision != receiptReplay || replay.digest != first.digest || replay.result != first.result {
		t.Fatal("current slot replay", replay, err)
	}
	if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 2 {
		t.Fatal("replay retired a second receipt", got)
	}
	request.latestSequence = 4
	second, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(122, 21, 5), request)
	if err != nil || second.decision != receiptFresh {
		t.Fatal(second, err)
	}
	retired, err := iaRetirementReclaimRun(t, w, r, p, firstIdentity, firstRequest)
	if err != nil || retired.decision != receiptRetired || retired.digest != (ResultDigest{}) || retired.result.removedKind != "" {
		t.Fatal("superseded slot was decoded", retired, err)
	}
	bad := iaRetirementReclaimReference(t, iaRetirementBeginIdentity(122, 21, 5), request)
	bad.RequestDigest[0]++
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireReclaim, action: iaEntryApply, reference: bad})
	out, err := reclaimIARetirement(t.Context(), turn, p, t.Context(), budget, bad, request)
	turn.transaction.rollback(nil)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("conflicting digest admitted", out, err)
	}
}

func TestIARetirementReclaimRequiresCompletion(t *testing.T) {
	for name, mutation := range map[string]string{
		"still_retiring":     `UPDATE catalog_removal_progress SET retirement_state='retiring',phase=1`,
		"phase_not_terminal": `UPDATE catalog_removal_progress SET retirement_state='retiring',phase=4`,
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, r, p := iaRetirementReclaimFixture(t)
			_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 2)
			wc008NativeExec(t, w, mutation)
			out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(130, 21, 4), request)
			if !errors.Is(err, ErrResolveRequired) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("unfinished retirement reclaimed", out, err)
			}
			if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 3 {
				t.Fatal("unfinished retirement lost a receipt", got)
			}
		})
	}
}

func TestIARetirementReclaimProofs(t *testing.T) {
	for name, mutate := range map[string]string{
		"ledger_rows":  `UPDATE catalog_removal_progress SET receipt_retained_count=receipt_retained_count+1`,
		"ledger_short": `UPDATE catalog_removal_progress SET receipt_retained_bytes=1`,
		"receipt_gap":  `DELETE FROM catalog_removal_receipts WHERE command_sequence=1`,
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, r, p := iaRetirementReclaimFixture(t)
			_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 3)
			wc008NativeExec(t, w, mutate)
			receipts := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`)
			out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(140, 21, 5), request)
			if !errors.Is(err, ErrResolveRequired) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("incomplete driver proof admitted", out, err)
			}
			if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != receipts {
				t.Fatal("refusal retired a receipt", got, receipts)
			}
			if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_progress WHERE reclaim_command_sequence IS NULL`); got != 1 {
				t.Fatal("refusal wrote the slot", got)
			}
		})
	}
	t.Run("extra_receipt_after_empty", func(t *testing.T) {
		w, keeper, r, p := iaRetirementReclaimFixture(t)
		_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 1)
		first, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(142, 21, 3), request)
		if err != nil || first.decision != receiptFresh {
			t.Fatal(first, err)
		}
		request.latestSequence = 3
		second, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(143, 21, 4), request)
		if err != nil || second.decision != receiptFresh || second.result.rows != 0 {
			t.Fatal(second, err)
		}
		var operation, incarnation []byte
		if err = w.db.QueryRowContext(t.Context(), `SELECT original_operation_id,command_incarnation FROM catalog_removal_progress`).Scan(&operation, &incarnation); err != nil {
			t.Fatal(err)
		}
		iaRetirementReclaimAppendReceipt(t, w, operation, incarnation, 5, "lib-gc")
		request.latestSequence = 4
		out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(144, 21, 5), request)
		if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
			t.Fatal("orphan receipt admitted as empty debt", out, err)
		}
	})
	t.Run("final_unit_mismatch", func(t *testing.T) {
		w, keeper, r, p := iaRetirementReclaimFixture(t)
		_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 1)
		// One receipt left after the first turn; its charge must equal the debt.
		if out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(145, 21, 3), request); err != nil || out.decision != receiptFresh {
			t.Fatal(out, err)
		}
		// A replacement receipt with two more measured bytes than the ledger can
		// never be the final unit it claims to be.
		wc008NativeExec(t, w, `UPDATE catalog_removal_receipts SET result_object_cursor='xx',retained_bytes=retained_bytes+2`)
		request.latestSequence = 3
		out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(146, 21, 4), request)
		if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
			t.Fatal("final unit mismatch admitted", out, err)
		}
	})
	for name, request := range map[string]iaRetirementReclaimRequest{
		"stale_sequence": {kind: "library", target: "lib-gc", libraryID: 900, libraryKey: "lib-gc", resolvedRoot: "/media/gc", latestSequence: 1},
		"other_root":     {kind: "library", target: "lib-gc", libraryID: 900, libraryKey: "lib-gc", resolvedRoot: "/media/other", latestSequence: 2},
		"other_target":   {kind: "library", target: "lib-other", libraryID: 900, libraryKey: "lib-other", resolvedRoot: "/media/gc", latestSequence: 2},
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, r, p := iaRetirementReclaimFixture(t)
			_, _, _ = iaRetirementReclaimSeed(t, w, keeper, p, 1)
			out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(147, 21, request.latestSequence+1), request)
			if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
				t.Fatal("wrong driver witness admitted", out, err)
			}
			if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 2 {
				t.Fatal("refusal retired a receipt", got)
			}
		})
	}
}

func TestIARetirementReclaimSourceOwner(t *testing.T) {
	w, keeper, r, p := iaRetirementReclaimFixture(t)
	iaRetirementBeginLibrarySeed(t, w, 901, "lib-gc-source", "/media/gc-source", "Library", 5)
	iaRetirementBeginSourceSeed(t, w, "lib-gc-source", "source-00", 3, "incarnation-00", "/media/gc-source", "identity-00")
	begin := iaRetirementReclaimRegistry(t, keeper, iaRetirementBeginDefinitions())
	identity := iaRetirementBeginIdentity(150, 151, 1)
	beginRequest := iaRetirementBeginSourceRequest("lib-gc-source", "source-00", 901, 5, 3, "/media/gc-source", "incarnation-00", "identity-00", true)
	ref := iaRetirementBeginPrepared(t, IARetireSource, identity, beginRequest)
	turn, out, err := iaRetirementBeginRun(t, w, begin, p, IARetireSource, ref, beginRequest)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("seed source retirement", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var operation, incarnation []byte
	var retained int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT original_operation_id,command_incarnation,receipt_retained_bytes FROM catalog_removal_progress WHERE kind='folder' AND target='source-00'`).Scan(&operation, &incarnation, &retained); err != nil {
		t.Fatal(err)
	}
	receipt := iaRetirementReceiptBytes("retire_step", "folder", "source-00", "retiring")
	marker := make([]byte, 16)
	marker[0] = 0x95
	wc008NativeExec(t, w, `INSERT INTO catalog_removal_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,
request_digest,command_kind,result_version,accepted_revision,applied_ms,result_kind,result_target,result_phase,
result_entity_kind,result_entity_cursor,result_object_cursor,result_walk_removed,result_walk_kept,result_state,
result_count,result_digest,retained_bytes)
VALUES(?,?,2,?,zeroblob(32),'retire_step',1,2,1002,'folder','source-00',1,0,0,'',0,0,'retiring',0,zeroblob(32),?)`,
		operation, incarnation, marker, receipt)
	wc008NativeExec(t, w, `UPDATE catalog_removal_progress SET last_command_sequence=2,acknowledged_command_sequence=1,retirement_state='complete',phase=5,
receipt_retained_count=2,receipt_retained_bytes=? WHERE kind='folder' AND target='source-00'`, retained+receipt)
	request := iaRetirementReclaimRequest{kind: "folder", target: "source-00", libraryID: 901, libraryKey: "lib-gc-source",
		resolvedRoot: "/media/gc-source", sourceIncarnation: "incarnation-00", sourceGeneration: 3, rootIdentity: "identity-00", latestSequence: 2}
	var got iaRetirementReclaimFacts
	reclaim := func(identity CommandIdentity, request iaRetirementReclaimRequest) (iaRetirementReclaimOutcome, error) {
		ref := iaRetirementReclaimReference(t, identity, request)
		turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireReclaim, action: iaEntryApply, reference: ref})
		out, err := reclaimIARetirement(t.Context(), turn, p, t.Context(), budget, ref, request)
		if err != nil {
			turn.transaction.rollback(nil)
			return out, err
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		return out, nil
	}
	// A library-shaped request cannot reclaim a folder driver.
	bad := iaRetirementReclaimRequest{kind: "library", target: "lib-gc-source", libraryID: 901,
		libraryKey: "lib-gc-source", resolvedRoot: "/media/gc-source", latestSequence: 2}
	if out, err := reclaim(iaRetirementBeginIdentity(152, 151, 3), bad); !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
		t.Fatal("folder driver admitted a library witness", out, err)
	}
	first, err := reclaim(iaRetirementBeginIdentity(152, 151, 3), request)
	if err != nil || first.decision != receiptFresh || first.result.removedKind != "header" || first.result.rows != 1 {
		t.Fatal("source receipt turn", first, err)
	}
	request.latestSequence = 3
	second, err := reclaim(iaRetirementBeginIdentity(153, 151, 4), request)
	if err != nil || second.decision != receiptFresh || !second.result.noNext || second.result.rows != 0 {
		t.Fatal("source terminal turn", second, err)
	}
	if got = iaRetirementReclaimReadRetired(t, w); got.rows != 0 || got.bytes != 0 {
		t.Fatal("source debt survived", got)
	}
	if n := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts WHERE result_target='source-00'`); n != 0 {
		t.Fatal("source receipts survived", n)
	}
}

func iaRetirementReclaimReadRetired(t *testing.T, w *executor) iaRetirementReclaimFacts {
	t.Helper()
	var f iaRetirementReclaimFacts
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,
receipt_retained_count,receipt_retained_bytes,receipt_reclaim_due_ms,reclaim_command_sequence,retirement_state,phase
FROM catalog_removal_progress WHERE kind='folder' AND target='source-00'`).Scan(&f.last, &f.acknowledged, &f.reclaimed, &f.rows, &f.bytes, &f.due, &f.slot, &f.state, &f.phase); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestIARetirementReclaimCurrentDenial(t *testing.T) {
	for name, mutation := range map[string]string{
		"owner_role":     `UPDATE direct_memberships SET role='member'`,
		"token_retired":  `UPDATE authorization_family_tokens SET retired=1`,
		"deny_fence":     `UPDATE identity_security_fences SET denied=1`,
		"stale_lifetime": `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, r, p := iaRetirementReclaimFixture(t)
			_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 2)
			wc008NativeExec(t, w, mutation)
			out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(160, 21, 4), request)
			if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("denied authority reached history", out, err)
			}
			if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 3 {
				t.Fatal("denial retired a receipt", got)
			}
		})
	}
	t.Run("forged_issuer", func(t *testing.T) {
		w, keeper, r, p := iaRetirementReclaimFixture(t)
		_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		out, err := iaRetirementReclaimRun(t, w, r, &fake, iaRetirementBeginIdentity(162, 21, 4), request)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 {
			t.Fatal("forged issuer admitted", out, err)
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, keeper, r, p := iaRetirementReclaimFixture(t)
		_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 2)
		ref := iaRetirementReclaimReference(t, iaRetirementBeginIdentity(163, 21, 4), request)
		turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireReclaim, action: iaEntryApply, reference: ref})
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := reclaimIARetirement(context.Background(), turn, p, cancelled, budget, ref, request)
		turn.transaction.rollback(nil)
		if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("cancelled lifetime released a result", out, err)
		}
		if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 3 {
			t.Fatal("cancelled lifetime retired a receipt", got)
		}
	})
}

func TestIARetirementReclaimLateRevocationAfterWrites(t *testing.T) {
	w, keeper, r, p := iaRetirementReclaimFixture(t)
	_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 2)
	wc008NativeExec(t, w, `CREATE TRIGGER ia_retirement_reclaim_late_revocation AFTER UPDATE ON catalog_removal_progress BEGIN UPDATE identity_security_fences SET denied=1; END`)
	ref := iaRetirementReclaimReference(t, iaRetirementBeginIdentity(164, 21, 4), request)
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireReclaim, action: iaEntryApply, reference: ref})
	out, err := reclaimIARetirement(t.Context(), turn, p, t.Context(), budget, ref, request)
	if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 {
		t.Fatal("late post-write recheck", out, err)
	}
	var deleted, slotted int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_removal_receipts WHERE command_sequence=1),(SELECT count(*) FROM catalog_removal_progress WHERE reclaim_command_sequence IS NOT NULL)`).Scan(&deleted, &slotted); err != nil || deleted != 0 || slotted != 1 {
		t.Fatal("effects not reached", deleted, slotted, err)
	}
	turn.transaction.rollback(nil)
	if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_receipts`); got != 3 {
		t.Fatal("rollback retained the receipt delete", got)
	}
	if got := iaRetirementReclaimCount(t, w, `SELECT count(*) FROM catalog_removal_progress WHERE reclaim_command_sequence IS NULL`); got != 1 {
		t.Fatal("rollback retained the slot", got)
	}
}

func TestIARetirementReclaimUnknownCommitFreshRead(t *testing.T) {
	w, keeper, r, p := iaRetirementReclaimFixture(t)
	_, _, request := iaRetirementReclaimSeed(t, w, keeper, p, 2)
	path := iaConfigureLibraryPath(t, w)
	ref := iaRetirementReclaimReference(t, iaRetirementBeginIdentity(165, 21, 4), request)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IARetireReclaim, action: iaEntryApply, reference: ref})
	if out, err := reclaimIARetirement(t.Context(), turn, p, t.Context(), budget, ref, request); err != nil || out.decision != receiptFresh {
		t.Fatal("fresh before commit", out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_removal_receipts WHERE command_sequence=1`, 0},
		{`SELECT count(*) FROM catalog_removal_progress WHERE last_command_sequence=4 AND acknowledged_command_sequence=4 AND receipt_reclaim_sequence=1`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IARetireReclaim, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

func TestIARetirementReclaimBoundedRequest(t *testing.T) {
	base := iaRetirementReclaimRequest{kind: "library", target: "lib-bounds", libraryID: 777, libraryKey: "lib-bounds",
		resolvedRoot: "/media/bounds", latestSequence: 1}
	for name, mutate := range map[string]func(*iaRetirementReclaimRequest){
		"unknown_kind":   func(q *iaRetirementReclaimRequest) { q.kind = "show" },
		"empty_target":   func(q *iaRetirementReclaimRequest) { q.target = "" },
		"empty_key":      func(q *iaRetirementReclaimRequest) { q.libraryKey = "" },
		"zero_library":   func(q *iaRetirementReclaimRequest) { q.libraryID = 0 },
		"long_target":    func(q *iaRetirementReclaimRequest) { q.target = strings.Repeat("t", 129) },
		"relative_root":  func(q *iaRetirementReclaimRequest) { q.resolvedRoot = "media/bounds" },
		"unclean_root":   func(q *iaRetirementReclaimRequest) { q.resolvedRoot = "/media/../bounds" },
		"zero_latest":    func(q *iaRetirementReclaimRequest) { q.latestSequence = 0 },
		"max_latest":     func(q *iaRetirementReclaimRequest) { q.latestSequence = 1<<63 - 1 },
		"library_source": func(q *iaRetirementReclaimRequest) { q.sourceIncarnation = "incarnation" },
		"library_target": func(q *iaRetirementReclaimRequest) { q.target = "other" },
		"folder_no_root": func(q *iaRetirementReclaimRequest) { q.kind = "folder" },
		"folder_zero_gen": func(q *iaRetirementReclaimRequest) {
			q.kind, q.sourceIncarnation, q.rootIdentity, q.sourceGeneration = "folder", "incarnation", "identity", 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			q := base
			mutate(&q)
			if _, err := newIARetirementReclaimCommand(iaRetirementBeginIdentity(170, 171, 2), q); !errors.Is(err, ErrInvalid) {
				t.Fatal("bounded request admitted", err)
			}
		})
	}
	if _, err := newIARetirementReclaimCommand(iaRetirementBeginIdentity(170, 171, 3), base); !errors.Is(err, ErrInvalid) {
		t.Fatal("sequence gap admitted", err)
	}
	t.Run("missing_driver", func(t *testing.T) {
		w, _, r, p := iaRetirementReclaimFixture(t)
		out, err := iaRetirementReclaimRun(t, w, r, p, iaRetirementBeginIdentity(170, 171, 2), base)
		if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
			t.Fatal("absent driver admitted", out, err)
		}
	})
}
