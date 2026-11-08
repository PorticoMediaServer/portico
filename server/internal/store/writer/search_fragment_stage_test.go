package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// SQL/protocol component controls, not governor admission or Work qualification.
// W's dedicated fragment grant/partition fixture replaces this synthetic custody
// for resource integration. There is no image-purpose turn or helper borrowing.
func searchFragmentNativeFixture(t *testing.T) (*executor, *stmt.Registry, *searchFragmentStageCommand) {
	t.Helper()
	w, c := searchFragmentNativeSeed(t)
	registry := cachedNativeInventoryFixtureRegistry(t, w, searchFragmentDefinitions(), "search_fragment_stage_test.go", "search_fragment_statements.go")
	return w, registry, c
}

func searchFragmentNativeSeed(t *testing.T) (*executor, *searchFragmentStageCommand) {
	t.Helper()
	w := searchImageStagePlanFixture(t)
	s, rows := searchFragmentStageSnapshotFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	p := s.bank.profile
	exec(`UPDATE search_generations SET recipe_version=?,engine_version=?,unicode_fingerprint=? WHERE id=7`, p.RecipeVersion, p.EngineVersion, p.UnicodeFingerprint)
	exec(`INSERT INTO search_publications(id,entity_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title_fold,title_prefix_tokens,initial_prefix_tokens)
VALUES(307,41,7,3,1,5,?,11,?,'','','')`, s.source.incarnation[:], p.RecipeVersion)
	exec(`INSERT INTO search_image_seals(publication_id,algorithm,seed,expected_digest,expected_fragments,expected_bytes,chain)
VALUES(307,?,?,?,?,?,?)`, s.seal.algorithm, s.seal.seed[:], s.seal.expectedDigest[:], s.seal.expectedFragments, s.seal.expectedBytes, s.seal.chain[:])
	reclaimIncarnation := [16]byte{51}
	exec(`INSERT INTO search_reclaim_jobs(publication_id,live_publication_id,phase,command_incarnation) VALUES(307,307,'held',?)`, reclaimIncarnation[:])
	exec(`UPDATE search_publish_jobs SET document_id=307,phase='stage',expected_fragments=? WHERE id=29`, s.seal.expectedFragments)
	exec(`UPDATE search_publish_receipts SET command_kind='fragment_stage',fragment_id=2050+publish_job_id,
after_field=1,after_ordinal=0,after_fragment_id=2050+publish_job_id,staged_fragments=1,staged_bytes=8 WHERE publish_job_id IN(30,31)`)
	c, err := newSearchFragmentStageCommand(CommandIdentity{s.job.incarnation, 8, [16]byte{71}}, searchFragmentStageRequest{"fragment_stage", s, rows[0].row})
	if err != nil {
		t.Fatal(err)
	}
	return w, c
}

