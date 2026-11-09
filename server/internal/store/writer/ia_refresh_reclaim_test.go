package writer

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// A synthetic accepted journal whose receipts and before-images are seeded
// directly, so the bounded quantum, the measured freed bytes and the fixed-slot
// replay are the only things under test. The statements under test are the real
// production ones; only the fixture rows are seeded.
func iaRefreshReclaimTick(t *testing.T, q iaRefreshRequest, refreshID, revision, sequence int64) iaRefreshRequest {
	t.Helper()
	q.purpose = IARefreshFinalize
	q.action = iaEntryApply
	q.key = iaRefreshKey{}
	q.row = nil
	q.refreshID = refreshID
	q.expectedRevision = revision
	q.reference.Identity.Sequence = sequence
	q.reference.Identity.OperationID[0] = byte(sequence)
	q.reference.Identity.OperationID[1] = byte(sequence >> 8)
	q.reference.Identity.OperationID[15] = 0xA3
	iaRefreshSign(t, &q)
	return q
}

func iaRefreshSeedUndoRow(t *testing.T, w *executor, refreshID int64, rec iaRefreshRecord) int64 {
	t.Helper()
	f, v := rec.refreshRecord()
	s := iaRefreshShapes[f]
	cols, marks := "refresh_id,old_exists,written_revision,write_digest", "?,?,?,?"
	args := []any{refreshID, int64(1), int64(1), make([]byte, 32)}
	for _, c := range s.columns {
		cols += "," + c.name
		marks += ",?"
	}
	args = append(args, v...)
	wc008NativeExec(t, w, "INSERT INTO catalog_info_undo_"+s.table+"("+cols+") VALUES("+marks+")", args...)
	k := iaRefreshKey{family: f, token: v[0].(string)}
	if f == 2 || f == 4 {
		k.index = v[1].(int64)
	}
	_, charge, err := iaRefreshImageDigest(k, iaRefreshImage{present: true, values: v})
	if err != nil {
		t.Fatal(err)
	}
	return charge
}

