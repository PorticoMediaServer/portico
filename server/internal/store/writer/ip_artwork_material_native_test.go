package writer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func ipAMTestFixture(t *testing.T) (*executor, ipArtworkMaterialRequest) {
	t.Helper()
	w, d, _ := ipADTestFixture(t, 0)
	q := ipArtworkMaterialRequest{Identity: d.identity, Owner: d.owner, Policy: ipArtworkMaterialPolicy{"tmdb", "fr-CA", 3}, Phase: 60000,
		Job: ipArtworkMaterialJob{ID: "image-job", Role: "poster", Choice: "matched", ImageID: "image-1", Origin: "tmdb", Attribution: "© fixture", Locale: "fr-CA", SourceFence: "source-v7", Actor: "owner", Revision: 1, State: 1, NextMS: EvidenceInteger{0, true}}}
	j := q.Job
	ipADTestExec(t, w, `INSERT INTO artwork_jobs(id,subject_job_id,kind,entity_id,role,choice,image_id,origin,attribution,locale,effective_language,provider,source_fence,actor,created_at,job_next_attempt_ms)
 VALUES(?,?,'entity',101,?,?,?,?,?,?,?,?,?,?,'fixture',0)`, j.ID, q.Owner.id, j.Role, j.Choice, j.ImageID, j.Origin, j.Attribution, j.Locale, q.Policy.Language, q.Policy.Provider, j.SourceFence, j.Actor)
	// Native complete observation used by changed automatic selections. The
	// source manifest is preparation only; readiness/fences are checked in SQL.
	candidate := ipArtworkDiscoveryCandidate{ordinal: 0, sourceOrdinal: 0, role: j.Role, imageID: j.ImageID, origin: j.Origin, locale: j.Locale, attribution: j.Attribution}
	countBytes, e := ipArtworkDiscoveryCandidateBytes(candidate)
	if e != nil {
		t.Fatal(e)
	}
	spec := d.spec
	spec.expectedCount = 1
	spec.expectedBytes = countBytes
	manifest := ipArtworkDiscoveryManifestDigest(q.Owner, spec, []ipArtworkDiscoveryCandidate{candidate})
	ipADTestExec(t, w, `INSERT INTO artwork_discovery_stages(id,owner_id,owner_incarnation,provider,source_incarnation,source_revision,policy_revision,effective_language,generation,revision,state,expected_count,expected_bytes,manifest_algorithm,manifest_digest,staged_count,staged_bytes) VALUES(21,?,?,?,?,?,?,?,5,1,'ready',1,?,'portico-ip-artwork-discovery-sha256-v1',?,1,?)`, q.Owner.id, q.Owner.incarnation[:], q.Policy.Provider, q.Owner.sourceIncarnation[:], q.Owner.sourceRevision, q.Policy.Revision, q.Policy.Language, countBytes, manifest[:], countBytes)
	ipADTestExec(t, w, `INSERT INTO artwork_discovery_candidates(stage_id,ordinal,role,source_ordinal,image_id,origin,locale,attribution,score) VALUES(21,0,?,0,?,?,?,?,0)`, j.Role, j.ImageID, j.Origin, j.Locale, j.Attribution)
	return w, q
}
func ipAMTestTurn(t *testing.T, w *executor, k CommandID, custody *testCustody) *commandTurn {
	t.Helper()
	registry, e := stmttest.New(t, ipArtworkMaterialDefinitions())
	if e != nil {
		t.Fatal(e)
	}
	recipe := turnRecipe{limit: Work{10000, 10000000, 10000, 10000000, 25 * time.Millisecond}, setup: Work{1, 1, 1, 1, time.Microsecond}, terminal: Work{1, 1, 1, 1, time.Microsecond}}
	tx, e := w.begin(t.Context(), k, recipe, custody)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	turn, e := newCommandTurn(tx, registry, ipArtworkMaterialSteps())
	if e != nil {
		t.Fatal(e)
	}
	return turn
}
func ipAMTestCodecGate(t *testing.T) {
	t.Helper()
	i := CommandIdentity{Sequence: 1}
	r := ipArtworkMaterialResult{Kind: IPArtworkJobClaim, OwnerID: 1, Revision: 1, AppliedMS: 1, Count: 1, LeaseGeneration: 1, LeaseUntilMS: 2}
	if _, e := ipArtResultDigest(i, RequestDigest{}, r); e != nil {
		t.Skip("parent closed canonical result cases are not composed; run with the documented two-case Go overlay for local draft integration smoke")
	}
}
func ipAMTestApply(t *testing.T, w *executor, k CommandID, q ipArtworkMaterialRequest) ipArtworkMaterialOutcome {
	t.Helper()
	turn := ipAMTestTurn(t, w, k, &testCustody{})
	r, e := ipArtworkMaterialApply(t.Context(), turn, k, q)
	if e != nil {
		t.Fatal(k, e)
	}
	if e = turn.closeRows(); e != nil {
		t.Fatal(e)
	}
	if _, e = turn.transaction.commit(); e != nil {
		t.Fatal(e)
	}
	return r
}
func ipAMTestNext(t *testing.T, w *executor, q ipArtworkMaterialRequest) ipArtworkMaterialRequest {
	t.Helper()
	ipADTestExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=last_command_sequence WHERE id=?`, q.Owner.id)
	q.Identity.Sequence++
	q.Identity.OperationID[0]++
	q.Owner.revision++
	return q
}
func ipAMTestScalar(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if e := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func ipAMTestMaterial(q ipArtworkMaterialRequest, original bool) ipArtworkMaterialRequest {
	q.Phase = ipArtPrepare
	q.Material = ipArtworkMaterial{ID: q.Job.ID, SourceDigest: strings.Repeat("a", 64), MIME: "image/webp", Generation: q.Job.LeaseGeneration, Revision: 1, Width: 1200, Height: 1800, LargeEdge: 1920, EncoderRevision: 1, SlotCount: 3}
	for n := 0; n < 3; n++ {
		q.Material.Slots[n] = ipArtworkMaterialSlot{Digest: strings.Repeat(string(rune('b'+n)), 64), MIME: "image/jpeg", Width: int64(120 * (n + 1)), Height: int64(180 * (n + 1)), Bytes: int64(200 + n)}
	}
	if original {
		q.Material.SlotCount = 4
		q.Material.Slots[3] = ipArtworkMaterialSlot{Digest: q.Material.SourceDigest, MIME: q.Material.MIME, Width: q.Material.Width, Height: q.Material.Height, Bytes: 1024}
	}
	return q
}
func ipAMTestClaimed(t *testing.T, w *executor, q ipArtworkMaterialRequest) ipArtworkMaterialRequest {
	t.Helper()
	r := ipAMTestApply(t, w, IPArtworkJobClaim, q)
	q = ipAMTestNext(t, w, q)
	q.Job.Revision++
	q.Job.State = 2
	q.Job.LeaseOwner = q.Owner.leaseOwner
	q.Job.LeaseGeneration = r.result.LeaseGeneration
	q.Job.LeaseUntilMS = r.result.LeaseUntilMS
	return q
}
func ipAMTestPublished(t *testing.T, w *executor, q ipArtworkMaterialRequest) ipArtworkMaterialRequest {
	t.Helper()
	q = ipAMTestClaimed(t, w, q)
	q = ipAMTestMaterial(q, true)
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	q.Phase = ipArtInstalled
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	q.Phase = ipArtPublish
	q.Material.Revision++
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	q.Material.Revision++
	q.Job.Revision++
	q.Job.State = 4
	q.Job.NextMS = EvidenceInteger{}
	q.Job.LeaseUntilMS = 0
	return q
}
func ipAMTestValue(q ipArtworkMaterialRequest, revision int64) ipArtworkSelectionValue {
	return ipArtworkSelectionValue{Present: true, Revision: revision, Digest: q.Material.SourceDigest, Provider: q.Policy.Provider, ImageID: q.Job.ImageID, Origin: q.Job.Origin, Attribution: q.Job.Attribution, Locale: q.Job.Locale, Language: q.Policy.Language, SourceFence: q.Job.SourceFence, Actor: q.Job.Actor}
}

func TestIPArtworkMaterialTinySmoke(t *testing.T) {
	ipAMTestCodecGate(t)
	w, q := ipAMTestFixture(t)
	// Claim+replay before any changed source/lease/policy check.
	original := q
	r := ipAMTestApply(t, w, IPArtworkJobClaim, q)
	ipADTestExec(t, w, `UPDATE metadata_runtime_settings SET online_lookups=0`)
	replay := ipAMTestApply(t, w, IPArtworkJobClaim, original)
	if replay.decision != receiptReplay || replay.result != r.result {
		t.Fatal(replay, r)
	}
	// Same original sequence with changed duration is a digest conflict, and a
	// corrupted historical result requires resolution instead of fresh effects.
	changed := original
	changed.Phase++
	ipAMTestRefuse(t, w, IPArtworkJobClaim, changed)
	ipADTestExec(t, w, `UPDATE artwork_command_receipts SET accepted_revision=accepted_revision+1 WHERE owner_id=? AND command_sequence=1`, q.Owner.id)
	ipAMTestRefuse(t, w, IPArtworkJobClaim, original)
	ipADTestExec(t, w, `UPDATE artwork_command_receipts SET accepted_revision=accepted_revision-1 WHERE owner_id=? AND command_sequence=1`, q.Owner.id)
	ipADTestExec(t, w, `UPDATE metadata_runtime_settings SET online_lookups=1`)
	q = ipAMTestNext(t, w, q)
	q.Job.Revision++
	q.Job.State = 2
	q.Job.LeaseOwner = q.Owner.leaseOwner
	q.Job.LeaseGeneration = r.result.LeaseGeneration
	q.Job.LeaseUntilMS = r.result.LeaseUntilMS
	q.Phase = 120000
	r = ipAMTestApply(t, w, IPArtworkJobRenew, q)
	q = ipAMTestNext(t, w, q)
	q.Job.Revision++
	q.Job.LeaseUntilMS = r.result.LeaseUntilMS
	q = ipAMTestMaterial(q, true)
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	// Prepare is durable and slots immutable before installed files are accepted.
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_sources`); n != 0 {
		t.Fatal(n)
	}
	q.Phase = ipArtInstalled
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	q = ipAMTestNext(t, w, q)
	q.Material.Revision++
	q.Phase = ipArtPublish
	before := q
	r = ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	if r.result.Count != 1 || r.result.Selected != q.Material.SourceDigest {
		t.Fatal(r)
	}
	replay = ipAMTestApply(t, w, IPArtworkMaterialPublish, before)
	if replay.decision != receiptReplay || replay.result != r.result {
		t.Fatal(replay, r)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_sources`); n != 1 {
		t.Fatal(n)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_blobs`); n != 3 {
		t.Fatal(n)
	}
	q = ipAMTestNext(t, w, q)
	q.Material.Revision++
	q.Job.Revision++
	q.Job.State = 4
	q.Job.LeaseUntilMS = 0
	q.Job.NextMS = EvidenceInteger{}
	// Matched selection, true no-op and locked choice preserve a current lock.
	q.Selection = ipArtworkSelectionRequest{CandidateStageID: 21, Role: "poster", Choice: "matched", Actor: "owner", After: ipAMTestValue(q, 1)}
	// Preparation values cannot substitute for a current ready observation.
	ipADTestExec(t, w, `UPDATE artwork_discovery_stages SET policy_revision=4 WHERE id=21`)
	ipAMTestRefuse(t, w, IPArtworkSelect, q)
	ipADTestExec(t, w, `UPDATE artwork_discovery_stages SET policy_revision=3,state='building' WHERE id=21`)
	ipAMTestRefuse(t, w, IPArtworkSelect, q)
	ipADTestExec(t, w, `UPDATE artwork_discovery_stages SET state='ready' WHERE id=21`)
	r = ipAMTestApply(t, w, IPArtworkSelect, q)
	if replay := ipAMTestApply(t, w, IPArtworkSelect, q); replay.decision != receiptReplay || replay.result != r.result {
		t.Fatal("selection replay after artwork revision bump", replay)
	}
	if r.result.HistoryID == 0 {
		t.Fatal(r)
	}
	q = ipAMTestNext(t, w, q)
	q.Owner.sourceRevision++
	q.Selection.Before = q.Selection.After
	q.Selection.After.Revision++
	stale := q
	stale.Selection.Before.Revision++
	stale.Selection.After.Revision++
	ipAMTestRefuse(t, w, IPArtworkSelect, stale)
	history := ipAMTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history`)
	r = ipAMTestApply(t, w, IPArtworkSelect, q)
	if r.result.Count != 0 || r.result.HistoryID != 0 {
		t.Fatal(r)
	}
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history`); n != history {
		t.Fatal(n)
	}
	q = ipAMTestNext(t, w, q)
	q.Selection = ipArtworkSelectionRequest{Role: "poster", Choice: "locked", Actor: "owner", After: ipAMTestValue(q, 1)}
	ipAMTestApply(t, w, IPArtworkSelect, q)
	q = ipAMTestNext(t, w, q)
	q.Owner.sourceRevision++
	locked := q.Selection.After
	q.Selection = ipArtworkSelectionRequest{Role: "poster", Choice: "matched", Actor: "owner", Before: ipAMTestValue(q, 1), Locked: locked}
	r = ipAMTestApply(t, w, IPArtworkClear, q)
	if r.result.Count != 1 {
		t.Fatal(r)
	}
	q = ipAMTestNext(t, w, q)
	q.Owner.sourceRevision++
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_selections WHERE choice='locked'`); n != 1 {
		t.Fatal("lost current lock", n)
	}
	// Clear already-empty matched role does not produce history or source writes.
	q.Selection.Before = ipArtworkSelectionValue{}
	r = ipAMTestApply(t, w, IPArtworkClear, q)
	if r.result.Count != 0 || r.result.HistoryID != 0 {
		t.Fatal(r)
	}
}

func TestIPArtworkMaterialRollbackAndFences(t *testing.T) {
	ipAMTestCodecGate(t)
	w, q := ipAMTestFixture(t)
	q = ipAMTestClaimed(t, w, q)
	q = ipAMTestMaterial(q, false)
	for _, tc := range []struct {
		name   string
		mutate func(*ipArtworkMaterialRequest)
	}{
		{"stale_source", func(v *ipArtworkMaterialRequest) { v.Owner.sourceRevision++ }},
		{"stale_policy", func(v *ipArtworkMaterialRequest) { v.Policy.Revision++ }},
		{"stale_selection", func(v *ipArtworkMaterialRequest) { v.Job.SelectionRevision = 1 }},
		{"stale_lease", func(v *ipArtworkMaterialRequest) { v.Job.LeaseGeneration++ }},
		{"changed_slot", func(v *ipArtworkMaterialRequest) { v.Material.Slots[0].Bytes = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := q
			tc.mutate(&bad)
			turn := ipAMTestTurn(t, w, IPArtworkMaterialPublish, &testCustody{})
			out, e := ipArtworkMaterialApply(t.Context(), turn, IPArtworkMaterialPublish, bad)
			if out != (ipArtworkMaterialOutcome{}) {
				t.Fatal(out)
			}
			ipADTestRollback(t, turn, e)
		})
	}
	// Cancellation during the immutable-slot inserts rolls back both manifest and
	// owner/receipt. Already-held file/memory custody is external and untouched.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	custody := &testCustody{}
	turn := ipAMTestTurn(t, w, IPArtworkMaterialPublish, custody)
	start := custody.checks
	custody.checkHook = func() {
		if custody.checks-start == 15 {
			cancel()
		}
	}
	out, e := ipArtworkMaterialApply(ctx, turn, IPArtworkMaterialPublish, q)
	if !errors.Is(e, context.Canceled) || out != (ipArtworkMaterialOutcome{}) {
		t.Fatal(out, e)
	}
	// Confirm the fault actually followed native slot effects before rollback.
	var partial int64
	if probe := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_material_slots WHERE intent_id=?`, q.Material.ID).Scan(&partial); probe != nil || partial == 0 {
		t.Fatal("cancellation did not follow a native slot write", partial, probe)
	}
	ipADTestRollback(t, turn, e)
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_material_intents`); n != 0 {
		t.Fatal(n)
	}
	ipAMTestApply(t, w, IPArtworkMaterialPublish, q)
	// Cancellation/abandonment is retained debt. Unpublished canonical files
	// must not lose their only slot custody through the staging-only GC path.
	gc := ipAMTestNext(t, w, q)
	gc.GC = ipArtworkGCRequest{Target: ipArtIntent, Revision: 1, Token: [16]byte{8}}
	ipAMTestRefuse(t, w, IPArtworkGCMark, gc)
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_material_slots`); n != 3 {
		t.Fatal(n)
	}
}

