package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Full current canonical fixture, plus the EXACT adopted C019 journal fragment
// until C embeds. No Capture/qualification receipt is minted from this draft.
// Current-owner security producers are reused from the already-censused IA05
// fixture; this file adds no identity/session producer or authority shortcut.
func iaRefreshFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRefreshRequest) {
	t.Helper()
	w, keeper, p := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-refresh-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(ddl)
	if hex.EncodeToString(sum[:]) != "f8dd6653780c7cf8d134b8714710cb4fbf9fbe19fce263d84bb7b5b26c400c36" {
		t.Fatal("adopted C019 fragment changed")
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('refresh-library','Refresh','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'refresh-library','/media')`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES('refresh-source','refresh-library','/media','/media','root-v1','root:1',1,1)`,
		`INSERT INTO library_scan_policies(library_id,revision) VALUES('refresh-library',1)`,
		`INSERT INTO jobs(id,library_id,status,created_at) VALUES('refresh-run','refresh-library','running','2026-11-08T00:00:00Z')`,
		`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,info_epoch) VALUES(41,'refresh-asset',7,'/media/file',42,11,'','','',0,0,0,2)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,seen_job) VALUES('refresh-object','refresh-source','refresh-asset','root-v1','file','object-v1','{}',42,11,'refresh-run')`,
		`INSERT INTO catalog_info_input_budget(singleton,used_bytes) VALUES(1,0)`,
	} {
		wc008NativeExec(t, w, q)
	}
	s := iaRefreshScope{size: 42, modifiedNS: 11, sourceID: "refresh-source", sourceGeneration: 1, rootIncarnation: "root-v1", objectID: "refresh-object", objectRevision: "object-v1", policyRevision: 1, changeID: 51, assetID: 41, libraryID: 7, epoch: 2, inputRevision: 1, token: "refresh-asset", runID: "refresh-run", libraryKey: "refresh-library", fingerprint: [32]byte{7}, changeRevision: 1, leaseOwner: 1, leaseGeneration: 1, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli()}
	s.fingerprint, err = iaRefreshInputFingerprint(s)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode,command_incarnation) VALUES('refresh-run',7,'refresh-library','/media',0,?,?,'refresh-source',1,'root-v1','root:1',1,'incremental',?)`, make([]byte, 16), make([]byte, 32), []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	wc008NativeExec(t, w, `INSERT INTO inventory_source_active(source_id,job_id) VALUES('refresh-source','refresh-run')`)
	wc008NativeExec(t, w, `INSERT INTO catalog_info_changes(id,asset_id,asset_token,slot,expected_epoch,input_revision,family_mask,fingerprint,created_ms,job_lease_owner,job_lease_generation,job_lease_expires_ms) VALUES(51,41,'refresh-asset',1,2,1,511,?,0,1,1,?)`, s.fingerprint[:], s.leaseExpiresMS)
	wc008NativeExec(t, w, `INSERT INTO catalog_info_input_witnesses(change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,fingerprint) VALUES(51,0,0,'refresh-run','refresh-source',1,'root-v1','refresh-object','object-v1',1,?)`, s.fingerprint[:])
	r, err := stmttest.New(t, iaRefreshDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	q := iaRefreshRequest{purpose: IARefreshBegin, action: iaEntryApply, scope: s, reference: ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}}}
	iaRefreshSign(t, &q)
	return w, &iaEntryRegistry{r, keeper}, p, q
}
func iaRefreshSign(t *testing.T, q *iaRefreshRequest) {
	t.Helper()
	d, err := iaRefreshRequestDigest(*q)
	if err != nil {
		t.Fatal(err)
	}
	q.reference.RequestDigest = d
}
func iaRefreshTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaRefreshRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: q.purpose})
}
func iaRefreshApply(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaRefreshRequest) iaRefreshOutcome {
	t.Helper()
	turn, b := iaRefreshTurn(t, w, r, q)
	out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
	if err != nil {
		turn.transaction.rollback(err)
		t.Fatal("refresh", q.purpose, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return out
}

// ACK setup is fixture-only. IA23 owns the fixed reclaim protocol; this never
// refunds physical debt and is not a production ACK implementation.
func iaRefreshNext(t *testing.T, w *executor, q iaRefreshRequest, last iaRefreshOutcome, purpose CommandID, key iaRefreshKey, row iaRefreshRecord) iaRefreshRequest {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET acknowledged_command_sequence=last_command_sequence WHERE refresh_id=?`, last.result.refreshID)
	q.refreshID = last.result.refreshID
	q.expectedRevision = last.result.revision
	q.purpose = purpose
	q.key = key
	q.row = row
	q.reference.Identity.Sequence++
	q.reference.Identity.OperationID[0]++
	q.action = iaEntryApply
	iaRefreshSign(t, &q)
	return q
}
func iaRefreshCount(t *testing.T, w *executor, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func iaRefreshSeed(t *testing.T, w *executor, r iaRefreshRecord) {
	t.Helper()
	f, v := r.refreshRecord()
	s := iaRefreshShapes[f]
	var cols, marks []string
	for _, c := range s.columns {
		cols = append(cols, c.name)
		marks = append(marks, "?")
	}
	wc008NativeExec(t, w, "INSERT INTO "+s.table+"("+strings.Join(cols, ",")+") VALUES("+strings.Join(marks, ",")+")", v...)
}
func iaRefreshFact(rev int64) iaRefreshStreamFactRow {
	return iaRefreshStreamFactRow{"refresh-asset", rev, 42, 11, "old", 1}
}

func TestIARefreshFirstImageAndRestore(t *testing.T) {
	w, r, p, q := iaRefreshFixture(t)
	iaRefreshSeed(t, w, iaRefreshFact(10))
	begin := iaRefreshApply(t, w, r, p, q)
	if begin.result.refreshID <= 0 || begin.result.state != "copying" || iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 1 {
		t.Fatal(begin)
	}
	key := iaRefreshKey{1, "refresh-asset", 0}
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, iaRefreshFact(11))
	step := iaRefreshApply(t, w, r, p, q)
	q = iaRefreshNext(t, w, q, step, IARefreshStep, key, iaRefreshFact(12))
	step = iaRefreshApply(t, w, r, p, q)
	if iaRefreshCount(t, w, `SELECT revision FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, step.result.refreshID) != 10 || iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts`) != 1 {
		t.Fatal("first old image replaced")
	}
	q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
	restored := iaRefreshApply(t, w, r, p, q)
	if restored.result.state != "restoring" || iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts`) != 10 || iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 1 || iaRefreshCount(t, w, `SELECT playback_origin_revision FROM catalog_assets WHERE id=41`) != 4 {
		t.Fatal("restore/fence/MP", restored)
	}
	if iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`) != (restored.result.bytes) || restored.result.rows != 5 {
		t.Fatal("whole custody debt", restored)
	}
}
func TestIARefreshAbsentAndEmptyAreDifferent(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[present], func(t *testing.T) {
			w, r, p, q := iaRefreshFixture(t)
			if present {
				iaRefreshSeed(t, w, iaRefreshFact(10))
			}
			begin := iaRefreshApply(t, w, r, p, q)
			key := iaRefreshKey{1, "refresh-asset", 0}
			after := iaRefreshFact(11)
			after.Fingerprint = ""
			q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, after)
			step := iaRefreshApply(t, w, r, p, q)
			q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
			iaRefreshApply(t, w, r, p, q)
			want := int64(0)
			if present {
				want = 1
			}
			if iaRefreshCount(t, w, `SELECT count(*) FROM asset_stream_facts`) != want || iaRefreshCount(t, w, `SELECT old_exists FROM catalog_info_undo_asset_stream_facts`) != want {
				t.Fatal("presence lost")
			}
		})
	}
}
func TestIARefreshDriftRestoreAndReceiptReplay(t *testing.T) {
	w, r, p, q := iaRefreshFixture(t)
	iaRefreshSeed(t, w, iaRefreshFact(10))
	beginQ := q
	begin := iaRefreshApply(t, w, r, p, q)
	key := iaRefreshKey{1, "refresh-asset", 0}
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, iaRefreshFact(11))
	stepQ := q
	step := iaRefreshApply(t, w, r, p, q)
	wc008NativeExec(t, w, `UPDATE catalog_assets SET size=99,modified_ns=88 WHERE id=41`)
	wc008NativeExec(t, w, `UPDATE inventory_objects SET revision='object-v2' WHERE id='refresh-object'`)
	replay := iaRefreshApply(t, w, r, p, stepQ)
	if replay.decision != receiptReplay || replay.result != step.result || replay.digest != step.digest {
		t.Fatal("original replay changed", replay)
	}
	q = iaRefreshNext(t, w, q, step, IARefreshStep, key, iaRefreshFact(12))
	turn, b := iaRefreshTurn(t, w, r, q)
	out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
		t.Fatal("drift copied", out, err)
	}
	turn.transaction.rollback(err)
	q.purpose = IARefreshRollback
	q.row = nil
	iaRefreshSign(t, &q)
	iaRefreshApply(t, w, r, p, q)
	if iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts`) != 10 || iaRefreshCount(t, w, `SELECT size FROM catalog_assets WHERE id=41`) != 99 {
		t.Fatal("source stat was restored/spliced")
	}
	// Original begin remains Retired after ACK, even with its header retained.
	retired := iaRefreshApply(t, w, r, p, beginQ)
	if retired.decision != receiptRetired || retired.result != (iaRefreshResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired)
	}
}
func TestIARefreshNewerPublicationAndScopeGuards(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE asset_stream_facts SET revision=99 WHERE asset_id='refresh-asset'`,
		`UPDATE asset_stream_facts SET fingerprint='later' WHERE asset_id='refresh-asset'`,
		`UPDATE catalog_assets SET info_epoch=3 WHERE id=41`,
		`UPDATE catalog_assets SET info_publication_pending=0 WHERE id=41`,
		`UPDATE library_sources SET enabled=0 WHERE id='refresh-source'`,
		`UPDATE catalog_info_changes SET job_lease_generation=2 WHERE id=51`,
		`UPDATE catalog_info_changes SET job_lease_expires_ms=1 WHERE id=51`,
		`UPDATE catalog_info_changes SET fingerprint=zeroblob(32) WHERE id=51`,
		`UPDATE catalog_info_input_witnesses SET object_id='replacement' WHERE change_id=51`,
	} {
		t.Run(mutation, func(t *testing.T) {
			w, r, p, q := iaRefreshFixture(t)
			iaRefreshSeed(t, w, iaRefreshFact(10))
			begin := iaRefreshApply(t, w, r, p, q)
			key := iaRefreshKey{1, "refresh-asset", 0}
			q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, iaRefreshFact(11))
			step := iaRefreshApply(t, w, r, p, q)
			// object_id is an immutable witness but not an FK; rollback may ignore the
			// changed object's mutable revision, never an altered input identity.
			wc008NativeExec(t, w, mutation)
			q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
			turn, b := iaRefreshTurn(t, w, r, q)
			out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
			if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal("guard bypass", out, err)
			}
			turn.transaction.rollback(err)
		})
	}
}
func TestIARefreshCaptureAndMutationRollbackTogether(t *testing.T) {
	for _, point := range []string{"capture", "write", "receipt"} {
		t.Run(point, func(t *testing.T) {
			w, r, p, q := iaRefreshFixture(t)
			iaRefreshSeed(t, w, iaRefreshFact(10))
			begin := iaRefreshApply(t, w, r, p, q)
			switch point {
			case "capture":
				wc008NativeExec(t, w, `CREATE TRIGGER fail_refresh_capture AFTER INSERT ON catalog_info_undo_asset_stream_facts BEGIN SELECT RAISE(ABORT,'capture fault'); END`)
			case "write":
				wc008NativeExec(t, w, `CREATE TRIGGER fail_refresh_write AFTER UPDATE ON asset_stream_facts BEGIN SELECT RAISE(ABORT,'write fault'); END`)
			case "receipt":
				wc008NativeExec(t, w, `CREATE TRIGGER fail_refresh_receipt AFTER INSERT ON catalog_info_refresh_receipts BEGIN SELECT RAISE(ABORT,'receipt fault'); END`)
			}
			key := iaRefreshKey{1, "refresh-asset", 0}
			q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, iaRefreshFact(11))
			turn, b := iaRefreshTurn(t, w, r, q)
			out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
			if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal(out, err)
			}
			turn.transaction.rollback(err)
			if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts`) != 0 || iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts`) != 10 || iaRefreshCount(t, w, `SELECT playback_origin_revision FROM catalog_assets WHERE id=41`) != 1 || iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`) != begin.result.bytes {
				t.Fatal("partial capture/write/debt")
			}
		})
	}
}
func TestIARefreshUnknownCommitDoesNotAuthorizeNewOriginal(t *testing.T) {
	w, r, p, q := iaRefreshFixture(t)
	path := iaConfigureLibraryPath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, b := iaRefreshTurn(t, w, r, q)
	out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_refresh_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = w.begin(t.Context(), IARefreshBegin, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown commit was reused", err)
	}
}

func TestIARefreshSevenTypedFamiliesRoundTrip(t *testing.T) {
	text := ""
	zero := int64(0)
	rev := int64(7)
	records := []iaRefreshRecord{
		iaRefreshFact(10),
		iaRefreshStreamRow{AssetID: "refresh-asset", StreamIndex: 1, Type: "video", Codec: "h264", Title: "", HasAssetStreamBreadth: 1, BreadthAudioDescription: &zero, BreadthAudioVariant: &text, BreadthChromaSubsampling: &text, BreadthFpsNumerator: &zero, BreadthFpsDenominator: &zero, BreadthFrameRateMode: &text, BreadthInterlaceAnalysis: &text, BreadthTelecine: &zero, BreadthDoviRpu: &zero, BreadthDoviBaseLayer: &zero, BreadthDoviMetadataKnown: &zero, BreadthDoviEnhancementType: &text, BreadthDoviCmVersion: &text, BreadthPlaybackFramesRevision: &text, BreadthPacketPeakRevision: &text},
		iaRefreshChapterFactRow{AssetID: "refresh-asset", Revision: 10, Size: 42, ModifiedNS: 11, Status: "ready", Fingerprint: ""},
		iaRefreshChapterRow{AssetID: "refresh-asset", ChapterIndex: 1, Title: "", StartSeconds: 0, EndSeconds: 2.5},
		iaRefreshSubtitleFactRow{AssetID: "refresh-asset", Revision: 10, Size: 42, ModifiedNS: 11, Status: "ready", Fingerprint: "", HasSubtitleInventoryEvidence: 1, InventoryRevision: &rev, InventoryEvidence: &text},
		iaRefreshSidecarRow{ID: "sidecar", Path: "/media/file.srt", Size: 1, ModifiedNS: 11, Digest: ""},
		iaRefreshSubtitleRow{ID: "subtitle", AssetID: "refresh-asset", Origin: "embedded", Locator: "1", StreamIndex: 1, Format: "srt", Title: ""},
	}
	for _, record := range records {
		f, v := record.refreshRecord()
		t.Run(iaRefreshShapes[f].table, func(t *testing.T) {
			for _, present := range []bool{true, false} {
				t.Run(map[bool]string{true: "present_empty_fields", false: "absent"}[present], func(t *testing.T) {
					w, r, p, q := iaRefreshFixture(t)
					switch f {
					case 2:
						iaRefreshSeed(t, w, iaRefreshFact(10))
					case 4:
						iaRefreshSeed(t, w, records[2])
					case 7:
						iaRefreshSeed(t, w, records[4])
					}
					if present {
						iaRefreshSeed(t, w, record)
					}
					begin := iaRefreshApply(t, w, r, p, q)
					key := iaRefreshKey{family: f, token: v[0].(string)}
					if f == 2 || f == 4 {
						key.index = v[1].(int64)
					}
					// Delete existing rows; create absent rows, including folded breadth/evidence.
					var replacement iaRefreshRecord
					if !present {
						replacement = record
					}
					q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, replacement)
					step := iaRefreshApply(t, w, r, p, q)
					q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
					iaRefreshApply(t, w, r, p, q)
					expected := iaRefreshAbsent(key)
					if present {
						expected = iaRefreshImage{true, v}
					}
					// Read exact returned SQLite columns through the private charged row reader.
					q.reference.Identity.Sequence++
					q.reference.Identity.OperationID[0]++
					q.expectedRevision++
					iaRefreshSign(t, &q)
					wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET acknowledged_command_sequence=last_command_sequence`)
					turn, b := iaRefreshTurn(t, w, r, q)
					entry, err := enterIARefresh(t.Context(), turn, q, p, t.Context(), b)
					if err != nil {
						t.Fatal(err)
					}
					got, _, _, _, err := iaRefreshReadImage(t.Context(), turn, entry, false)
					if err != nil || !reflect.DeepEqual(got, expected) {
						t.Fatal("complete before-image lost", f, got, expected, err)
					}
					turn.transaction.rollback(nil)
				})
			}
		})
	}
}
func TestIARefreshSharedSidecarAndCascadeVeto(t *testing.T) {
	for _, mode := range []string{"shared", "referenced_delete", "later_digest", "child_cascade"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaRefreshFixture(t)
			iaRefreshSeed(t, w, iaRefreshSubtitleFactRow{AssetID: "refresh-asset", Revision: 1, Status: "ready"})
			sidecar := iaRefreshSidecarRow{ID: "sidecar", Path: "/media/sub.srt", Digest: "old"}
			iaRefreshSeed(t, w, sidecar)
			if mode == "shared" {
				wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(42,'other',7,'/media/other',1,1,'','','',0,0,0)`)
				iaRefreshSeed(t, w, iaRefreshSubtitleFactRow{AssetID: "other", Revision: 1, Status: "ready"})
			}
			asset := "refresh-asset"
			if mode == "shared" {
				asset = "other"
			}
			iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub", AssetID: asset, Origin: "sidecar", Locator: "sub", StreamIndex: -1, SidecarID: &sidecar.ID, Format: "srt"})
			begin := iaRefreshApply(t, w, r, p, q)
			key := iaRefreshKey{6, "sidecar", 0}
			after := sidecar
			after.Digest = "written"
			var replacement iaRefreshRecord = after
			if mode == "referenced_delete" {
				replacement = nil
			}
			if mode == "child_cascade" {
				key = iaRefreshKey{5, "refresh-asset", 0}
				replacement = nil
			}
			q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, replacement)
			if mode == "later_digest" {
				step := iaRefreshApply(t, w, r, p, q)
				wc008NativeExec(t, w, `UPDATE subtitle_sidecars SET digest='newer' WHERE id='sidecar'`)
				q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
			}
			turn, b := iaRefreshTurn(t, w, r, q)
			out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal("unsafe shared row change", out, err)
			}
			turn.transaction.rollback(err)
			if iaRefreshCount(t, w, `SELECT count(*) FROM asset_subtitles WHERE id='sub'`) != 1 {
				t.Fatal("unlinked referenced sidecar/uncaptured child")
			}
			if mode == "later_digest" {
				var digest string
				if err = w.db.QueryRow(`SELECT digest FROM subtitle_sidecars WHERE id='sidecar'`).Scan(&digest); err != nil || digest != "newer" {
					t.Fatal(digest, err)
				}
			}
		})
	}
}

func TestIARefreshLateSourceDenialRollsBackWholeTurn(t *testing.T) {
	for _, table := range []string{"catalog_info_undo_asset_stream_facts", "asset_stream_facts", "catalog_info_refresh_receipts"} {
		t.Run(table, func(t *testing.T) {
			w, r, p, q := iaRefreshFixture(t)
			iaRefreshSeed(t, w, iaRefreshFact(10))
			begin := iaRefreshApply(t, w, r, p, q)
			op := "INSERT"
			if table == "asset_stream_facts" {
				op = "UPDATE"
			}
			wc008NativeExec(t, w, "CREATE TRIGGER late_source_deny AFTER "+op+" ON "+table+" BEGIN UPDATE library_sources SET enabled=0 WHERE id='refresh-source'; END")
			q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{1, "refresh-asset", 0}, iaRefreshFact(11))
			turn, b := iaRefreshTurn(t, w, r, q)
			out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal("late denial was masked", out, err)
			}
			turn.transaction.rollback(err)
			if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts`) != 0 || iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts`) != 10 || iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`) != begin.result.bytes {
				t.Fatal("late failure leaked mutation")
			}
		})
	}
}
func TestIARefreshCursorIsExactAndReversible(t *testing.T) {
	for _, k := range []iaRefreshKey{{1, "asset", 0}, {2, "bytes:\x00é", 123}, {4, "chapter", 9223372036854775807}, {6, "sidecar", 0}, {7, "subtitle", 0}} {
		c := k.cursor()
		got, err := iaRefreshDecodeCursor(k.family, c)
		if err != nil || got != k {
			t.Fatal(k, c, got, err)
		}
		if _, err = iaRefreshDecodeCursor(k.family, c+"0"); err == nil {
			t.Fatal("noncanonical cursor")
		}
	}
}

