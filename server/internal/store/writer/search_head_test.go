package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func searchHeadGDriverProof(t *testing.T, turn *commandTurn) string {
	t.Helper()
	var proof []any
	for _, q := range []string{`SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM search_generations WHERE id=7`, `SELECT * FROM search_generation_receipts WHERE search_generation_id=7 ORDER BY command_sequence`} {
		rows, err := turn.transaction.conn.QueryContext(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for n := range values {
				targets[n] = &values[n]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			proof = append(proof, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Reuses W's existing test executor/frame and synthetic recipe. It grants no
// purpose memory/engine/issuer authority and measures no qualified native Work.
func searchPublisherComponentTurn(t *testing.T, w *executor, r *stmt.Registry, purpose CommandID) *commandTurn {
	t.Helper()
	if purpose != SearchHeadPublish && purpose != SearchFragmentReclaim {
		t.Fatal("unclosed component purpose")
	}
	tx, err := w.begin(t.Context(), purpose, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	if tx.settings.Synchronous != int(sqliteconn.Normal) {
		t.Fatal("component not NORMAL")
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

func searchHeadRequestFixture(t *testing.T) (searchHeadPublishRequest, CommandIdentity) {
	t.Helper()
	s := searchHeadPublishSnapshotFixture(t)
	r := searchHeadPublishRequest{variant: "head_publish", snapshot: s, old: searchOwnPrefixImage{13, "00006f00006c000064", "00006f00006c000064"}, next: searchOwnPrefixImage{307, "006771004eac", "006771004eac"},
		retirement: searchImageRetirementWitness{id: 2, publicationID: 13, livePublicationID: sql.NullInt64{Int64: 13, Valid: true}, phase: "held", incarnation: [16]byte{61}, revision: 1}}
	return r, CommandIdentity{s.stage.job.incarnation, 8, [16]byte{81}}
}

func searchHeadNativeSeed(t *testing.T) (*executor, *searchHeadPublishCommand) {
	t.Helper()
	w, _ := searchFragmentNativeSeed(t)
	r, identity := searchHeadRequestFixture(t)
	s := r.snapshot
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE catalog_entities SET title='東京' WHERE id=41`)
	exec(`UPDATE search_publications SET title='Old',source_revision=9,title_prefix_tokens=?,initial_prefix_tokens=? WHERE id=13`, r.old.prefix, r.old.initial)
	exec(`UPDATE search_publications SET title='東京',title_prefix_tokens=?,initial_prefix_tokens=? WHERE id=307`, r.next.prefix, r.next.initial)
	exec(`INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)`, int64(41), r.old.prefix, r.old.initial)
	exec(`UPDATE search_publish_jobs SET phase='publish',staged_fragments=?,staged_bytes=?,after_field=?,after_ordinal=?,after_fragment_id=? WHERE id=29`, s.stage.seal.stagedFragments, s.stage.seal.stagedBytes, s.stage.seal.afterField, s.stage.seal.afterOrdinal, s.stage.seal.afterFragmentID)
	exec(`UPDATE search_image_seals SET staged_fragments=?,staged_bytes=?,verified_fragments=?,verified_bytes=?,chain=?,after_field=?,after_ordinal=?,after_fragment_id=?,sealed=1 WHERE publication_id=307`, s.stage.seal.stagedFragments, s.stage.seal.stagedBytes, s.stage.seal.verifiedFragments, s.stage.seal.verifiedBytes, s.stage.seal.chain[:], s.stage.seal.afterField, s.stage.seal.afterOrdinal, s.stage.seal.afterFragmentID)
	exec(`INSERT INTO search_reclaim_jobs(id,publication_id,live_publication_id,phase,command_incarnation) VALUES(2,13,13,'held',?)`, r.retirement.incarnation[:])
	exec(`UPDATE search_revision SET revision=19,input_sequence=71 WHERE singleton=1`)
	exec(`UPDATE search_generations SET pending_subjects=3,indexed_rows=17,indexed_bytes=29,job_revision=9223372036854775807,job_lease_owner=41,job_lease_generation=43,job_lease_expires_ms=2100000000001,after_entity_id=47,after_person_id=53,after_reading_id=59,after_table=7,captured_input_sequence=61,phase='idle',job_next_attempt_ms=67,last_command_sequence=3,acknowledged_command_sequence=2 WHERE id=7`)
	var ginc []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT command_incarnation FROM search_generations WHERE id=7`).Scan(&ginc); err != nil {
		t.Fatal(err)
	}
	op, digest := [16]byte{93}, [32]byte{94}
	exec(`INSERT INTO search_generation_receipts(search_generation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,active_generation_id,building_generation_id,input_sequence,after_entity_id,after_person_id,after_reading_id,after_table) VALUES(7,?,3,?,?,'advance',9223372036854775807,1000,7,NULL,71,47,53,59,7)`, ginc, op[:], digest[:])
	_, rows := searchFragmentStageSnapshotFixture(t)
	for n, p := range rows {
		id := int64(501 + n)
		row := p.row
		exec(`INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(?,41,NULL,?,?,?,?,?,?,5,7,307)`, id, int64(row.Field), row.Ordinal, row.Revision, row.Prefix, row.Lexical, row.Normalized)
		exec(`INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)`, id, row.Prefix)
		exec(`INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)`, id, row.Lexical)
		exec(`INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)`, id, row.Normalized)
	}
	c, err := newSearchHeadPublishCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	return w, c
}

func searchHeadNativeFixture(t *testing.T) (*executor, *stmt.Registry, *searchHeadPublishCommand) {
	t.Helper()
	w, c := searchHeadNativeSeed(t)
	r := nativeInventoryFixtureRegistry(t, w, searchHeadDefinitions(), "search_head_test.go", "search_head_statements.go")
	return w, r, c
}

func TestSearchHeadCommandCompleteBoundAndDigest(t *testing.T) {
	e := requestEncoder{h: sha256.New()}
	e.value(reflect.ValueOf(searchHeadPublishRequest{}), 0)
	fixed := e.written + uint64(len(canonicalRequestCodec)+4+16+8+16)
	if e.err != nil || fixed > searchHeadPublishFixedBytes {
		t.Fatalf("head fixed wire %d exceeds preflight %d: %v", fixed, searchHeadPublishFixedBytes, e.err)
	}
	t.Logf("actual shared fixed wire=%d, preflight ceiling=%d", fixed, searchHeadPublishFixedBytes)
	r, identity := searchHeadRequestFixture(t)
	c, err := newSearchHeadPublishCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planSearchHeadPublish(c.request.snapshot); err != nil {
		t.Fatal(err)
	}
	copy := c.request
	copy.old.prefix = "000061"
	digest, err := canonicalRequestDigest(SearchHeadPublish, 1, c.identity, copy)
	if err != nil || digest == c.digest {
		t.Fatal("old original tokens omitted from request", err)
	}
	copy = c.request
	copy.retirement.leaseGeneration++
	digest, err = canonicalRequestDigest(SearchHeadPublish, 1, c.identity, copy)
	if err != nil || digest == c.digest {
		t.Fatal("old custody owner omitted from request", err)
	}
	// The complete preflight pointer inventory must cover EVERY retained TEXT.
	addresses := map[uintptr]bool{}
	for _, p := range searchHeadTexts(&r) {
		addresses[uintptr(unsafe.Pointer(p))] = true
	}
	stringsFound := 0
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			stringsFound++
			if !addresses[v.UnsafeAddr()] {
				t.Fatal("retained string omitted from preflight", v.Type())
			}
		}
	})
	if stringsFound != len(addresses) {
		t.Fatal("preflight string coverage", stringsFound, len(addresses))
	}
}

func TestSearchHeadExistingReclaimDueIsNeverDelayed(t *testing.T) {
	w, _, c := searchHeadNativeFixture(t)
	r := c.request
	r.retirement.nextAttempt = sql.NullInt64{Int64: 500, Valid: true}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_reclaim_jobs SET job_next_attempt_ms=500 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	c, err := newSearchHeadPublishCommand(c.identity, r)
	if err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, searchHeadDefinitions(), "search_head_test.go", "search_head_statements.go")
	turn := searchPublisherComponentTurn(t, w, registry, SearchHeadPublish)
	native, err := searchHeadPublishFreshFacts(t.Context(), turn, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeSearchHeadPublish(t.Context(), turn, c, native, 1000); err != nil {
		t.Fatal(err)
	}
	var due int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT job_next_attempt_ms FROM search_reclaim_jobs WHERE id=2`).Scan(&due); err != nil || due != 500 {
		t.Fatal("flip postponed earlier reclaim", due, err)
	}
}

func TestSearchHeadNativeFlipAndSavedReplay(t *testing.T) {
	w, registry, c := searchHeadNativeFixture(t)
	turn := searchPublisherComponentTurn(t, w, registry, SearchHeadPublish)
	native, err := searchHeadPublishFreshFacts(t.Context(), turn, c.request)
	if err != nil {
		t.Fatal(err)
	}
	before := searchImageSharedFacts(t, turn)
	proofBefore := searchHeadGDriverProof(t, turn)
	started := time.Now()
	out, err := writeSearchHeadPublish(t.Context(), turn, c, native, 1000)
	bodyEnd := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if out.AcceptedJobRevision != 20 || out.ImageID != 307 || out.PublishedGeneration != 5 || out.StagedFragments != 3 || out.VerifiedFragments != 3 || out.Outcome != (ReceiptOutcome{}) {
		t.Fatal(out)
	}
	if after := searchHeadGDriverProof(t, turn); after != proofBefore {
		t.Fatal("Gdriver command identity/ACK/receipt bytes changed")
	}
	want := before
	want.serving++
	want.pending--
	if after := searchImageSharedFacts(t, turn); after != want {
		t.Fatal("wrong serving/pending or Gdriver/input mutation", before, after)
	}
	var head, stage sql.NullInt64
	var phase string
	var due sql.NullInt64
	var cursor int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT published_document_id,document_id,phase,job_next_attempt_ms,after_fragment_id FROM search_publish_jobs WHERE id=29`).Scan(&head, &stage, &phase, &due, &cursor); err != nil {
		t.Fatal(err)
	}
	if head.Int64 != 307 || !head.Valid || stage.Valid || phase != "idle" || due.Valid || cursor != 503 {
		t.Fatal("stage/due/cursor transition", head, stage, phase, due, cursor)
	}
	var revision, last, ack, lease, live int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT phase,job_revision,job_next_attempt_ms,last_command_sequence,acknowledged_command_sequence,job_lease_owner,live_publication_id FROM search_reclaim_jobs WHERE id=2`).Scan(&phase, &revision, &due, &last, &ack, &lease, &live); err != nil {
		t.Fatal(err)
	}
	if phase != "ready" || revision != 2 || !due.Valid || due.Int64 != 1000 || last != 0 || ack != 0 || lease != 0 || live != 13 {
		t.Fatal("old image lost durable bounded reclaim custody")
	}
	var rowid int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH '006771004eac'`).Scan(&rowid); err != nil || rowid != 41 {
		t.Fatal("new native own prefix missing", err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH '00006f00006c000064'`).Scan(&rowid); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old native own token survived", err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	commitStart := time.Now()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	returned := time.Now()
	t.Logf("component only IDs job=%d oldImage=13 newImage=%d oldBytes=%d newBytes=%d body=%s commit+return=%s whole=%s; virtual resource clock, NOT hold qualification", out.JobID, out.ImageID, len(c.request.old.prefix)+len(c.request.old.initial), len(c.request.next.prefix)+len(c.request.next.initial), bodyEnd.Sub(started), returned.Sub(commitStart), returned.Sub(started))
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_publish_jobs SET input_generation=99,job_revision=9223372036854775807,source_revision=101,job_lease_expires_ms=0 WHERE id=29`); err != nil {
		t.Fatal(err)
	}
	turn = searchPublisherComponentTurn(t, w, registry, SearchHeadPublish)
	for id := range turn.steps {
		if id != searchPublisherOwnerID && id != searchPublisherIdentity && id != searchHeadReceiptRead {
			delete(turn.steps, id)
		}
	}
	again, err := applySearchHeadPublish(t.Context(), turn, c)
	if err != nil || again != out {
		t.Fatal("historical flip result reconstructed/rechecked", again, out, err)
	}
}

func TestSearchHeadStaleCASRollsBackNativeAndAccounting(t *testing.T) {
	for _, variant := range []struct{ name, sql string }{{"revision", "UPDATE search_reclaim_jobs SET job_revision=2 WHERE id=2"}, {"foreign_lease", "UPDATE search_reclaim_jobs SET job_lease_owner=7,job_lease_generation=8,job_lease_expires_ms=2100000001000 WHERE id=2"}} {
		t.Run(variant.name, func(t *testing.T) {
			w, r, c := searchHeadNativeFixture(t)
			if _, err := w.db.ExecContext(t.Context(), variant.sql); err != nil {
				t.Fatal(err)
			}
			turn := searchPublisherComponentTurn(t, w, r, SearchHeadPublish)
			out, err := writeSearchHeadPublish(t.Context(), turn, c, c.request, 1000)
			if !errors.Is(err, ErrConflict) || out != (searchHeadPublishResult{}) {
				t.Fatal(out, err)
			}
			if _, e := turn.transaction.rollback(err); !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
			var head, pending, clock int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT published_document_id FROM search_publish_jobs WHERE id=29`).Scan(&head); err != nil {
				t.Fatal(err)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT pending_subjects FROM search_generations WHERE id=7`).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM search_revision WHERE singleton=1`).Scan(&clock); err != nil {
				t.Fatal(err)
			}
			if head != 13 || pending != 3 || clock != 19 {
				t.Fatal("partial accounting survived stale custody CAS", head, pending, clock)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH '00006f00006c000064'`).Scan(&head); err != nil || head != 41 {
				t.Fatal("rollback lost original native token", err)
			}

		})
	}
}

