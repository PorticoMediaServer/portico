package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// All native fixtures start with the deliberately opt-in, canonical-SHA-pinned
// draft overlay. No fixture silently substitutes the proposed DDL for adoption.
func ipADTestFixture(t *testing.T, n int) (*executor, ipArtworkDiscoveryRequest, []ipArtworkDiscoveryCandidate) {
	t.Helper()
	w := ipDraftExecutor(t)
	q := ipArtworkDiscoveryRequest{identity: CommandIdentity{Incarnation: [16]byte{71}, Sequence: 1, OperationID: [16]byte{1}},
		owner: ipArtworkDiscoveryOwner{id: 11, incarnation: [16]byte{71}, entityID: 101, sourceIncarnation: [16]byte{91}, sourceRevision: 7, revision: 1, leaseOwner: 8, leaseGeneration: 9, leaseUntilMS: w.now().Add(time.Hour).UnixMilli()},
		spec:  ipArtworkDiscoverySpec{provider: "tmdb", language: "fr-CA", policyRevision: 3, generation: 5, expectedCount: int64(n)}}
	for _, sql := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('ip-art','Art','movie','/fixture')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'ip-art','/fixture')`,
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO metadata_runtime_settings(singleton,artwork_policy_revision) VALUES(1,3)`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','fixture',1)`,
	} {
		ipADTestExec(t, w, sql)
	}
	ipADTestExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(101,?,7,1)`, q.owner.sourceIncarnation[:])
	ipADTestExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artwork_revision,metadata_language)
VALUES(101,?,7,1,'Art','art','a','art','a',1,?,?,7,'fr-CA')`, q.owner.sourceIncarnation[:], q.owner.sourceIncarnation[:], q.owner.sourceIncarnation[:])
	ipADTestExec(t, w, `INSERT INTO artwork_subject_jobs(id,entity_id,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation)
VALUES(11,101,?,7,'providers',1,8,9,?,?)`, q.owner.sourceIncarnation[:], q.owner.leaseUntilMS, q.owner.incarnation[:])
	var candidates []ipArtworkDiscoveryCandidate
	for ordinal := 0; ordinal < n; ordinal++ {
		role, locale := "poster", "fr-CA"
		if ordinal%2 == 1 {
			role, locale = "backdrop", ""
		}
		c := ipArtworkDiscoveryCandidate{ordinal: int64(ordinal), sourceOrdinal: int64(10 + ordinal/2), role: role, imageID: fmt.Sprintf("image-%d", ordinal), origin: "tmdb", locale: locale, attribution: "© fixture", score: float64(ordinal) / 3}
		b, err := ipArtworkDiscoveryCandidateBytes(c)
		if err != nil {
			t.Fatal(err)
		}
		q.spec.expectedBytes += b
		candidates = append(candidates, c)
	}
	q.spec.manifest = ipArtworkDiscoveryManifestDigest(q.owner, q.spec, candidates)
	return w, q, candidates
}
func ipADTestExec(t *testing.T, w *executor, sql string, args ...any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), sql, args...); err != nil {
		t.Fatal("fixture SQL:", sql, err)
	}
}
func ipADTestTurn(t *testing.T, w *executor, kind CommandID, custody *testCustody) *commandTurn {
	t.Helper()
	// Virtual-clock component recipe only, not a measured resource envelope.
	recipe := turnRecipe{limit: Work{10000, 10000000, 10000, 10000000, 25 * time.Millisecond}, setup: Work{1, 1, 1, 1, time.Microsecond}, terminal: Work{1, 1, 1, 1, time.Microsecond}}
	tx, err := w.begin(t.Context(), kind, recipe, custody)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	registry, err := stmttest.New(t, ipArtworkDiscoveryDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	turn, err := newCommandTurn(tx, registry, ipArtworkDiscoverySteps())
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func ipADTestApply(t *testing.T, w *executor, kind CommandID, q ipArtworkDiscoveryRequest) ipArtworkDiscoveryOutcome {
	t.Helper()
	turn := ipADTestTurn(t, w, kind, &testCustody{})
	out, err := ipArtworkDiscoveryApply(t.Context(), turn, kind, q)
	if err != nil {
		t.Fatal(err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return out
}
func ipADTestNext(t *testing.T, w *executor, q ipArtworkDiscoveryRequest, r ipArtworkDiscoveryResult) ipArtworkDiscoveryRequest {
	t.Helper()
	// Simulate W's SEPARATE durable ACK; ACK does not delete any candidate bytes.
	ipADTestExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=last_command_sequence WHERE id=?`, q.owner.id)
	q.identity.Sequence++
	q.identity.OperationID[0]++
	q.owner.revision++
	q.stageID, q.stageRevision, q.afterOrdinal = r.stageID, r.revision, r.afterOrdinal
	q.candidates = nil
	return q
}
func ipADTestStage(t *testing.T, w *executor, id int64) (revision, count, bytes int64, state string) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,staged_count,staged_bytes,state FROM artwork_discovery_stages WHERE id=?`, id).Scan(&revision, &count, &bytes, &state); err != nil {
		t.Fatal(err)
	}
	return
}
func ipADTestRollback(t *testing.T, turn *commandTurn, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected refusal")
	}
	if e := turn.closeRows(); e != nil {
		t.Fatal(e)
	}
	if _, e := turn.transaction.rollback(err); e == nil {
		t.Fatal("lost rollback refusal")
	}
}

func TestIPArtworkDiscoveryFreshReplayAndFinish(t *testing.T) {
	w, q, candidates := ipADTestFixture(t, 3)
	// A pre-existing locked current choice must survive the entire observation.
	digest := strings.Repeat("a", 64)
	ipADTestExec(t, w, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,1,'fixture')`, digest)
	ipADTestExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at)
VALUES(?,'image/jpeg',1,1,?,?,?,1920,0,0,1,'fixture')`, digest, digest, digest, digest)
	ipADTestExec(t, w, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at)
VALUES('entity',101,'poster','locked',?,'upload','owner-image','upload','','owner-fence','owner','fixture')`, digest)
	begin := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	if begin.decision != receiptFresh || begin.result.revision != 1 {
		t.Fatal(begin)
	}
	// Replay the original result BEFORE inspecting today's changed policy/source.
	ipADTestExec(t, w, `UPDATE metadata_runtime_settings SET online_lookups=0`)
	replay := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	if replay.decision != receiptReplay || replay.result != begin.result {
		t.Fatal(replay)
	}
	ipADTestExec(t, w, `UPDATE metadata_runtime_settings SET online_lookups=1`)
	q = ipADTestNext(t, w, q, begin.result)
	q.candidates = candidates
	page := ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	revision, count, bytes, state := ipADTestStage(t, w, q.stageID)
	if revision != 2 || count != 3 || bytes != q.spec.expectedBytes || state != "building" {
		t.Fatal(revision, count, bytes, state)
	}
	// The original page receipt retains its complete scalar result after changes.
	replay = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	if replay.decision != receiptReplay || replay.result != page.result {
		t.Fatal(replay)
	}
	q = ipADTestNext(t, w, q, page.result)
	finish := ipADTestApply(t, w, IPArtworkDiscoveryFinish, q)
	if finish.result.count != 3 || finish.result.revision != 3 {
		t.Fatal(finish)
	}
	_, _, _, state = ipADTestStage(t, w, q.stageID)
	if state != "ready" {
		t.Fatal(state)
	}
	var locked, discovery int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_selections WHERE choice='locked' AND image_id='owner-image' AND revision=1`).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_discovery`).Scan(&discovery); err != nil {
		t.Fatal(err)
	}
	if locked != 1 || discovery != 0 {
		t.Fatal("observation altered selection/source visibility", locked, discovery)
	}
}

func TestIPArtworkDiscoveryEmpty(t *testing.T) {
	w, q, _ := ipADTestFixture(t, 0)
	bad := q
	bad.spec.manifest[0] ^= 1
	turn := ipADTestTurn(t, w, IPArtworkDiscoveryClaim, &testCustody{})
	out, err := ipArtworkDiscoveryApply(t.Context(), turn, IPArtworkDiscoveryClaim, bad)
	if out != (ipArtworkDiscoveryOutcome{}) {
		t.Fatal(out)
	}
	ipADTestRollback(t, turn, err)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	r = ipADTestApply(t, w, IPArtworkDiscoveryFinish, q)
	if r.result.count != 0 || r.result.afterOrdinal != 0 {
		t.Fatal(r)
	}
	_, count, bytes, state := ipADTestStage(t, w, q.stageID)
	if count != 0 || bytes != 0 || state != "ready" {
		t.Fatal(count, bytes, state)
	}
}

func TestIPArtworkDiscoveryStaleAndMalformed(t *testing.T) {
	for _, test := range []struct {
		name, mutation string
		change         func(*ipArtworkDiscoveryRequest)
	}{
		{"source_revision", `UPDATE catalog_entities SET artwork_revision=artwork_revision+1`, nil},
		{"source_incarnation", `UPDATE catalog_entities SET artwork_incarnation=randomblob(16)`, nil},
		{"owner_incarnation", `UPDATE artwork_subject_jobs SET command_incarnation=randomblob(16)`, nil},
		{"owner_revision", `UPDATE artwork_subject_jobs SET job_revision=job_revision+1`, nil},
		{"owner_source", `UPDATE artwork_subject_jobs SET source_revision=source_revision+1`, nil},
		{"lease", `UPDATE artwork_subject_jobs SET job_lease_generation=job_lease_generation+1`, nil},
		{"expired", `UPDATE artwork_subject_jobs SET job_lease_expires_ms=0`, nil},
		{"policy", `UPDATE metadata_runtime_settings SET artwork_policy_revision=artwork_policy_revision+1`, nil},
		{"language", `UPDATE catalog_entities SET metadata_language='ja'`, nil},
		{"provider", `UPDATE metadata_providers SET enabled=0`, nil},
		{"namespace", "", func(q *ipArtworkDiscoveryRequest) { q.owner.personID = q.owner.entityID; q.owner.entityID = 0 }},
		{"both_namespaces", "", func(q *ipArtworkDiscoveryRequest) { q.owner.personID = q.owner.entityID }},
		{"over_budget", "", func(q *ipArtworkDiscoveryRequest) { q.spec.expectedBytes = ipArtworkDiscoveryMaxBytes + 1 }},
		{"over_count", "", func(q *ipArtworkDiscoveryRequest) { q.spec.expectedCount = ipArtworkDiscoveryMaxCandidates + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, q, _ := ipADTestFixture(t, 1)
			if test.mutation != "" {
				ipADTestExec(t, w, test.mutation)
			}
			if test.change != nil {
				test.change(&q)
			}
			turn := ipADTestTurn(t, w, IPArtworkDiscoveryClaim, &testCustody{})
			out, err := ipArtworkDiscoveryApply(t.Context(), turn, IPArtworkDiscoveryClaim, q)
			if out != (ipArtworkDiscoveryOutcome{}) {
				t.Fatal("refusal leaked output", out)
			}
			ipADTestRollback(t, turn, err)
			var n int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_discovery_stages`).Scan(&n); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		})
	}
}

