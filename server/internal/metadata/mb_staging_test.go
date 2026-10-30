package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"strings"
	"testing"
	"time"
)

func mbStagingJob(t *testing.T, s *Service, kind string) mbJob {
	t.Helper()
	if _, e := s.db.Exec(`UPDATE mb_jobs SET status='needs_selection' WHERE kind<>?`, kind); e != nil {
		t.Fatal(e)
	}
	j, e := s.claimMB(context.Background())
	if e != nil || j == nil || j.kind != kind {
		t.Fatal(j, e)
	}
	return *j
}
func TestMBStagingParentPayloadHold(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	stage, e := stageMBRelease(mbAccountingLargeRelease(10))
	if e != nil {
		t.Fatal(e)
	}
	recordings := map[string]mbBuildProof{}
	for _, track := range stage.value.Media[0].Tracks {
		r, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: track.Recording.ID, Recording: track.Recording})
		if e != nil {
			t.Fatal(e)
		}
		recordings[r.digest] = mbBuildProof{revision: track.Recording.ID}
	}
	p, e := mbReleaseBuildPlan(stage, "large-parent", recordings)
	if e != nil {
		t.Fatal(e)
	}
	dbwork.ResetPlans()
	dbwork.WriteGate().ResetPeak()
	started := time.Now()
	if e = s.mbEnsureBuild(context.Background(), j, p, "2000-01-01T00:00:00Z", ""); e != nil {
		t.Fatal(e)
	}
	held := dbwork.WriteGate().Stats().MaxHeldMilli
	cost := dbwork.Plans().Largest
	t.Logf("one %d-byte canonical parent: elapsed=%s max-held=%dms rows=%d pages=%d VM=%d overruns=%d", len(stage.payload), time.Since(started), held, cost.Rows, cost.Pages, cost.VMOps, dbwork.Plans().Overruns)
	if held > 250 {
		t.Fatalf("SRV023 STOP: parent writer %dms >250ms", held)
	}
	if cost.Rows > 8 || cost.Pages > 2048 || cost.VMOps > 250_000 {
		t.Fatal("parent exceeded bounded plan", cost)
	}
	assertMBAccounting(t, db)
}
func TestMBStagingCommittedPartialIsChargedAndUnaccepted(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "song")
	r := mbLaneRecordingFixture()
	r.ArtistCredit = nil
	for n := 0; n < 128; n++ {
		r.ArtistCredit = append(r.ArtistCredit, metadataprovider.ArtistCredit{Name: fmt.Sprint(n), Artist: metadataprovider.Artist{ID: mbLaneArtistID}})
	}
	stage, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: r.ID, Recording: r})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TRIGGER staging_test_interrupt BEFORE INSERT ON mb_recording_credits WHEN NEW.ordinal=64 BEGIN SELECT RAISE(ABORT,'injected quantum failure');END;`); e != nil {
		t.Fatal(e)
	}
	dbwork.ResetPlans()
	_, e = s.mbStageRecording(context.Background(), j, stage, "2000-01-01T00:00:00Z")
	if e == nil || !strings.Contains(e.Error(), "injected quantum failure") {
		t.Fatal(e)
	}
	var state string
	var sealed, cursor, credits int
	var charge int64
	if e = db.QueryRow(`SELECT b.state,b.cursor,b.materialized_charge,r.sealed,(SELECT count(*) FROM mb_recording_credits c WHERE c.revision_id=r.revision_id) FROM mb_evidence_builds b JOIN mb_recording_evidence r ON r.revision_id=b.revision_id`).Scan(&state, &cursor, &charge, &sealed, &credits); e != nil {
		t.Fatal(e)
	}
	if state != "aborting" || sealed != 0 || cursor != 1+2*mbBuildQuantum || credits != 2*mbBuildQuantum || charge != int64(len(stage.payload))*8+int64(1+2*mbBuildQuantum)*512 {
		t.Fatal(state, sealed, cursor, credits, charge)
	}
	var pins int
	if e = db.QueryRow(`SELECT count(*) FROM mb_song_links`).Scan(&pins); e != nil || pins != 0 {
		t.Fatal(pins, e)
	}
	if dbwork.Plans().Largest.Rows > 160 {
		t.Fatal("construction exceeded row plan", dbwork.Plans())
	}
	assertMBAccounting(t, db)
}
func TestMBStagingFrozenProofRejectsUnexpectedRows(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "song")
	stage, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()})
	if e != nil {
		t.Fatal(e)
	}
	p := mbRecordingBuildPlan(stage, "proof-record", "now")
	if e = s.mbEnsureBuild(context.Background(), j, p, "now", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO mb_recording_credits VALUES('proof-record',127,?,'extra','','','','')`, mbLaneArtistID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.mbConstructEvidence(context.Background(), j, p, "now", ""); !errors.Is(e, errPublicationInput) {
		t.Fatal("unmanifested row sealed", e)
	}
	var sealed bool
	if e = db.QueryRow(`SELECT sealed FROM mb_recording_evidence WHERE revision_id='proof-record'`).Scan(&sealed); e != nil || sealed {
		t.Fatal(sealed, e)
	}
	assertMBAccounting(t, db)
}
func TestMBStagingFrozenRowsAndGeneration(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			db, _ := mbLaneDB(t)
			if _, e := db.Exec(fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursive)); e != nil {
				t.Fatal(e)
			}
			s := New(db, "")
			j := mbStagingJob(t, s, "song")
			stage, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: metadataprovider.Recording{ID: mbLaneRecord}})
			if e != nil {
				t.Fatal(e)
			}
			p := mbRecordingBuildPlan(stage, "frozen-record", "now")
			if e = s.mbEnsureBuild(context.Background(), j, p, "now", ""); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`INSERT INTO mb_recording_credits VALUES('frozen-record',0,?,'','','','','')`, mbLaneArtistID); e == nil {
				t.Fatal("proving set changed")
			}
			proof, e := s.mbConstructEvidence(context.Background(), j, p, "now", "")
			if e != nil {
				t.Fatal(e)
			}
			tx, e := db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			stale := proof
			stale.generation++
			if e = mbEvidenceReadyTx(context.Background(), tx, stale); !errors.Is(e, errMBBuildBusy) {
				t.Fatal("stale generation", e)
			}
			if e = mbEvidenceReadyTx(context.Background(), tx, proof); e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(`UPDATE mb_evidence_builds SET state='retiring',phase='cleanup',generation=generation+1 WHERE build_id=?`, proof.revision); e != nil {
				t.Fatal(e)
			}
			if e = mbEvidenceReadyTx(context.Background(), tx, proof); !errors.Is(e, errMBBuildBusy) {
				t.Fatal("retiring accepted", e)
			}
			if _, e = tx.Exec(`INSERT INTO mb_recording_aliases VALUES(?,?,?,'now')`, mbLaneRecord, mbLaneRecord, proof.revision); e == nil {
				t.Fatal("retiring alias allowed")
			}
			tx.Rollback()
			assertMBAccounting(t, db)
		})
	}
}
func TestMBStagingResumeAndReadyReuse(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "song")
	stage, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()})
	if e != nil {
		t.Fatal(e)
	}
	p := mbRecordingBuildPlan(stage, "resume-record", "now")
	if e = s.mbEnsureBuild(context.Background(), j, p, "now", ""); e != nil {
		t.Fatal(e)
	}
	restarted := New(db, "")
	proof, e := restarted.mbConstructEvidence(context.Background(), j, p, "now", "")
	if e != nil {
		t.Fatal(e)
	}
	again, e := restarted.mbStageRecording(context.Background(), j, stage, "later")
	if e != nil || again != proof {
		t.Fatal(again, proof, e)
	}
	assertMBAccounting(t, db)
}

