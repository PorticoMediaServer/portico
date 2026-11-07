package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func TestIPArtworkMaterialUploadAcceptance(t *testing.T) {
	ipAMTestCodecGate(t)
	w, q := ipAMTestFixture(t)
	ipADTestExec(t, w, `UPDATE artwork_jobs SET choice='locked',provider='upload',origin='upload' WHERE id=?`, q.Job.ID)
	q.Policy.Provider = "upload"
	q.Job.Choice = "locked"
	q.Job.Origin = "upload"
	// Upload claim/preparation/install remains available offline, under its
	// exact native locked upload job and the current outer owner permission.
	ipADTestExec(t, w, `UPDATE metadata_runtime_settings SET online_lookups=0`)
	q = ipAMTestClaimed(t, w, q)
	q = ipAMTestMaterial(q, true)
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	q.Phase = ipArtInstalled
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	m := q.Material
	q.Phase = ipArtPublish
	q.Material.Revision++
	q.Selection = ipArtworkSelectionRequest{Role: "poster", Choice: "locked", Actor: "owner", After: ipAMTestValue(q, 1)}
	ipADTestExec(t, w, `UPDATE metadata_runtime_settings SET online_lookups=0`)
	// Fail after material/source/selection/history writes, then prove complete
	// rollback. Existing installed file custody remains in its immutable intent.
	fault := ipAMTestTurn(t, w, IPArtworkUploadAccept, &testCustody{})
	defs := ipArtworkMaterialDefinitions()
	for n := range defs {
		if defs[n].ID == ipAMHistory {
			defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING id", "RETURNING abs(-9223372036854775808)", 1)
		}
	}
	registry, e := stmttest.New(t, defs)
	if e != nil {
		t.Fatal(e)
	}
	fault.registry = registry
	failed, e := ipArtworkMaterialApply(t.Context(), fault, IPArtworkUploadAccept, q)
	if failed != (ipArtworkMaterialOutcome{}) {
		t.Fatal(failed)
	}
	ipADTestRollback(t, fault, e)
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_sources`); n != 0 {
		t.Fatal("rolled-back upload source", n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_blobs`); n != 0 {
		t.Fatal("rolled-back upload blobs", n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_selections`); n != 0 {
		t.Fatal("rolled-back choice", n)
	}
	if n := ipAMTestScalar(t, w, `SELECT staged_slots FROM artwork_material_intents WHERE id=?`, q.Material.ID); n != 4 {
		t.Fatal("lost physical custody", n)
	}
	r := ipAMTestApply(t, w, IPArtworkUploadAccept, q)
	if r.result.HistoryID <= 0 || r.result.Selected != m.SourceDigest || r.result.MaterialID != m.ID || r.result.Choice != "locked" {
		t.Fatal(r)
	}
	if replay := ipAMTestApply(t, w, IPArtworkUploadAccept, q); replay.decision != receiptReplay || replay.result != r.result {
		t.Fatal(replay)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_sources`); n != 1 {
		t.Fatal(n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_selections WHERE choice='locked'`); n != 1 {
		t.Fatal(n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history_artwork`); n != 1 {
		t.Fatal(n)
	}
}
func TestIPArtworkMaterialProtectedRowsError(t *testing.T) {
	w, q := ipAMTestFixture(t)
	q.Job.LeaseGeneration = 1
	q = ipAMTestMaterial(q, false)
	// Tiny native manifest fixture; no accepting command or synthetic SQL is
	// registered outside this test's isolated registry.
	ipADTestExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots)
 VALUES(?,?,?,1,1,?,'image/webp',1200,1800,1920,1,'prepared',3,3)`, q.Material.ID, q.Owner.id, q.Owner.incarnation[:], q.Material.SourceDigest)
	for n := 0; n < 3; n++ {
		s := q.Material.Slots[n]
		ipADTestExec(t, w, `INSERT INTO artwork_material_slots VALUES(?,?,?,?,?,?,?,0)`, q.Material.ID, int64(n), s.Digest, s.MIME, s.Width, s.Height, s.Bytes)
	}
	turn := ipAMTestTurn(t, w, IPArtworkMaterialPublish, &testCustody{})
	defs := ipArtworkMaterialDefinitions()
	for n := range defs {
		if defs[n].ID == ipAMSlotKeys {
			defs[n].SQL = `SELECT CASE WHEN slot=1 THEN abs(-9223372036854775808) ELSE slot END FROM artwork_material_slots WHERE intent_id=? AND slot>? ORDER BY slot LIMIT ?`
		}
	}
	registry, e := stmttest.New(t, defs)
	if e != nil {
		t.Fatal(e)
	}
	turn.registry = registry
	keys, e := ipArtSlotKeys(t.Context(), turn, q.Material.ID)
	if e == nil || keys != nil {
		t.Fatal("released errored prefix", keys, e)
	}
	if len(turn.rows) != 1 || turn.rows[0].Next() {
		t.Fatal("rows survived protected Close")
	}
	if e = turn.closeRows(); e != nil {
		t.Fatal(e)
	}
	if _, e = turn.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
}