func searchFragmentComponentTurn(t *testing.T, w *executor, r *stmt.Registry) *commandTurn {
	t.Helper()
	// Reuse W's existing frame/test recipe; these small virtual-clock numbers
	// exercise finite step accounting only, not measured native-cost bounds.
	tx, err := w.begin(t.Context(), SearchFragmentStage, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	if tx.settings.Synchronous != int(sqliteconn.Normal) {
		t.Fatal("fragment command lost NORMAL policy", tx.settings)
	}
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{1, 100, 1, 100, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestSearchFragmentNativeStreamsCountersAndOriginalReplay(t *testing.T) {
	w, registry, c := searchFragmentNativeFixture(t)
	turn := searchFragmentComponentTurn(t, w, registry)
	native, err := searchFragmentStageFreshFacts(t.Context(), turn, c.request.snapshot)
	if err != nil || native != c.request.snapshot {
		t.Fatal("captured native vector differs", native, err)
	}
	before := searchImageSharedFacts(t, turn)
	out, err := writeSearchFragmentStage(t.Context(), turn, c, native, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if out.AcceptedJobRevision != 20 || out.FragmentID <= 0 || out.AfterFragmentID != out.FragmentID || out.StagedFragments != 1 || out.VerifiedFragments != 0 || out.SealChain != native.seal.chain || out.Outcome != (ReceiptOutcome{}) {
		t.Fatal("not the actual immutable fragment result", out)
	}
	var prefix, lexical, normalized, revision string
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT prefix_tokens,lexical_tokens,normalized,source_revision FROM search_fragments WHERE id=?`, out.FragmentID).Scan(&prefix, &lexical, &normalized, &revision); err != nil {
		t.Fatal(err)
	}
	if prefix != c.payload.row.Prefix || lexical != c.payload.row.Lexical || normalized != c.payload.row.Normalized || revision != "11" {
		t.Fatal("independent raw streams changed")
	}
	for _, q := range []string{
		`SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH '006771004eac'`,
		`SELECT rowid FROM search_fragment_lexical WHERE search_fragment_lexical MATCH '東京'`,
	} {
		var actual int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), q).Scan(&actual); err != nil || actual != out.FragmentID {
			t.Fatal("native stream missing or has an invented rowid", actual, err)
		}
	}
	var jobCount, jobBytes, sealCount, sealBytes, verified, sealCursor int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT staged_fragments,staged_bytes FROM search_publish_jobs WHERE id=29`).Scan(&jobCount, &jobBytes); err != nil {
		t.Fatal(err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT staged_fragments,staged_bytes,verified_fragments,after_fragment_id FROM search_image_seals WHERE publication_id=307`).Scan(&sealCount, &sealBytes, &verified, &sealCursor); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 || sealCount != jobCount || jobBytes != out.StagedBytes || sealBytes != jobBytes || verified != 0 || sealCursor != 0 || searchImageSharedFacts(t, turn) != before {
		t.Fatal("staging changed verification/head/global/source state", jobCount, sealCount, jobBytes, sealBytes, verified, sealCursor)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// Retire the native fragment, change source/profile and exhaust the live
	// revision. Replay still returns the ORIGINAL saved scalars before fresh
	// compatibility, lease, image or fragment access; no result reconstruction.
	for _, sql := range []string{
		`DELETE FROM search_fragments`,
		`UPDATE catalog_entities SET revision=revision+1 WHERE id=41`,
		`UPDATE search_generations SET unicode_fingerprint='incompatible' WHERE id=7`,
		`UPDATE search_publish_jobs SET job_revision=9223372036854775807,job_lease_expires_ms=0,input_generation=99 WHERE id=29`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	turn = searchFragmentComponentTurn(t, w, registry)
	// Keep ONLY original receipt reads. Fresh and engine/resource methods
	// cannot run (draft W resource methods are deliberately not present).
	for id := range turn.steps {
		if id != searchPublisherOwnerID && id != searchPublisherIdentity && id != searchFragmentReceiptRead {
			delete(turn.steps, id)
		}
	}
	again, err := applySearchFragmentStage(t.Context(), turn, c)
	if err != nil || again != out {
		t.Fatal("replay rechecked fresh state or invented result", again, out, err)
	}
}

func TestSearchFragmentNativeConflictRollsBackEveryEffect(t *testing.T) {
	w, registry, c := searchFragmentNativeFixture(t)
	// Drift only the seal CAS after the captured/prepared vector. The earlier
	// ordinary and three native INSERTs must all disappear on frame rollback.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_image_seals SET expected_bytes=expected_bytes+1 WHERE publication_id=307`); err != nil {
		t.Fatal(err)
	}
	turn := searchFragmentComponentTurn(t, w, registry)
	out, err := writeSearchFragmentStage(t.Context(), turn, c, c.request.snapshot, 1000)
	if !errors.Is(err, ErrConflict) || out != (searchFragmentStageResult{}) {
		t.Fatal("conflicting seal released result", out, err)
	}
	if _, err := turn.transaction.rollback(err); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var fragments, receipts, revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM search_fragments),(SELECT count(*) FROM search_publish_receipts WHERE publish_job_id=29),job_revision FROM search_publish_jobs WHERE id=29`).Scan(&fragments, &receipts, &revision); err != nil {
		t.Fatal(err)
	}
	if fragments != 0 || receipts != 0 || revision != 19 {
		t.Fatal("partial effects survived failed command", fragments, receipts, revision)
	}
}

func TestSearchFragmentHistoryCancellationClearsDecisionAndResult(t *testing.T) {
	w, registry, c := searchFragmentNativeFixture(t)
	turn := searchFragmentComponentTurn(t, w, registry)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	q := PublisherReceiptRequest{JobID: 29, LibraryID: 3, EntityID: 41, Receipt: ReceiptReference{c.identity, c.digest}}
	out, decision, err := lookupSearchFragmentStage(ctx, turn, q)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, searchFragmentStageResult{}) || decision != 0 {
		t.Fatal("canceled lookup leaked decision/result", out, decision, err)
	}
}

func TestSearchFragmentNativeEmptyStreamsAndFreshHeadDrift(t *testing.T) {
	w, registry, c := searchFragmentNativeFixture(t)
	// Empty columns are a real fragment. They do not imply an empty image or
	// a completed/verified seal, and all three fixed-slot INSERTs still run.
	r := c.request
	r.row.Prefix, r.row.Lexical, r.row.Normalized = "", "", ""
	empty, err := newSearchFragmentStageCommand(c.identity, r)
	if err != nil {
		t.Fatal(err)
	}
	turn := searchFragmentComponentTurn(t, w, registry)
	out, err := writeSearchFragmentStage(t.Context(), turn, empty, r.snapshot, 1000)
	if err != nil || out.StagedFragments != 1 || out.StagedBytes != 0 || out.VerifiedFragments != 0 {
		t.Fatal("empty columns disappeared or claimed verification", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// A captured NULL building pointer cannot be treated as a wildcard.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
SELECT 8,'v2',recipe_version,engine_version,unicode_fingerprint,'building',created_ms FROM search_generations WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_revision SET building_generation_id=8 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	turn = searchFragmentComponentTurn(t, w, registry)
	facts, err := searchFragmentStageFreshFacts(t.Context(), turn, c.request.snapshot)
	if !errors.Is(err, ErrConflict) || facts != (searchFragmentStageSnapshot{}) {
		t.Fatal("stale exact head pair admitted", facts, err)
	}
}