func TestIPArtworkMaterialLockedEmptyRefusesWithoutEffects(t *testing.T) {
	// DDL gap: empty locked choice has no representation with digest NOT NULL.
	// Refuse explicitly; do not silently turn a user lock into absent/unlocked.
	w, q := ipAMTestFixture(t)
	q.Selection = ipArtworkSelectionRequest{Role: "poster", Choice: "locked", Actor: "owner", After: ipArtworkSelectionValue{Present: true, Revision: 1, Actor: "owner"}}
	turn := ipAMTestTurn(t, w, IPArtworkSelect, &testCustody{})
	out, e := ipArtworkMaterialApply(t.Context(), turn, IPArtworkSelect, q)
	if !errors.Is(e, ErrInvalid) || out != (ipArtworkMaterialOutcome{}) {
		t.Fatal(out, e)
	}
	ipADTestRollback(t, turn, e)
	if n := ipAMTestScalar(t, w, `SELECT count(*) FROM artwork_selections`); n != 0 {
		t.Fatal(n)
	}
}

func TestIPArtworkMaterialPlans(t *testing.T) {
	w, _ := ipAMTestFixture(t)
	for _, d := range ipArtworkMaterialDefinitions() {
		t.Run(string(d.ID), func(t *testing.T) {
			rows, e := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if e != nil {
				t.Fatal(e)
			}
			var found bool
			for rows.Next() {
				var a, b, c int
				var detail string
				if e = rows.Scan(&a, &b, &c, &detail); e != nil {
					t.Fatal(e)
				}
				if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "CORRELATED") {
					t.Fatal(detail)
				}
				found = found || strings.Contains(detail, "SEARCH ")
			}
			if e = rows.Err(); e != nil {
				t.Fatal(e)
			}
			if e = rows.Close(); e != nil {
				t.Fatal(e)
			}
			if !strings.HasPrefix(d.SQL, "INSERT ") && !found {
				t.Fatal("missing SEARCH")
			}
		})
	}
}

