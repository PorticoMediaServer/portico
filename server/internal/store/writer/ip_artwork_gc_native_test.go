package writer

import (
	"strings"
	"testing"
)

func ipAMTestRefuse(t *testing.T, w *executor, k CommandID, q ipArtworkMaterialRequest) {
	t.Helper()
	turn := ipAMTestTurn(t, w, k, &testCustody{})
	out, e := ipArtworkMaterialApply(t.Context(), turn, k, q)
	if out != (ipArtworkMaterialOutcome{}) {
		t.Fatal(out)
	}
	ipADTestRollback(t, turn, e)
}
func TestIPArtworkMaterialGCTokensHoldsAndReferences(t *testing.T) {
	ipAMTestCodecGate(t)
	w, q := ipAMTestFixture(t)
	q = ipAMTestPublished(t, w, q)
	source := q.Material.SourceDigest
	q.GC = ipArtworkGCRequest{Target: ipArtSource, Digest: source, Revision: 1, Token: [16]byte{41}, OriginalBytes: 1024}
	// Historical selected source receipt AND material manifest retain custody,
	// even after ACK; actual disposition is required, never merely an ACK floor.
	ipAMTestRefuse(t, w, IPArtworkGCMark, q)
	ipADTestExec(t, w, `DELETE FROM artwork_command_receipts WHERE owner_id=? AND command_sequence<=?`, q.Owner.id, q.Identity.Sequence-1)
	ipAMTestRefuse(t, w, IPArtworkGCMark, q) // published material still pins source
	q.GC = ipArtworkGCRequest{Target: ipArtIntent, Revision: q.Material.Revision, Token: [16]byte{42}}
	ipAMTestApply(t, w, IPArtworkGCMark, q)
	q = ipAMTestNext(t, w, q)
	q.GC.Revision++
	q.Material.Revision++
	// Exact token is required for the physical owner to finalize staging custody.
	bad := q
	bad.GC.Token[0]++
	ipAMTestRefuse(t, w, IPArtworkGCFinish, bad)
	ipAMTestApply(t, w, IPArtworkGCFinish, q)
	q = ipAMTestNext(t, w, q)
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_material_slots`); n != 0 {
		t.Fatal(n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_blobs`); n != 3 {
		t.Fatal("staging retirement removed shared blobs", n)
	}
	q.GC = ipArtworkGCRequest{Target: ipArtSource, Digest: source, Revision: 1, Token: [16]byte{43}, OriginalBytes: 1024}
	// Independent exact source references, normalized undo, and global backup.
	ipADTestExec(t, w, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',101,'poster','locked',?,'upload','id','upload','','fence','owner','fixture')`, source)
	ipAMTestRefuse(t, w, IPArtworkGCMark, q)
	ipADTestExec(t, w, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=101 AND role='poster' AND choice='locked'`)
	ipADTestExec(t, w, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('entity',101,'1','2','fixture','owner','fixture','{}','{}')`)
	ipADTestExec(t, w, `INSERT INTO metadata_owner_history_artwork(history_id,role,choice,digest) VALUES(1,'poster','locked',?)`, source)
	ipAMTestRefuse(t, w, IPArtworkGCMark, q)
	ipADTestExec(t, w, `DELETE FROM metadata_owner_history_artwork WHERE history_id=1`)
	ipADTestExec(t, w, `INSERT INTO backup_blob_holds VALUES(1,'backup','epoch','scratch',0,'copying')`)
	ipAMTestRefuse(t, w, IPArtworkGCMark, q)
	ipADTestExec(t, w, `DELETE FROM backup_blob_holds WHERE singleton=1`)
	r := ipAMTestApply(t, w, IPArtworkGCMark, q)
	if r.result.Selected != "" || r.result.Blob != source || r.result.StorageRevision != 2 {
		t.Fatal(r)
	}
	// Replay does not repeat mark, even though the source is now retiring.
	if replay := ipAMTestApply(t, w, IPArtworkGCMark, q); replay.result != r.result || replay.decision != receiptReplay {
		t.Fatal(replay)
	}
	q = ipAMTestNext(t, w, q)
	q.GC.Revision++
	// New references refuse the retiring source under the same writer turn.
	selected := q
	selected.Selection = ipArtworkSelectionRequest{Role: "poster", Choice: "matched", Actor: "owner", After: ipAMTestValue(q, 1)}
	ipAMTestRefuse(t, w, IPArtworkSelect, selected)
	turn := ipAMTestTurn(t, w, IPArtworkMaterialPublish, &testCustody{})
	e := ipArtCheckReadyOrAbsent(t.Context(), turn, q.Material)
	ipADTestRollback(t, turn, e)
	// A retiring source still holds every blob until source deletion is physical.
	blob := q
	blob.GC = ipArtworkGCRequest{Target: ipArtBlob, Digest: q.Material.Slots[0].Digest, Revision: 1, Token: [16]byte{44}}
	ipAMTestRefuse(t, w, IPArtworkGCMark, blob)
	bad = q
	bad.GC.Token[0]++
	ipAMTestRefuse(t, w, IPArtworkGCFinish, bad)
	// A newly acquired backup also prevents finalize. Parent backup admission
	// must additionally refuse outstanding marks BEFORE starting its copy.
	ipADTestExec(t, w, `INSERT INTO backup_blob_holds VALUES(1,'backup2','epoch','scratch2',0,'files')`)
	ipAMTestRefuse(t, w, IPArtworkGCFinish, q)
	ipADTestExec(t, w, `DELETE FROM backup_blob_holds WHERE singleton=1`)
	ipAMTestApply(t, w, IPArtworkGCFinish, q)
	q = ipAMTestNext(t, w, q)
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_sources`); n != 0 {
		t.Fatal(n)
	}
	q.GC = ipArtworkGCRequest{Target: ipArtBlob, Digest: q.Material.Slots[0].Digest, Revision: 1, Token: [16]byte{45}}
	ipAMTestApply(t, w, IPArtworkGCMark, q)
	q = ipAMTestNext(t, w, q)
	q.GC.Revision++
	// No new source or manifest can claim this deleting exact blob.
	turn = ipAMTestTurn(t, w, IPArtworkMaterialPublish, &testCustody{})
	e = ipArtCheckReadyOrAbsent(t.Context(), turn, q.Material)
	ipADTestRollback(t, turn, e)
	ipAMTestApply(t, w, IPArtworkGCFinish, q)
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_blobs WHERE digest=?`, q.GC.Digest); n != 0 {
		t.Fatal(n)
	}
	// Its retained mark/finish receipts have scalar blob digests, not dangling FKs.
	rows, e := w.db.QueryContext(t.Context(), `PRAGMA foreign_key_check`)
	if e != nil {
		t.Fatal(e)
	}
	if rows.Next() {
		t.Fatal("FK pin to deleted blob/source")
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if e = rows.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestIPArtworkMaterialReencodeSourceRevision(t *testing.T) {
	ipAMTestCodecGate(t)
	w, q := ipAMTestFixture(t)
	q = ipAMTestPublished(t, w, q)
	old := q.Material
	// A separate existing retained subject-job still owns the new image job.
	q.Job.ID = "reencode-job"
	q.Job.State = 2
	q.Job.Revision = 1
	q.Job.LeaseUntilMS = q.Owner.leaseUntilMS - 1
	q.Job.LeaseGeneration = 21
	q.Job.NextMS = EvidenceInteger{0, true}
	j := q.Job
	ipADTestExec(t, w, `INSERT INTO artwork_jobs(id,subject_job_id,kind,entity_id,role,choice,image_id,origin,attribution,locale,effective_language,provider,source_fence,actor,created_at,status,job_next_attempt_ms,job_lease_owner,job_lease_generation,job_lease_expires_ms)
 VALUES(?,?,'entity',101,?,'matched',?,?,?,?,?,?,?,?,'fixture','running',0,?,?,?)`, j.ID, q.Owner.id, "backdrop", j.ImageID, j.Origin, j.Attribution, j.Locale, q.Policy.Language, q.Policy.Provider, j.SourceFence, j.Actor, j.LeaseOwner, j.LeaseGeneration, j.LeaseUntilMS)
	q.Job.Role = "backdrop"
	q.Material.ID = q.Job.ID
	q.Material.Generation = q.Job.LeaseGeneration
	q.Material.Revision = 1
	q.Material.SourceRevision = 1
	q.Material.EncoderRevision = 2
	q.Material.Previous = ipArtworkMaterialSource{old.Slots[0].Digest, old.Slots[1].Digest, old.Slots[2].Digest, old.LargeEdge, 0, 0, 1, 1024}
	for n := 0; n < 3; n++ {
		q.Material.Slots[n].Digest = strings.Repeat(string(rune('1'+n)), 64)
	}
	q.Phase = ipArtPrepare
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	q.Phase = ipArtInstalled
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	q.Material.Revision++
	q.Phase = ipArtPublish
	bad := q
	bad.Material.SourceRevision++
	ipAMTestRefuse(t, w, IPArtworkReencode, bad)
	ipAMTestApply(t, w, IPArtworkReencode, q)
	if n := ipAMTestScalar(t, w, `SELECT revision FROM artwork_sources WHERE digest=?`, old.SourceDigest); n != 2 {
		t.Fatal(n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_selections`); n != 0 {
		t.Fatal("child/reference fanout", n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history`); n != 0 {
		t.Fatal(n)
	}
}

func TestIPArtworkMaterialNoopSourceReuse(t *testing.T) {
	ipAMTestCodecGate(t)
	w, q := ipAMTestFixture(t)
	q = ipAMTestPublished(t, w, q)
	// The production source helper reuses native metadata with zero source/blob
	// writes, and an identical reencode preserves the current source revision.
	turn := ipAMTestTurn(t, w, IPArtworkMaterialPublish, &testCustody{})
	r := ipArtworkMaterialResult{}
	if e := ipArtPublishSource(t.Context(), turn, IPArtworkMaterialPublish, q, w.now().UnixMilli(), &r); e != nil {
		t.Fatal(e)
	}
	if r.Count != 0 || r.StorageRevision != 1 {
		t.Fatal(r)
	}
	q.Material.SourceRevision = 1
	q.Material.Previous = ipArtworkMaterialSource{q.Material.Slots[0].Digest, q.Material.Slots[1].Digest, q.Material.Slots[2].Digest, q.Material.LargeEdge, 0, 0, 1, 1024}
	if e := ipArtPublishSource(t.Context(), turn, IPArtworkReencode, q, w.now().UnixMilli(), &r); e != nil {
		t.Fatal(e)
	}
	if r.Count != 0 || r.StorageRevision != 1 {
		t.Fatal(r)
	}
	if e := turn.closeRows(); e != nil {
		t.Fatal(e)
	}
	if _, e := turn.transaction.commit(); e != nil {
		t.Fatal(e)
	}
	if n := ipAMTestScalar(t, w, `SELECT revision FROM artwork_sources`); n != 1 {
		t.Fatal(n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_blobs`); n != 3 {
		t.Fatal(n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history`); n != 0 {
		t.Fatal(n)
	}
}