// Exactly the stopped gate's two disjoint 10k closures, now with the real final
// publication: active operation/base/receipt, aliases, pin, catalog fields,
// reconciliation, candidate decisions, receipt and operation completion. The old
// release fixture is seeded through the preserved legacy saver; the new release
// uses every real bounded build/proof/seal transaction before this final hold.
func TestMBStagingTenThousandCompleteFinalWriterHold(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, names := mbLaneDB(t)
	ctx := context.Background()
	oldValue := mbAccountingLargeRelease(10)
	oldStage, e := stageMBRelease(oldValue)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	old, e := saveMBReleaseEvidence(ctx, tx, oldStage, "2000-01-01T00:00:00Z")
	if e != nil {
		t.Fatal(e)
	}
	if e = mbFixturePinTx(tx, mbFixturePinIntent{Link: "album", Entity: names["album"].ID, NewRelease: old}, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO mb_album_links VALUES(?,?,?,?,'2000-01-01T00:00:00Z')`, names["album"].ID, old, oldValue.Release.ID, oldValue.RequestedID)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if e = checkMBEvidenceBudget(ctx, tx); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	nextValue := mbAccountingLargeRelease(20)
	s := New(db, "")
	if _, e = db.Exec(`UPDATE mb_jobs SET manual=1,selected_id=?,generation=generation+1,status='pending' WHERE kind='album' AND entity_id=?`, nextValue.RequestedID, names["album"].ID); e != nil {
		t.Fatal(e)
	}
	j := mbStagingJob(t, s, "album")
	if _, e = db.Exec(`INSERT INTO mb_candidates(kind,entity_id,provider_id,entity_type,title,artist,edition,confidence,decision,query_digest,payload,observed_at) VALUES('album',?,?,'release','','','',1,'candidate',?,'{}','now')`, names["album"].ID, nextValue.RequestedID, j.digest); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO mb_selection_receipts(id,kind,entity_id,job_generation,actor_authority,actor_account_id,actor_profile_id,candidate_id,query_digest,prior_revision_id,status,created_at) VALUES('complete-final','album',?,?,'local','owner','profile',?,?,?,'pending','now')`, names["album"].ID, j.generation, nextValue.RequestedID, j.digest, old); e != nil {
		t.Fatal(e)
	}
	stage, e := stageMBRelease(nextValue)
	if e != nil {
		t.Fatal(e)
	}
	dbwork.ResetPlans()
	dbwork.WriteGate().ResetPeak()
	start := time.Now()
	proof, e := s.mbStageRelease(ctx, j, stage, "2000-01-01T00:00:00Z")
	if e != nil {
		t.Fatal("bounded construction", e)
	}
	t.Logf("10k construction/proof/seal elapsed=%s peak-held=%dms largest=%+v overruns=%d", time.Since(start), dbwork.WriteGate().Stats().MaxHeldMilli, dbwork.Plans().Largest, dbwork.Plans().Overruns)
	if peak := dbwork.WriteGate().Stats().MaxHeldMilli; peak > 250 {
		t.Fatalf("SRV023 STOP: construction held %dms", peak)
	}
	if dbwork.Plans().Overruns != 0 {
		t.Fatal("bounded construction exceeded unchanged plans", dbwork.Plans())
	}
	if e = s.mbAttachStaged(ctx, j, proof); e != nil {
		t.Fatal(e)
	}
	assertMBAccounting(t, db)
	// Preserve the original gate's exact recording boundary. In a disposable
	// prospective fixture transaction, releasing the old pin makes all 20,000
	// real recordings unaccepted; the normalized 20,001st (with its credit)
	// must fail. Rollback restores the original accepted pin and its heads.
	capTx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer capTx.Rollback()
	if e = mbFixturePinTx(capTx, mbFixturePinIntent{Link: "album", Entity: names["album"].ID}, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM mb_album_links WHERE album_id=?`, names["album"].ID)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	var cachedRecordings int
	if e = capTx.QueryRow(`SELECT recordings FROM mb_evidence_totals WHERE singleton=1`).Scan(&cachedRecordings); e != nil || cachedRecordings != 20_000 {
		t.Fatal(cachedRecordings, e)
	}
	if e = checkMBEvidenceBudget(ctx, capTx); e != nil {
		t.Fatal("exactly20k refused", e)
	}
	overflow, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = saveMBRecordingEvidence(ctx, capTx, overflow, "now"); e != nil {
		t.Fatal(e)
	}
	if e = checkMBEvidenceBudget(ctx, capTx); !errors.Is(e, errMBBudget) {
		t.Fatal("normalized20,001st admitted", e)
	}
	if e = capTx.Rollback(); e != nil {
		t.Fatal(e)
	}
	assertMBAccounting(t, db)
	dbwork.ResetPlans()
	dbwork.WriteGate().ResetPeak()
	start = time.Now()
	if e = s.mbPublishAlbumReady(ctx, j, stage, proof, "2000-01-01T00:00:00Z"); e != nil {
		t.Fatal("complete final publication", e)
	}
	elapsed := time.Since(start)
	held := dbwork.WriteGate().Stats().MaxHeldMilli
	cost := dbwork.Plans().Largest
	t.Logf("complete old10k->disjointnew10k final incl adoption enqueue: elapsed=%s peak-held=%dms rows=%d pages=%d VM=%d overruns=%d", elapsed, held, cost.Rows, cost.Pages, cost.VMOps, dbwork.Plans().Overruns)
	// Claude's explicit P2-52b-FINAL-GATE ruling permits only this named
	// bounded old/new10k exception up to500ms. Ordinary turns remain250ms.
	// Runtime's post-Begin timer asserts the precise bound after release;
	// peak-held excludes queue wait, while outer elapsed is diagnostic only.
	if held > uint64(mbFinalExtremeHold/time.Millisecond) {
		t.Errorf("SRV023 STOP before merge: complete final elapsed=%s maxheld=%dms >500ms", elapsed, held)
	}
	if dbwork.Plans().Overruns != 0 {
		t.Fatal("complete final exceeded unchanged plan", dbwork.Plans())
	}
	assertMBAccounting(t, db)
	var pin, receipt, operation, decision string
	if e = db.QueryRow(`SELECT release_revision FROM mb_album_links WHERE album_id=?`, names["album"].ID).Scan(&pin); e != nil || pin != proof.revision {
		t.Fatal(pin, e)
	}
	if e = db.QueryRow(`SELECT status FROM mb_selection_receipts WHERE id='complete-final'`).Scan(&receipt); e != nil || receipt != "applied" {
		t.Fatal(receipt, e)
	}
	if e = db.QueryRow(`SELECT status FROM mb_publication_operations WHERE id=?`, j.operation).Scan(&operation); e != nil || operation != "applied" {
		t.Fatal(operation, e)
	}
	if e = db.QueryRow(`SELECT decision FROM mb_candidates WHERE kind='album' AND entity_id=? AND provider_id=?`, names["album"].ID, nextValue.RequestedID).Scan(&decision); e != nil || decision != "accepted" {
		t.Fatal(decision, e)
	}
	var adoption int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='mb-adoption' AND cause_operation=? AND accepted_revision=?`, j.operation, proof.revision).Scan(&adoption); e != nil || adoption != 1 {
		t.Fatal("final adoption intent missing", adoption, e)
	}
	var recordings, continuations int
	if e = db.QueryRow(`SELECT count(*) FROM mb_recording_evidence`).Scan(&recordings); e != nil || recordings != 20_000 {
		t.Fatal(recordings, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND release_revision=? AND status='pending'`, names["album"].ID, proof.revision).Scan(&continuations); e != nil || continuations != 1 {
		t.Fatal(continuations, e)
	}
}

func TestMBStagingFinalBudgetRollsBackEveryEffect(t *testing.T) {
	db, names := mbLaneDB(t)
	ctx := context.Background()
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	value := mbLaneReleaseFixture()
	save := func(title string) string {
		t.Helper()
		v := value
		v.Title = title
		stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: v.ID, Release: v})
		if e != nil {
			t.Fatal(e)
		}
		id, e := saveMBReleaseEvidence(ctx, tx, stage, "2099-01-01T00:00:00Z")
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	old := save("old accepted")
	next := save("new accepted elsewhere")
	var library int64
	if e = tx.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, names["album"].ID).Scan(&library); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1000001,randomblob(16),?,6,'other','other','o')`, library); e != nil {
		t.Fatal(e)
	}
	for _, pin := range []struct {
		album   int64
		release string
	}{{names["album"].ID, old}, {1000001, next}} {
		pin := pin
		if e = mbFixturePinTx(tx, mbFixturePinIntent{Link: "album", Entity: pin.album, NewRelease: pin.release}, func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO mb_album_links VALUES(?,?,?,?,'now')`, pin.album, pin.release, value.ID, value.ID)
			return err
		}); e != nil {
			t.Fatal(e)
		}
	}
	for n := 0; n < 512; n++ {
		save(fmt.Sprintf("cache %d", n))
	}
	if e = checkMBEvidenceBudget(ctx, tx); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	s := New(db, "")
	if _, e = db.Exec(`UPDATE mb_jobs SET selected_id=?,manual=1,generation=generation+1,status='pending' WHERE kind='album' AND entity_id=?`, value.ID, names["album"].ID); e != nil {
		t.Fatal(e)
	}
	j := mbStagingJob(t, s, "album")
	if _, e = db.Exec(`INSERT INTO mb_candidates(kind,entity_id,provider_id,entity_type,title,artist,edition,confidence,decision,query_digest,payload,observed_at) VALUES('album',?,?,'release','','','',1,'candidate',?,'{}','now')`, names["album"].ID, value.ID, j.digest); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO mb_selection_receipts(id,kind,entity_id,job_generation,actor_authority,actor_account_id,actor_profile_id,candidate_id,query_digest,prior_revision_id,status,created_at) VALUES('rollback-all','album',?,?,'local','owner','profile',?,?,?,'pending','now')`, names["album"].ID, j.generation, value.ID, j.digest, old); e != nil {
		t.Fatal(e)
	}
	value.Title = "new accepted elsewhere"
	stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value})
	if e != nil {
		t.Fatal(e)
	}
	proof, e := s.mbStageRelease(ctx, j, stage, "now")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.mbAttachStaged(ctx, j, proof); e != nil {
		t.Fatal(e)
	}
	if e = s.mbPublishAlbumReady(ctx, j, stage, proof, "now"); !errors.Is(e, errMBBudget) {
		t.Fatal(e)
	}
	var pin, status, operation, decision string
	if e = db.QueryRow(`SELECT release_revision FROM mb_album_links WHERE album_id=?`, names["album"].ID).Scan(&pin); e != nil || pin != old {
		t.Fatal(pin, e)
	}
	if e = db.QueryRow(`SELECT status FROM mb_selection_receipts WHERE id='rollback-all'`).Scan(&status); e != nil || status != "pending" {
		t.Fatal(status, e)
	}
	if e = db.QueryRow(`SELECT status FROM mb_publication_operations WHERE id=?`, j.operation).Scan(&operation); e != nil || operation != "staged" {
		t.Fatal(operation, e)
	}
	if e = db.QueryRow(`SELECT decision FROM mb_candidates WHERE kind='album' AND entity_id=?`, names["album"].ID).Scan(&decision); e != nil || decision != "candidate" {
		t.Fatal(decision, e)
	}
	var effects int
	if e = db.QueryRow(`SELECT (SELECT count(*) FROM mb_release_aliases WHERE evidence_revision=?)+(SELECT count(*) FROM mb_album_reconciliations WHERE album_id=?)`, next, names["album"].ID).Scan(&effects); e != nil || effects != 0 {
		t.Fatal("alias/recon escaped rollback", effects, e)
	}
	if e = db.QueryRow(`SELECT status FROM mb_jobs WHERE kind='album' AND entity_id=?`, names["album"].ID).Scan(&status); e != nil || status != "pending" {
		t.Fatal(status, e)
	}
	var adoption int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE cause_operation=?`, j.operation).Scan(&adoption); e != nil || adoption != 0 {
		t.Fatal("adoption intent escaped rollback", adoption, e)
	}
	var release string
	if e = db.QueryRow(`SELECT release_id FROM catalog_albums WHERE entity_id=?`, names["album"].ID).Scan(&release); e != nil || release != "" {
		t.Fatal("catalog release escaped rollback", release, e)
	}
	assertMBAccounting(t, db)
}

func TestMBStagingOriginProvenanceNeverReparentsReuse(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	ctx := context.Background()
	value := mbLaneReleaseFixture()
	record, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()})
	if e != nil {
		t.Fatal(e)
	}
	existing, e := s.mbStageRecording(ctx, j, record, "now")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE mb_evidence_builds SET owner_operation='foreign-ready-owner' WHERE build_id=?`, existing.revision); e != nil {
		t.Fatal(e)
	}
	added := value.Media[0].Tracks[0]
	added.ID = "44444444-4444-4444-4444-444444444445"
	added.Position = 4
	added.Recording.ID = "11111111-1111-1111-1111-111111111112"
	value.Media[0].Tracks = append(value.Media[0].Tracks, added)
	stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value})
	if e != nil {
		t.Fatal(e)
	}
	parent, e := s.mbStageRelease(ctx, j, stage, "now")
	if e != nil {
		t.Fatal(e)
	}
	var oldParent, newParent, owner string
	if e = db.QueryRow(`SELECT owner_operation FROM mb_evidence_builds WHERE build_id=?`, existing.revision).Scan(&owner); e != nil || owner != "" {
		t.Fatal("real track FK did not release foreign reservation", owner, e)
	}
	if e = db.QueryRow(`SELECT parent_build FROM mb_evidence_builds WHERE build_id=?`, existing.revision).Scan(&oldParent); e != nil || oldParent != "" {
		t.Fatal("reused recording reparented", oldParent, e)
	}
	if e = db.QueryRow(`SELECT parent_build,owner_operation FROM mb_evidence_builds WHERE kind='recording' AND provider_id=?`, added.Recording.ID).Scan(&newParent, &owner); e != nil || newParent != parent.revision || owner != "" {
		t.Fatal(newParent, owner, e)
	}
	value.Title = "second parent reuses children"
	stage, e = stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.mbStageRelease(ctx, j, stage, "now"); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT parent_build FROM mb_evidence_builds WHERE kind='recording' AND provider_id=?`, added.Recording.ID).Scan(&newParent); e != nil || newParent != parent.revision {
		t.Fatal("origin was reassigned", newParent, e)
	}
	assertMBAccounting(t, db)
}

func TestMBStagingLateExpiryAndCancellationKeepPartialCharge(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			db, _ := mbLaneDB(t)
			s := New(db, "")
			j := mbStagingJob(t, s, "song")
			ctx := context.Background()
			stage, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()})
			if e != nil {
				t.Fatal(e)
			}
			p := mbRecordingBuildPlan(stage, "late-expiry", "now")
			if e = s.mbEnsureBuild(ctx, j, p, "now", ""); e != nil {
				t.Fatal(e)
			}
			if cancelled {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			} else {
				s.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
			}
			if _, e = s.mbConstructEvidence(ctx, j, p, "now", ""); cancelled && !errors.Is(e, context.Canceled) || !cancelled && !errors.Is(e, errTVDBStale) {
				t.Fatal(e)
			}
			var state string
			var cursor int
			if e = db.QueryRow(`SELECT state,cursor FROM mb_evidence_builds WHERE build_id='late-expiry'`).Scan(&state, &cursor); e != nil || state != "aborting" || cursor != 1 {
				t.Fatal(state, cursor, e)
			}
			assertMBAccounting(t, db)
		})
	}
}

func TestMBStagingForeignBuildingDuplicateDoesNotSteal(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "song")
	ctx := context.Background()
	stage, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()})
	if e != nil {
		t.Fatal(e)
	}
	p := mbRecordingBuildPlan(stage, "foreign-build", "now")
	if e = s.mbEnsureBuild(ctx, j, p, "now", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE mb_jobs SET lease='',lease_until='',generation=generation+1,status='pending' WHERE kind='song'`); e != nil {
		t.Fatal(e)
	}
	other := mbStagingJob(t, s, "song")
	if _, e = s.mbStageRecording(ctx, other, stage, "now"); !errors.Is(e, errMBBuildBusy) {
		t.Fatal("foreign partial reused", e)
	}
	var owner, state string
	var gen int64
	if e = db.QueryRow(`SELECT owner_operation,state,generation FROM mb_evidence_builds WHERE build_id='foreign-build'`).Scan(&owner, &state, &gen); e != nil || owner != j.operation || state != "building" || gen != 1 {
		t.Fatal("foreign build changed", owner, state, gen, e)
	}
	assertMBAccounting(t, db)
}