func TestIARefreshGuardsRefuseUnfundedOrMisboundTurns(t *testing.T) {
	for _, mode := range []string{"request_digest", "memory_under", "memory_over", "synchronous", "expired", "wrong_owner", "wrong_scope", "resolve_missing", "finalize_closed"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaRefreshFixture(t)
			if mode == "resolve_missing" {
				q.action = iaEntryResolve
			}
			if mode == "request_digest" {
				q.reference.RequestDigest[0] ^= 1
			}
			if mode == "wrong_scope" {
				q.scope.libraryKey = "other"
				iaRefreshSign(t, &q)
			}
			if mode == "wrong_owner" {
				p.witness.issuer.memberIncarnation = strings.Repeat("x", 16)
			}
			if mode == "finalize_closed" {
				q.purpose = IARefreshFinalize
				iaRefreshSign(t, &q)
			}
			turn, b := iaRefreshTurn(t, w, r, q)
			if mode == "memory_under" {
				b.memory = 1
			}
			if mode == "memory_over" {
				b.memory++
			}
			if mode == "synchronous" {
				turn.transaction.settings.Synchronous = int(sqliteconn.Full)
			}
			if mode == "expired" {
				p.tokenExpiry = w.now().Add(-time.Second)
			}
			out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
			if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal(mode, out, err)
			}
			turn.transaction.rollback(err)
			if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_undo`) != 0 || iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 0 {
				t.Fatal("failed admission wrote custody")
			}
		})
	}
}
func TestIARefreshFrozenShapesIncludeEveryNativeColumn(t *testing.T) {
	w, _, _, _ := iaRefreshFixture(t)
	for f := 1; f <= 7; f++ {
		s := iaRefreshShapes[f]
		rows, err := w.db.Query(`PRAGMA table_xinfo(` + s.table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var cid, nn, pk, hidden int64
			var name, kind string
			var defaultValue any
			if err = rows.Scan(&cid, &name, &kind, &nn, &defaultValue, &pk, &hidden); err != nil {
				t.Fatal(err)
			}
			if n >= len(s.columns) || s.columns[n].name != name || s.columns[n].kind != kind || hidden != 0 {
				t.Fatal("refresh would omit/retype a native before-image column", s.table, name)
			}
			n++
		}
		rows.Close()
		if n != len(s.columns) {
			t.Fatal(s.table, n)
		}
	}
}

func TestIARefreshCannotSpliceRetryIntoOldInput(t *testing.T) {
	w, r, p, q := iaRefreshFixture(t)
	begin := iaRefreshApply(t, w, r, p, q)
	// Model only IA23's future terminal boundary; this core never releases it.
	wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET state='restored' WHERE refresh_id=?`, begin.result.refreshID)
	wc008NativeExec(t, w, `UPDATE catalog_assets SET info_publication_pending=0 WHERE id=41`)
	q.reference.Identity.Incarnation[0]++
	q.reference.Identity.OperationID[0]++
	iaRefreshSign(t, &q)
	turn, b := iaRefreshTurn(t, w, r, q)
	out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
		t.Fatal("same input reused under a new incarnation", out, err)
	}
	turn.transaction.rollback(err)
	if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_undo`) != 1 {
		t.Fatal("retry allocated another journal for old input")
	}
}

func TestIARefreshSequenceConflictResolveAndRetiredRedaction(t *testing.T) {
	w, r, p, q := iaRefreshFixture(t)
	iaRefreshSeed(t, w, iaRefreshFact(10))
	begin := iaRefreshApply(t, w, r, p, q)
	key := iaRefreshKey{1, "refresh-asset", 0}
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, iaRefreshFact(11))
	pending := q
	pending.action = iaEntryResolve
	turn, b := iaRefreshTurn(t, w, r, pending)
	out, err := runIARefresh(t.Context(), turn, pending, p, t.Context(), b)
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
		t.Fatal("resolve executed fresh", out, err)
	}
	turn.transaction.rollback(err)
	reused := q
	reused.reference.Identity.OperationID[0]--
	iaRefreshSign(t, &reused)
	turn, b = iaRefreshTurn(t, w, r, reused)
	out, err = runIARefresh(t.Context(), turn, reused, p, t.Context(), b)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
		t.Fatal("operation id reused", out, err)
	}
	turn.transaction.rollback(err)
	if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts`) != 0 || iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts`) != 10 {
		t.Fatal("UNIQUE failure left effects")
	}
	step := iaRefreshApply(t, w, r, p, q)
	wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET acknowledged_command_sequence=last_command_sequence`)
	conflict := q
	conflict.row = iaRefreshFact(99)
	iaRefreshSign(t, &conflict)
	turn, b = iaRefreshTurn(t, w, r, conflict)
	out, err = runIARefresh(t.Context(), turn, conflict, p, t.Context(), b)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
		t.Fatal("retained ACK conflict hidden", out, err)
	}
	turn.transaction.rollback(err)
	retired := iaRefreshApply(t, w, r, p, q)
	if retired.decision != receiptRetired || retired.result != (iaRefreshResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal("retired receipt payload disclosed", step, retired)
	}
}

func TestIARefreshCurrentOwnerRecheckedAfterReceiptRead(t *testing.T) {
	for _, mode := range []string{"fresh", "replay", "retired"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaRefreshFixture(t)
			iaRefreshSeed(t, w, iaRefreshFact(10))
			begin := iaRefreshApply(t, w, r, p, q)
			q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{1, "refresh-asset", 0}, iaRefreshFact(11))
			if mode != "fresh" {
				iaRefreshApply(t, w, r, p, q)
			}
			if mode == "retired" {
				wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET acknowledged_command_sequence=last_command_sequence`)
			}
			state := installIAEntryQueryRevocation(t, w, "FROM catalog_info_refresh_receipts")
			turn, b := iaRefreshTurn(t, w, r, q)
			out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
			if !state.fired || state.err != nil || !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal("current owner revocation was hidden by history", state, out, err)
			}
			turn.transaction.rollback(err)
		})
	}
}