func TestSearchHeadBuildingDoesNotAdvanceServingClock(t *testing.T) {
	w, _, c := searchHeadNativeFixture(t)
	r := c.request
	r.snapshot.stage.bank.role = "building"
	r.snapshot.stage.active = sql.NullInt64{Int64: 8, Valid: true}
	r.snapshot.stage.building = sql.NullInt64{Int64: 7, Valid: true}
	r.snapshot.servingRevision = math.MaxInt64
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_generations SET state='building' WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	p := r.snapshot.stage.bank.profile
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(8,'v2',?,?,?,'active',1000)`, p.RecipeVersion, p.EngineVersion, p.UnicodeFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_revision SET active_generation_id=8,building_generation_id=7,revision=? WHERE singleton=1`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	c, err := newSearchHeadPublishCommand(c.identity, r)
	if err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, searchHeadDefinitions(), "search_head_test.go", "search_head_statements.go")
	turn := searchPublisherComponentTurn(t, w, registry, SearchHeadPublish)
	native, err := searchHeadPublishFreshFacts(t.Context(), turn, r)
	if err != nil {
		t.Fatal(err)
	}
	before := searchImageSharedFacts(t, turn)
	out, err := writeSearchHeadPublish(t.Context(), turn, c, native, 1000)
	if err != nil || out.PublishedGeneration != 5 {
		t.Fatal(out, err)
	}
	want := before
	want.pending--
	if after := searchImageSharedFacts(t, turn); after != want {
		t.Fatal("building publication changed serving/Gdriver", before, after)
	}
}