func TestMBStagingCanonicalProofMatchesOriginalReader(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	ctx := context.Background()
	v := mbLaneReleaseFixture()
	v.Title = "Été <live> \\\"edition\\\""
	v.ArtistCredit = v.Media[0].Tracks[0].ArtistCredit
	v.ReleaseGroup.SecondaryTypes = []string{"Live", "Compilation"}
	v.Media = append(v.Media, metadataprovider.Medium{Position: 1, Format: "Digital Media", Title: "empty medium"})
	for n := 1; n < 70; n++ {
		c := metadataprovider.ArtistCredit{Name: fmt.Sprintf("Édition %d", n), JoinPhrase: " & ", Artist: metadataprovider.Artist{ID: mbLaneArtistID, Name: "名"}}
		v.Media[0].Tracks[0].ArtistCredit = append(v.Media[0].Tracks[0].ArtistCredit, c)
		v.Media[0].Tracks[0].Recording.ArtistCredit = append(v.Media[0].Tracks[0].Recording.ArtistCredit, c)
	}
	stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: v.ID, Release: v})
	if e != nil {
		t.Fatal(e)
	}
	proof, e := s.mbStageRelease(ctx, j, stage, "now")
	if e != nil {
		t.Fatal(e)
	}
	e = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), db, func(readCtx context.Context) error {
		value, digest, e := loadMBReleaseEvidence(readCtx, dbwork.Snapshot(readCtx), proof.revision, false)
		if e != nil {
			return e
		}
		restaged, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value})
		if e != nil {
			return e
		}
		if digest != stage.digest || restaged.digest != stage.digest {
			return errors.New("bounded component proof diverged from original full reconstruction")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	assertMBAccounting(t, db)
}