func TestIPArtworkDiscoveryRawPagesAndDuplicateRollback(t *testing.T) {
	w, q, candidates := ipADTestFixture(t, 18)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = candidates[:16]
	r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = append([]ipArtworkDiscoveryCandidate(nil), candidates[16:]...)
	original := q.candidates[1]
	// First row inserts; second violates an EARLIER page's role/image uniqueness.
	q.candidates[1].imageID = candidates[1].imageID
	turn := ipADTestTurn(t, w, IPArtworkDiscoveryPage, &testCustody{})
	out, err := ipArtworkDiscoveryApply(t.Context(), turn, IPArtworkDiscoveryPage, q)
	if out != (ipArtworkDiscoveryOutcome{}) {
		t.Fatal("duplicate leaked prefix", out)
	}
	ipADTestRollback(t, turn, err)
	_, count, bytes, _ := ipADTestStage(t, w, q.stageID)
	var prefixBytes int64
	for _, c := range candidates[:16] {
		b, _ := ipArtworkDiscoveryCandidateBytes(c)
		prefixBytes += b
	}
	if count != 16 || bytes != prefixBytes {
		t.Fatal("rollback retained/refunded prefix", count, bytes)
	}
	var tail int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal>=16`, q.stageID).Scan(&tail); err != nil || tail != 0 {
		t.Fatal(tail, err)
	}
	q.candidates[1] = original
	r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	q = ipADTestNext(t, w, q, r.result)
	r = ipADTestApply(t, w, IPArtworkDiscoveryFinish, q)
	if r.result.afterOrdinal != 18 {
		t.Fatal(r)
	}
	var source int64
	var locale, origin string
	if err = w.db.QueryRowContext(t.Context(), `SELECT source_ordinal,locale,origin FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal=17`, q.stageID).Scan(&source, &locale, &origin); err != nil {
		t.Fatal(err)
	}
	if source != 18 || locale != "" || origin != "tmdb" {
		t.Fatal("raw/provider/locale lost", source, locale, origin)
	}
}

func TestIPArtworkDiscoveryFinishRequiresManifestAndNoNext(t *testing.T) {
	for _, test := range []struct{ name, sql string }{
		{"changed_manifest", `UPDATE artwork_discovery_candidates SET attribution='xxxxxxxxxx' WHERE ordinal=0`},
		{"extra_raw_key", `INSERT INTO artwork_discovery_candidates SELECT stage_id,99,role,99,'extra',origin,locale,attribution,score FROM artwork_discovery_candidates WHERE ordinal=0`},
		{"missing_raw_key", `DELETE FROM artwork_discovery_candidates WHERE ordinal=0`},
		{"oversized_native_text", `UPDATE artwork_discovery_candidates SET attribution=printf('%.*c',16385,'x') WHERE ordinal=0`},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, q, candidates := ipADTestFixture(t, 1)
			r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
			q = ipADTestNext(t, w, q, r.result)
			q.candidates = candidates
			r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
			q = ipADTestNext(t, w, q, r.result)
			ipADTestExec(t, w, test.sql)
			turn := ipADTestTurn(t, w, IPArtworkDiscoveryFinish, &testCustody{})
			out, err := ipArtworkDiscoveryApply(t.Context(), turn, IPArtworkDiscoveryFinish, q)
			if out != (ipArtworkDiscoveryOutcome{}) {
				t.Fatal(out)
			}
			ipADTestRollback(t, turn, err)
			revision, _, _, state := ipADTestStage(t, w, q.stageID)
			if state != "building" || revision != 2 {
				t.Fatal(revision, state)
			}
		})
	}
}

func TestIPArtworkDiscoveryCancellationRollsBack(t *testing.T) {
	w, q, candidates := ipADTestFixture(t, 2)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = candidates
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	custody := &testCustody{}
	turn := ipADTestTurn(t, w, IPArtworkDiscoveryPage, custody)
	start := custody.checks
	custody.checkHook = func() {
		if custody.checks-start == 11 {
			cancel()
		}
	}
	out, err := ipArtworkDiscoveryApply(ctx, turn, IPArtworkDiscoveryPage, q)
	if !errors.Is(err, context.Canceled) || out != (ipArtworkDiscoveryOutcome{}) {
		t.Fatal(out, err)
	}
	ipADTestRollback(t, turn, err)
	revision, count, bytes, _ := ipADTestStage(t, w, q.stageID)
	if revision != 1 || count != 0 || bytes != 0 {
		t.Fatal(revision, count, bytes)
	}
}

func TestIPArtworkDiscoveryAbandonedReclaim(t *testing.T) {
	w, q, candidates := ipADTestFixture(t, 18)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = candidates[:16]
	r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = candidates[16:]
	r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	// Isolate the last UNACKNOWLEDGED proof: retire only ACKed predecessors.
	ipADTestExec(t, w, `DELETE FROM artwork_command_receipts WHERE owner_id=? AND command_sequence<?`, q.owner.id, q.identity.Sequence)
	// Preserve the last UNACKNOWLEDGED receipt and its candidate proof.
	q.stageID, q.stageRevision, q.afterOrdinal = r.result.stageID, r.result.revision, 0
	q.candidates = nil
	q.owner.revision++
	ipADTestExec(t, w, `UPDATE artwork_discovery_stages SET state='retiring',reclaim_due_ms=0 WHERE id=?`, q.stageID)
	turn := ipADTestTurn(t, w, IPAbandonedReclaim, &testCustody{})
	out, err := ipArtworkDiscoveryReclaim(t.Context(), turn, q)
	if out != (ipArtworkDiscoveryReclaimResult{}) {
		t.Fatal(out)
	}
	ipADTestRollback(t, turn, err)
	_, count, bytes, _ := ipADTestStage(t, w, q.stageID)
	if count != 18 || bytes != q.spec.expectedBytes {
		t.Fatal("pin refunded", count, bytes)
	}
	// ACK alone still retains the pin; only explicit receipt retirement releases it.
	ipADTestExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=last_command_sequence WHERE id=?`, q.owner.id)
	turn = ipADTestTurn(t, w, IPAbandonedReclaim, &testCustody{})
	_, err = ipArtworkDiscoveryReclaim(t.Context(), turn, q)
	ipADTestRollback(t, turn, err)
	ipADTestExec(t, w, `DELETE FROM artwork_command_receipts WHERE owner_id=?`, q.owner.id)
	// Abandoned debt remains reclaimable after its source/lease becomes obsolete.
	ipADTestExec(t, w, `UPDATE artwork_subject_jobs SET source_revision=8,job_lease_expires_ms=0 WHERE id=?`, q.owner.id)
	turn = ipADTestTurn(t, w, IPAbandonedReclaim, &testCustody{})
	out, err = ipArtworkDiscoveryReclaim(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if out.deletedCount != 16 || out.done || out.afterOrdinal != 15 {
		t.Fatal(out)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	_, count, bytes, state := ipADTestStage(t, w, q.stageID)
	if count != 2 || bytes != q.spec.expectedBytes-out.deletedBytes || state != "retiring" {
		t.Fatal(count, bytes, state)
	}
	q.stageRevision = out.revision
	q.afterOrdinal = out.afterOrdinal + 1
	q.owner.revision++
	turn = ipADTestTurn(t, w, IPAbandonedReclaim, &testCustody{})
	out, err = ipArtworkDiscoveryReclaim(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if out.deletedCount != 2 || !out.done || out.afterOrdinal != 17 {
		t.Fatal(out)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	_, count, bytes, state = ipADTestStage(t, w, q.stageID)
	if count != 0 || bytes != 0 || state != "done" {
		t.Fatal(count, bytes, state)
	}
}

func TestIPArtworkDiscoveryPlans(t *testing.T) {
	w, _, _ := ipADTestFixture(t, 0)
	for _, d := range ipArtworkDiscoveryDefinitions() {
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		found := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "CORRELATED") {
				t.Fatal(d.ID, detail)
			}
			found = found || strings.Contains(detail, "SEARCH ")
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(d.SQL, "INSERT ") && !found {
			t.Fatal("missing indexed SEARCH", d.ID)
		}
	}
}

func TestIPArtworkDiscoveryProtectedRowsError(t *testing.T) {
	w, q, candidates := ipADTestFixture(t, 2)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = candidates
	r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	turn := ipADTestTurn(t, w, IPArtworkDiscoveryFinish, &testCustody{})
	// Test-only native fault variant: fail DURING the second step, after a raw
	// key has been copied. No fault SQL enters the finite production catalogue.
	definitions := ipArtworkDiscoveryDefinitions()
	for n := range definitions {
		if definitions[n].ID == ipADKeys {
			definitions[n].SQL = `SELECT CASE WHEN ordinal=1 THEN abs(-9223372036854775808) ELSE ordinal END FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`
		}
	}
	registry, err := stmttest.New(t, definitions)
	if err != nil {
		t.Fatal(err)
	}
	turn.registry = registry
	keys, err := ipArtworkDiscoveryKeys(t.Context(), turn, q.stageID, -1, 3)
	if err == nil || keys != nil {
		t.Fatal("Rows.Err released a prefix", keys, err)
	}
	if len(turn.rows) != 1 || turn.rows[0].Next() {
		t.Fatal("errored rows survived protected Close")
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if len(turn.rows) != 0 {
		t.Fatal("frame failed to drain closed rows")
	}
	_, _ = turn.transaction.rollback(nil)
}

func TestIPArtworkDiscoveryInputPageControls(t *testing.T) {
	// One native fixture, no per-permutation bootstraps; all refusals are before
	// effects and leave the same exact original request sequence available.
	w, q, candidates := ipADTestFixture(t, 18)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	for _, test := range []struct {
		name  string
		rows  []ipArtworkDiscoveryCandidate
		after int64
	}{
		{"over_chunk", candidates[:17], 0},
		{"wrong_raw_cursor", candidates[:1], 1},
		{"empty_chunk", nil, 0},
		{"duplicate_role_image", []ipArtworkDiscoveryCandidate{candidates[0], {ordinal: 1, sourceOrdinal: 11, role: "poster", imageID: candidates[0].imageID}}, 0},
		{"duplicate_role_source", []ipArtworkDiscoveryCandidate{candidates[0], {ordinal: 1, sourceOrdinal: candidates[0].sourceOrdinal, role: "poster", imageID: "other"}}, 0},
		{"malformed_role", []ipArtworkDiscoveryCandidate{{ordinal: 0, sourceOrdinal: 0, role: " Poster ", imageID: "id"}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := q
			input.candidates = test.rows
			input.afterOrdinal = test.after
			turn := ipADTestTurn(t, w, IPArtworkDiscoveryPage, &testCustody{})
			out, err := ipArtworkDiscoveryApply(t.Context(), turn, IPArtworkDiscoveryPage, input)
			if out != (ipArtworkDiscoveryOutcome{}) {
				t.Fatal(out)
			}
			ipADTestRollback(t, turn, err)
			revision, count, bytes, _ := ipADTestStage(t, w, q.stageID)
			if revision != 1 || count != 0 || bytes != 0 {
				t.Fatal(revision, count, bytes)
			}
		})
	}
}

func TestIPArtworkDiscoveryReclaimRollback(t *testing.T) {
	w, q, candidates := ipADTestFixture(t, 2)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = candidates
	r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	q.stageID, q.stageRevision, q.afterOrdinal = r.result.stageID, r.result.revision, 0
	q.candidates = nil
	q.owner.revision++
	ipADTestExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=last_command_sequence WHERE id=?`, q.owner.id)
	ipADTestExec(t, w, `DELETE FROM artwork_command_receipts WHERE owner_id=?`, q.owner.id)
	ipADTestExec(t, w, `UPDATE artwork_discovery_stages SET state='retiring',reclaim_due_ms=0 WHERE id=?`, q.stageID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	custody := &testCustody{}
	turn := ipADTestTurn(t, w, IPAbandonedReclaim, custody)
	start := custody.checks
	custody.checkHook = func() {
		if custody.checks-start == 8 {
			cancel()
		}
	}
	out, err := ipArtworkDiscoveryReclaim(ctx, turn, q)
	if !errors.Is(err, context.Canceled) || out != (ipArtworkDiscoveryReclaimResult{}) {
		t.Fatal(out, err)
	}
	ipADTestRollback(t, turn, err)
	revision, count, bytes, state := ipADTestStage(t, w, q.stageID)
	if revision != 2 || count != 2 || bytes != q.spec.expectedBytes || state != "retiring" {
		t.Fatal("refunded rolled-back deletes", revision, count, bytes, state)
	}
	var rows int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_discovery_candidates WHERE stage_id=?`, q.stageID).Scan(&rows); err != nil || rows != 2 {
		t.Fatal(rows, err)
	}
}

func TestIPArtworkDiscoveryPersonNamespace(t *testing.T) {
	w, q, candidates := ipADTestFixture(t, 1)
	// Both namespaces deliberately use integer101 and the same source bytes.
	ipADTestExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,artwork_revision) VALUES(101,'person-101','person-key','Person','person',?,7)`, q.owner.sourceIncarnation[:])
	ipADTestExec(t, w, `UPDATE artwork_subject_jobs SET entity_id=NULL,person_id=101 WHERE id=?`, q.owner.id)
	turn := ipADTestTurn(t, w, IPArtworkDiscoveryClaim, &testCustody{})
	out, err := ipArtworkDiscoveryApply(t.Context(), turn, IPArtworkDiscoveryClaim, q)
	if out != (ipArtworkDiscoveryOutcome{}) {
		t.Fatal(out)
	}
	ipADTestRollback(t, turn, err)
	q.owner.entityID, q.owner.personID = 0, 101
	q.spec.language = "en"
	q.spec.manifest = ipArtworkDiscoveryManifestDigest(q.owner, q.spec, candidates)
	r := ipADTestApply(t, w, IPArtworkDiscoveryClaim, q)
	q = ipADTestNext(t, w, q, r.result)
	q.candidates = candidates
	r = ipADTestApply(t, w, IPArtworkDiscoveryPage, q)
	q = ipADTestNext(t, w, q, r.result)
	r = ipADTestApply(t, w, IPArtworkDiscoveryFinish, q)
	if r.result.count != 1 {
		t.Fatal(r)
	}
}