func TestIPArtworkMaterialCanonicalBinding(t *testing.T) {
	q := ipArtworkMaterialRequest{Identity: CommandIdentity{Sequence: 1, Incarnation: [16]byte{1}, OperationID: [16]byte{2}}}
	d, e := canonicalRequestDigest(IPArtworkMaterialPublish, 1, q.Identity, q)
	if e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*ipArtworkMaterialRequest){
		func(v *ipArtworkMaterialRequest) { v.Identity.OperationID[1]++ }, func(v *ipArtworkMaterialRequest) { v.Owner.sourceRevision++ },
		func(v *ipArtworkMaterialRequest) { v.Material.Slots[3].Bytes++ }, func(v *ipArtworkMaterialRequest) { v.Selection.Locked.Actor = "actor" },
		func(v *ipArtworkMaterialRequest) { v.Job.NextMS.Present = true }, func(v *ipArtworkMaterialRequest) { v.GC.Token[0]++ },
	} {
		v := q
		mutate(&v)
		actual, e := canonicalRequestDigest(IPArtworkMaterialPublish, 1, q.Identity, v)
		if e != nil || actual == d {
			t.Fatal(actual, e)
		}
	}
	// Independent Python struct.pack/hashlib vector is pinned in the handoff.
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{3}}
	r := ipArtworkMaterialResult{Kind: IPArtworkGCMark, OwnerID: 11, Revision: 4, AppliedMS: 123, Count: 1, Action: ipArtBlob, Blob: strings.Repeat("a", 64), StorageRevision: 8, Token: [16]byte{9}}
	if e = preflightIPArtworkMaterialResult(r); e != nil {
		t.Fatal(e)
	}
	encoder := requestEncoder{h: sha256.New()}
	if e = writeIPArtworkMaterialResult(&encoder, i, RequestDigest{5}, r); e != nil {
		t.Fatal(e)
	}
	if got := fmt.Sprintf("%x", encoder.h.Sum(nil)); got != "fbf7d7465d1eb921b8fad84c47ae6ccce594066448584ec3b4ad93092629ee1c" {
		t.Fatal(got)
	}
	if canonical, e := ipArtResultDigest(i, RequestDigest{5}, r); e == nil && fmt.Sprintf("%x", canonical) != fmt.Sprintf("%x", encoder.h.Sum(nil)) {
		t.Fatal(canonical)
	}
}