// The staged image's zero-row seed is not an allocation certificate. The
// separate seal must be completed even when its expected count is zero.
func TestSearchHeadEmptyRequiresSeparateSeal(t *testing.T) {
	s := searchHeadPublishSnapshotFixture(t)
	s.stage.job.expectedFragments = 0
	s.stage.job.stagedFragments = 0
	s.stage.job.stagedBytes = 0
	s.stage.job.afterField = 0
	s.stage.job.afterOrdinal = 0
	s.stage.job.afterFragmentID = 0
	seed, err := searchmanifest.Seed(s.stage.image.input, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.stage.seal = searchFragmentSeal{algorithm: searchmanifest.Algorithm, seed: seed, chain: seed, expectedDigest: seed}
	if _, err := planSearchHeadPublish(s); err == nil {
		t.Fatal("empty allocation/stage passed as sealed")
	}
	s.stage.seal.sealed = 1
	if _, err := planSearchHeadPublish(s); err != nil {
		t.Fatal(err)
	}
}

func TestSearchHeadNativeExtraTailRefusesBeforeFlip(t *testing.T) {
	w, r, c := searchHeadNativeFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(504,41,NULL,1,3,'11','','','',5,7,307)`); err != nil {
		t.Fatal(err)
	}
	turn := searchPublisherComponentTurn(t, w, r, SearchHeadPublish)
	out, err := searchHeadPublishFreshFacts(t.Context(), turn, c.request)
	if !errors.Is(err, ErrConflict) || out != (searchHeadPublishRequest{}) {
		t.Fatal("indexed no-next proof accepted extra fragment", out, err)
	}
}

func TestSearchHeadFirstPublicationHasNoOldNativeDelete(t *testing.T) {
	w, _, c := searchHeadNativeFixture(t)
	r := c.request
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_own_prefix(search_own_prefix,rowid,title_prefix_tokens,initial_prefix_tokens) VALUES('delete',41,?,?)`, r.old.prefix, r.old.initial); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_publish_jobs SET published_document_id=NULL,published_generation=0 WHERE id=29`); err != nil {
		t.Fatal(err)
	}
	r.snapshot.stage.job.publishedImage = sql.NullInt64{}
	r.snapshot.stage.job.publishedGeneration = 0
	r.old = searchOwnPrefixImage{}
	r.retirement = searchImageRetirementWitness{}
	c, err := newSearchHeadPublishCommand(c.identity, r)
	if err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, searchHeadDefinitions(), "search_head_test.go", "search_head_statements.go")
	turn := searchPublisherComponentTurn(t, w, registry, SearchHeadPublish)
	native, err := searchHeadPublishFreshFacts(t.Context(), turn, r)
	if err != nil {
		t.Fatal(err)
	}
	out, err := writeSearchHeadPublish(t.Context(), turn, c, native, 1000)
	if err != nil || out.ImageID != 307 || out.PublishedGeneration != 5 {
		t.Fatal(out, err)
	}
	var phase string
	var revision int64
	var due sql.NullInt64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT phase,job_revision,job_next_attempt_ms FROM search_reclaim_jobs WHERE id=2`).Scan(&phase, &revision, &due); err != nil {
		t.Fatal(err)
	}
	if phase != "held" || revision != 1 || due.Valid {
		t.Fatal("first publication touched unrelated old custody", phase, revision, due)
	}
}