func iaRefreshSeedReceipt(t *testing.T, w *executor, refreshID, sequence int64, kind, state string) {
	t.Helper()
	operation := make([]byte, 16)
	operation[0], operation[15] = byte(sequence), 0x5C
	wc008NativeExec(t, w, `INSERT INTO catalog_info_refresh_receipts(refresh_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_state,after_family,after_key,result_rows,result_bytes,result_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		refreshID, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, sequence, operation, make([]byte, 32), kind, int64(1), int64(0), state, int64(0), "", int64(0), int64(0), make([]byte, 32))
}

func iaRefreshReceiptChargeFor(kind, state, key string) int64 {
	return iaRefreshReceiptCharge(kind, state, key)
}

// One accepted journal retires in bounded turns; each turn saves its result in
// the permanent fixed slot, the same original replays it, ACK retires it, and
// the journal's own rows/receipts are the only thing that ever decreases.
func TestIARefreshReclaimRetiresAcknowledgedJournal(t *testing.T) {
	w, r, p, q := iaRefreshFinishFixture(t)
	iaRefreshSeed(t, w, iaRefreshFact(10))
	iaRefreshSeed(t, w, iaRefreshStreamFixtureRow(1, "h264"))
	begin := iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{2, "refresh-asset", 1}, iaRefreshStreamFixtureRow(1, "hevc"))
	last := iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, last.result.refreshID)
	fin := iaRefreshFinalizeRequest(t, q, last)
	accepted := iaRefreshApply(t, w, r, p, fin)
	if accepted.result.state != "accepted" {
		t.Fatal(accepted)
	}
	iaRefreshAck(t, w, accepted.result.refreshID)
	reclaim := iaRefreshFinalizeRequest(t, fin, accepted)
	first := iaRefreshApply(t, w, r, p, reclaim)
	if first.result.state != "retired" || first.result.family != 7 || first.result.key != "" || first.result.rows != 1 {
		t.Fatalf("terminal cleanup: state=%q family=%d key=%q rows=%d bytes=%d", first.result.state, first.result.family, first.result.key, first.result.rows, first.result.bytes)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_streams WHERE refresh_id=?`, accepted.result.refreshID); got != 0 {
		t.Fatal("family before-images survived retirement", got)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, accepted.result.refreshID); got != 0 {
		t.Fatal("acknowledged receipts survived retirement", got)
	}
	if got := iaRefreshCount(t, w, `SELECT reclaim_terminal FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID); got != 1 {
		t.Fatal("fixed slot is not terminal", got)
	}
	var state string
	if err := w.db.QueryRow(`SELECT state FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID).Scan(&state); err != nil || state != "retired" {
		t.Fatal(state, err)
	}
	if used, retained := iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`), iaRefreshCount(t, w, `SELECT retained_bytes FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID); used != retained || first.result.bytes != used {
		t.Fatal("permanent slot charge is not the retained debt", used, retained, first.result.bytes)
	}
	// The cleanup turn appended no receipt: its exact result lives in the slot.
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts`); got != 0 {
		t.Fatal("cleanup appended a receipt", got)
	}
	replay := iaRefreshApply(t, w, r, p, reclaim)
	if replay.decision != receiptReplay || replay.result != first.result || replay.digest != first.digest {
		t.Fatal("fixed-slot replay changed", replay)
	}
	iaRefreshAck(t, w, first.result.refreshID)
	retired := iaRefreshApply(t, w, r, p, reclaim)
	if retired.decision != receiptRetired || retired.result != (iaRefreshResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal("ACKed cleanup disclosed its retained result", retired)
	}
}

// Retirement refuses before it deletes anything: an unacknowledged predecessor,
// a still-pending publication fence, or a newer active refresh for the asset.
func TestIARefreshReclaimRefusesUnackedOrPinnedJournal(t *testing.T) {
	for _, mode := range []string{"unacked", "fence_pending", "newer_active", "stale_slot"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q, last := iaRefreshFinishSeededJournal(t)
			fin := iaRefreshFinalizeRequest(t, q, last)
			accepted := iaRefreshApply(t, w, r, p, fin)
			reclaim := iaRefreshFinalizeRequest(t, fin, accepted)
			switch mode {
			case "fence_pending":
				iaRefreshAck(t, w, accepted.result.refreshID)
				wc008NativeExec(t, w, `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=41`)
			case "newer_active":
				iaRefreshAck(t, w, accepted.result.refreshID)
				wc008NativeExec(t, w, `INSERT INTO catalog_info_refresh_undo(change_id,original_run_id,command_incarnation,asset_id,asset_token,expected_epoch,input_revision,fingerprint,state,revision) VALUES(52,'refresh-run',?,41,'refresh-asset',2,1,zeroblob(32),'copying',1)`, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			case "stale_slot":
				// A saved slot that does not belong to the last admitted turn is
				// never overwritten, even after its own ACK.
				iaRefreshAck(t, w, accepted.result.refreshID)
				wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET reclaim_command_sequence=1,reclaim_operation_id=zeroblob(16),reclaim_request_digest=zeroblob(32),reclaim_result_digest=zeroblob(32),reclaim_applied_ms=0,reclaim_after_family=0,reclaim_after_key='',reclaim_removed_rows=0,reclaim_removed_bytes=0,reclaim_terminal=0,last_command_sequence=last_command_sequence+1,acknowledged_command_sequence=acknowledged_command_sequence+1 WHERE refresh_id=?`, accepted.result.refreshID)
				reclaim.reference.Identity.Sequence++
				reclaim.reference.Identity.OperationID[0]++
				iaRefreshSign(t, &reclaim)
				reclaim.expectedRevision = iaRefreshCount(t, w, `SELECT revision FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID)
			}
			turn, b := iaRefreshTurn(t, w, r, reclaim)
			out, err := runIARefresh(t.Context(), turn, reclaim, p, t.Context(), b)
			if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal("pinned retirement produced output", out, err)
			}
			if mode == "unacked" || mode == "stale_slot" {
				if !errors.Is(err, ErrResolveRequired) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			turn.transaction.rollback(err)
			if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, accepted.result.refreshID); got != 1 {
				t.Fatal("refused retirement deleted a before-image", got)
			}
			if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, accepted.result.refreshID); got != 3 {
				t.Fatal("refused retirement deleted a receipt", got)
			}
			if got := iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`); got != accepted.result.bytes {
				t.Fatal("refused retirement moved the budget", got)
			}
		})
	}
}