func TestMBStagingFinalExpiryRollsBackEveryEffect(t *testing.T) {
	db, names := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	ctx := context.Background()
	v := mbLaneReleaseFixture()
	stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: v.ID, Release: v})
	if e != nil {
		t.Fatal(e)
	}
	proof, e := s.mbStageRelease(ctx, j, stage, "now")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.mbAttachStaged(ctx, j, proof); e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	calls := 0
	s.now = func() time.Time {
		calls++
		if calls > 1 {
			return at.Add(2 * time.Minute)
		}
		return at
	}
	if e = s.mbPublishAlbumReady(ctx, j, stage, proof, "now"); !errors.Is(e, errTVDBStale) {
		t.Fatal("late expiry committed final authority", e)
	}
	var count int
	if e = db.QueryRow(`SELECT (SELECT count(*) FROM mb_album_links WHERE album_id=?)+(SELECT count(*) FROM mb_album_reconciliations WHERE album_id=?)+(SELECT count(*) FROM mb_release_aliases WHERE evidence_revision=?)+(SELECT count(*) FROM catalog_entity_mutations WHERE cause_operation=?)`, names["album"].ID, names["album"].ID, proof.revision, j.operation).Scan(&count); e != nil || count != 0 {
		t.Fatal("expired final left durable effects", count, e)
	}
	var status, release string
	if e = db.QueryRow(`SELECT status FROM mb_publication_operations WHERE id=?`, j.operation).Scan(&status); e != nil || status != "staged" {
		t.Fatal(status, e)
	}
	if e = db.QueryRow(`SELECT release_id FROM catalog_albums WHERE entity_id=?`, names["album"].ID).Scan(&release); e != nil || release != "" {
		t.Fatal("expired final changed catalog", release, e)
	}
	assertMBAccounting(t, db)
}

func TestMBStagingForeignReadyResultReleasesReservation(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "song")
	value := mbLaneRecordingFixture()
	stage, err := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: value.ID, Recording: value})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := s.mbStageRecording(context.Background(), j, stage, "now")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE mb_evidence_builds SET owner_operation='foreign-ready-owner' WHERE build_id=?`, proof.revision); err != nil {
		t.Fatal(err)
	}
	if err = s.mbAttachStaged(context.Background(), j, proof); err != nil {
		t.Fatal(err)
	}
	var owner, result string
	if err = db.QueryRow(`SELECT owner_operation FROM mb_evidence_builds WHERE build_id=?`, proof.revision).Scan(&owner); err != nil || owner != "" {
		t.Fatal(owner, err)
	}
	if err = db.QueryRow(`SELECT result_recording_revision FROM mb_publication_operations WHERE id=?`, j.operation).Scan(&result); err != nil || result != proof.revision {
		t.Fatal(result, err)
	}
	assertMBAccounting(t, db)
}