// Two turns are forced by seeding more than one quantum of acknowledged rows.
// Each turn reports exactly the encoded bytes it deleted and the budget moves by
// exactly that amount (plus the one retained slot charge), never on ACK alone.
func TestIARefreshReclaimFreesOnlyMeasuredBytes(t *testing.T) {
	w, r, p, q := iaRefreshHzFixture(t)
	var seeded int64
	rows := int64(1) // the BEGIN control row
	for index := int64(1); index <= 3; index++ {
		seeded += iaRefreshSeedUndoRow(t, w, q.refreshID, iaRefreshStreamFixtureRow(index, "h264"))
		rows++
	}
	for index := int64(1); index <= 2; index++ {
		seeded += iaRefreshSeedUndoRow(t, w, q.refreshID, iaRefreshChapterRow{AssetID: "refresh-asset", ChapterIndex: index, Title: "c", EndSeconds: 2})
		rows++
	}
	seeded += iaRefreshSeedUndoRow(t, w, q.refreshID, iaRefreshSubtitleRow{ID: "sub", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt"})
	rows++
	seeded += iaRefreshSeedUndoRow(t, w, q.refreshID, iaRefreshSidecarRow{ID: "sidecar", Path: "/media/sub.srt", Size: 1, Digest: "d"})
	rows++
	seeded += iaRefreshSeedUndoRow(t, w, q.refreshID, iaRefreshSubtitleFactRow{AssetID: "refresh-asset", Revision: 1, Status: "ready"})
	rows++
	// 65 acknowledged STEP receipts plus the BEGIN receipt = 66 headers.
	for sequence := int64(2); sequence <= 66; sequence++ {
		iaRefreshSeedReceipt(t, w, q.refreshID, sequence, "refresh_step", "copying")
		seeded += iaRefreshReceiptChargeFor("refresh_step", "copying", "")
		rows++
	}
	seeded += iaRefreshReceiptChargeFor("refresh_begin", "copying", "")
	wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET last_command_sequence=66,acknowledged_command_sequence=66,retained_rows=?,retained_bytes=?,state='accepted' WHERE refresh_id=?`, rows, seeded, q.refreshID)
	wc008NativeExec(t, w, `UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1`, seeded)
	wc008NativeExec(t, w, `UPDATE catalog_assets SET info_publication_pending=0 WHERE id=41`)
	revision := iaRefreshCount(t, w, `SELECT revision FROM catalog_info_refresh_undo WHERE refresh_id=?`, q.refreshID)

	first := iaRefreshReclaimTick(t, q, q.refreshID, revision, 67)
	out := iaRefreshApply(t, w, r, p, first)
	if out.result.rows != rows-64+1 {
		t.Fatal("first turn retained rows", out.result.rows)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, q.refreshID); got != 2 {
		t.Fatal("first turn did not delete exactly one quantum of receipts", got)
	}
	if got := iaRefreshCount(t, w, `SELECT reclaim_removed_bytes FROM catalog_info_refresh_undo WHERE refresh_id=?`, q.refreshID); got != iaRefreshReceiptChargeFor("refresh_begin", "copying", "")+63*iaRefreshReceiptChargeFor("refresh_step", "copying", "") {
		t.Fatal("reported receipt bytes are not the measured charge", got)
	}
	firstCharge := iaRefreshReceiptChargeFor("refresh_finalize", "accepted", out.result.key)
	if out.result.bytes <= 0 {
		t.Fatal(out)
	}
	if got := iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`); got != seeded-(iaRefreshReceiptChargeFor("refresh_begin", "copying", "")+63*iaRefreshReceiptChargeFor("refresh_step", "copying", ""))+firstCharge {
		t.Fatal("first turn budget", got)
	}
	if out.result.state != "accepted" {
		t.Fatal("first turn was not resumable", out)
	}
	iaRefreshAck(t, w, out.result.refreshID)
	second := iaRefreshReclaimTick(t, q, q.refreshID, revision, 68)
	last := iaRefreshApply(t, w, r, p, second)
	if last.result.state != "retired" || last.result.rows != 1 || last.result.family != 7 {
		t.Fatal(last)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, q.refreshID); got != 0 {
		t.Fatal("receipts survived the terminal turn", got)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_streams WHERE refresh_id=?`, q.refreshID); got != 0 {
		t.Fatal("before-images survived the terminal turn", got)
	}
	if used, retained := iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`), iaRefreshCount(t, w, `SELECT retained_bytes FROM catalog_info_refresh_undo WHERE refresh_id=?`, q.refreshID); used != retained || used != last.result.bytes {
		t.Fatal("terminal slot is not the whole remaining debt", used, retained, last.result.bytes)
	}
}

// The cleanup half is an accepting command too: an uncertain COMMIT is read back
// through a FRESH read-only connection, and no output is ever claimed.
func TestIARefreshReclaimUnknownCommitReadsBackFresh(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	fin := iaRefreshFinalizeRequest(t, q, last)
	accepted := iaRefreshApply(t, w, r, p, fin)
	iaRefreshAck(t, w, accepted.result.refreshID)
	reclaim := iaRefreshFinalizeRequest(t, fin, accepted)
	path := iaConfigureLibraryPath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, b := iaRefreshTurn(t, w, r, reclaim)
	out, err := runIARefresh(t.Context(), turn, reclaim, p, t.Context(), b)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown COMMIT was reported as success", result, err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var terminal, rows int
	if err = db.QueryRowContext(t.Context(), `SELECT reclaim_terminal,retained_rows FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID).Scan(&terminal, &rows); err != nil || terminal != 1 || rows != 1 {
		t.Fatal("fresh read lost the retired slot", terminal, rows, err)
	}
	var receipts int
	if err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, accepted.result.refreshID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("fresh read lost the reclaimed receipts", receipts, err)
	}
	if _, err = w.begin(t.Context(), IARefreshFinalize, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown commit was reused", err)
	}
}

// A republish after accept moves the asset's info_epoch. Retirement owns no
// canonical row, so the epoch is not part of the journal's identity: the bounded
// walk must resume after the move, free exactly the debt it charged, and settle
// on a slot charged only for its own turns.
func TestIARefreshReclaimRetiresAfterRepublish(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	fin := iaRefreshFinalizeRequest(t, q, last)
	accepted := iaRefreshApply(t, w, r, p, fin)
	iaRefreshAck(t, w, accepted.result.refreshID)
	// Extend the acknowledged journal past one quantum so retirement needs two
	// bounded turns. The first three receipts are the BEGIN, STEP and finalize
	// headers the real accept wrote.
	rows := iaRefreshCount(t, w, `SELECT retained_rows FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID)
	bytes := iaRefreshCount(t, w, `SELECT retained_bytes FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID)
	for sequence := int64(4); sequence <= 66; sequence++ {
		iaRefreshSeedReceipt(t, w, accepted.result.refreshID, sequence, "refresh_step", "copying")
		rows++
		bytes += iaRefreshReceiptChargeFor("refresh_step", "copying", "")
	}
	wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET last_command_sequence=66,acknowledged_command_sequence=66,retained_rows=?,retained_bytes=? WHERE refresh_id=?`, rows, bytes, accepted.result.refreshID)
	wc008NativeExec(t, w, `UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1`, bytes)
	// The republish: the accepted epoch moves on after ACK, with the canonical
	// revisions that a completed publication would carry. Reclaim neither
	// relabels nor re-reads these rows, so the journal stays owned by this asset.
	wc008NativeExec(t, w, `UPDATE catalog_assets SET info_epoch=info_epoch+1,playback_origin_revision=playback_origin_revision+1,prepared_source_revision=prepared_source_revision+1,info_invalidation_revision=info_invalidation_revision+1 WHERE id=41`)
	revision := iaRefreshCount(t, w, `SELECT revision FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID)

	first := iaRefreshApply(t, w, r, p, iaRefreshReclaimTick(t, q, accepted.result.refreshID, revision, 67))
	if first.result.state != "accepted" || first.result.family != 0 || first.result.key != "64" {
		t.Fatalf("first turn: %+v", first.result)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, accepted.result.refreshID); got != 2 {
		t.Fatal("first turn did not delete exactly one quantum of receipts", got)
	}
	iaRefreshAck(t, w, first.result.refreshID)
	done := iaRefreshApply(t, w, r, p, iaRefreshReclaimTick(t, q, accepted.result.refreshID, revision, 68))
	if done.result.state != "retired" || done.result.family != 7 || done.result.rows != 1 {
		t.Fatalf("terminal turn: %+v", done.result)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, accepted.result.refreshID); got != 0 {
		t.Fatal("receipts survived the republished retirement", got)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_streams WHERE refresh_id=?`, accepted.result.refreshID); got != 0 {
		t.Fatal("before-images survived the republished retirement", got)
	}
	// Every charged row was freed, so the final retained debt is exactly the two
	// turn charges: 186 for the "accepted"/"64" intermediate slot and 183 for the
	// "retired" slot. The intermediate charge is never refunded (reviewer note).
	wantBytes := iaRefreshReceiptChargeFor("refresh_finalize", "accepted", "64") + iaRefreshReceiptChargeFor("refresh_finalize", "retired", "")
	if wantBytes != 369 {
		t.Fatal("pinned retirement charge moved", wantBytes)
	}
	if used, retained := iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`), iaRefreshCount(t, w, `SELECT retained_bytes FROM catalog_info_refresh_undo WHERE refresh_id=?`, accepted.result.refreshID); used != wantBytes || retained != wantBytes || done.result.bytes != wantBytes {
		t.Fatal("republished retirement free/exact bytes", used, retained, done.result.bytes, wantBytes)
	}
	// The replay of the terminal turn still reads the fixed slot, not the asset.
	replay := iaRefreshApply(t, w, r, p, iaRefreshReclaimTick(t, q, accepted.result.refreshID, revision, 68))
	if replay.decision != receiptReplay || replay.result != done.result {
		t.Fatalf("republished retirement replay changed: %+v", replay)
	}
}

// Dropping the epoch pin must not drop the journal's asset/token/library binding:
// a retirement that names another asset, token or library is refused before any
// row is deleted.
func TestIARefreshReclaimRefusesForeignAssetBinding(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	fin := iaRefreshFinalizeRequest(t, q, last)
	accepted := iaRefreshApply(t, w, r, p, fin)
	iaRefreshAck(t, w, accepted.result.refreshID)
	// A second real asset exists, so a refusal cannot be explained by an absent
	// row.
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,info_epoch) VALUES(42,'other-asset',7,'/media/other',42,11,'','','',0,0,0,2)`)
	for _, mode := range []string{"asset", "token", "library"} {
		t.Run(mode, func(t *testing.T) {
			reclaim := iaRefreshFinalizeRequest(t, fin, accepted)
			switch mode {
			case "asset":
				reclaim.scope.assetID = 42
			case "token":
				reclaim.scope.token = "other-asset"
			case "library":
				reclaim.scope.libraryID = 8
			}
			iaRefreshSign(t, &reclaim)
			turn, b := iaRefreshTurn(t, w, r, reclaim)
			out, err := runIARefresh(t.Context(), turn, reclaim, p, t.Context(), b)
			if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("foreign binding produced output", out, err)
			}
			turn.transaction.rollback(err)
			if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, accepted.result.refreshID); got != 1 {
				t.Fatal("refused retirement deleted a before-image", got)
			}
			if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, accepted.result.refreshID); got != 3 {
				t.Fatal("refused retirement deleted a receipt", got)
			}
			if got := iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`); got != accepted.result.bytes {
				t.Fatal("refused retirement moved the budget", got)
			}
		})
	}
}

// A republish that is still fenced (a new publication pending) is the one asset
// state that still pins retirement: the undo rows may be a live writer's data.
func TestIARefreshReclaimRefusesPendingFenceAfterRepublish(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	fin := iaRefreshFinalizeRequest(t, q, last)
	accepted := iaRefreshApply(t, w, r, p, fin)
	iaRefreshAck(t, w, accepted.result.refreshID)
	wc008NativeExec(t, w, `UPDATE catalog_assets SET info_epoch=info_epoch+1,info_publication_pending=1 WHERE id=41`)
	reclaim := iaRefreshFinalizeRequest(t, fin, accepted)
	turn, b := iaRefreshTurn(t, w, r, reclaim)
	out, err := runIARefresh(t.Context(), turn, reclaim, p, t.Context(), b)
	if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("fenced republished asset produced output", out, err)
	}
	turn.transaction.rollback(err)
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=?`, accepted.result.refreshID); got != 3 {
		t.Fatal("fenced retirement deleted a receipt", got)
	}
}

// iaRefreshHzFixture is the IA20 fixture with a real BEGIN journal and no steps,
// so a test can seed an exact acknowledged debt and drive the cleanup quantum.
func iaRefreshHzFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRefreshRequest) {
	t.Helper()
	w, r, p, q := iaRefreshFinishFixture(t)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	q.expectedRevision = begin.result.revision
	return w, r, p, q
}
