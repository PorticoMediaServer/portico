package writer

import (
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private native domain controls on W's real governor/frame/engine/time and
// memory seam, with synthetic component resource figures. These do not mint
// PublisherDisclosure, enable the public facade or qualify a production profile.
func searchImageStageFixture(t *testing.T) (*executor, *stmt.Registry) {
	t.Helper()
	w := searchImageStagePlanFixture(t)
	profile := searchpolicy.Compiled()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_generations SET recipe_version=?,engine_version=?,unicode_fingerprint=? WHERE id=7`, profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint); err != nil {
		t.Fatal(err)
	}
	// Unrelated generation-driver state, including an exhausted revision and
	// active lease, is not a subject-publisher write or freshness dependency.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_revision SET revision=91,input_sequence=71 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_generations SET pending_subjects=9,indexed_rows=17,indexed_bytes=29,
job_revision=9223372036854775807,job_lease_owner=41,job_lease_generation=43,job_lease_expires_ms=2100000000001,
after_entity_id=47,after_person_id=53,after_reading_id=59,after_table=7,captured_input_sequence=61,phase='idle',job_next_attempt_ms=67 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	defs := append(searchPublisherReceiptDefinitions(), searchImageBeginDefinitions()...)
	registry := cachedNativeInventoryFixtureRegistry(t, w, defs, "search_image_stage_test.go", "search_image_stage_plan_test.go", "search_publish_history_test.go", "search_image_stage_statements.go", "search_publish_receipts.go")
	return w, registry
}

func searchImageStageNativeCommand(t *testing.T, job int64) *searchImageStageCommand {
	t.Helper()
	r, id := searchImageBeginRequestFixture(t)
	r.source.entityID = job + 12
	r.source.title = "Owner"
	r.owner.id = job
	r.owner.entityID = r.source.entityID
	r.owner.publishedGeneration = 0
	r.owner.publishedImage = sql.NullInt64{}
	r.owner.nextAttempt = sql.NullInt64{}
	if job == 29 || job == 37 {
		r.owner.publishedGeneration = 3
		image := int64(14)
		if job == 29 {
			image = 13
		}
		r.owner.publishedImage = sql.NullInt64{Int64: image, Valid: true}
	}
	if job == 29 || job == 36 {
		r.owner.nextAttempt = sql.NullInt64{Int64: 1000, Valid: true}
	}
	id.OperationID = [16]byte{byte(job)}
	r.header.titleFold = "owner"
	r.header.titlePrefixTokens = "00006f00007700006e000065000072"
	r.header.initialPrefixTokens = "00006f00007700006e000065000072"
	m, err := searchmanifest.Prepare(searchImageBeginManifestInput(r), []searchmanifest.Row{{Field: 1, Revision: "11", Prefix: r.header.titlePrefixTokens, Lexical: "owner", Normalized: "owner"}})
	if err != nil {
		t.Fatal(err)
	}
	r.header.expectedFragments, r.header.expectedBytes, r.header.expectedDigest = m.Count(), m.Bytes(), m.Digest()
	c, err := newSearchImageStageCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func searchImageStageTurn(t *testing.T, w *executor, r *stmt.Registry) *commandTurn {
	t.Helper()
	return searchPublisherFixtureTurn(t, w, r, searchImageMemoryBudget{4 << 20, 4 << 20})
}

func TestSearchImageStageNativeAllocationAndOriginalReplay(t *testing.T) {
	w, registry := searchImageStageFixture(t)
	for _, job := range []int64{29, 35, 36, 37} {
		command := searchImageStageNativeCommand(t, job)
		turn := searchImageStageTurn(t, w, registry)
		sharedBefore := searchImageSharedFacts(t, turn)
		result, err := applySearchImageStage(t.Context(), turn, command)
		if err != nil {
			t.Fatalf("job%d: %v", job, err)
		}
		if result.ImageID <= 14 || result.JobID != job || result.AcceptedJobRevision != 20 || result.InputGeneration != 5 || result.PublishedGeneration != command.request.owner.publishedGeneration || result.Outcome != (ReceiptOutcome{}) {
			t.Fatal("not a complete hidden result", result)
		}
		var sealed, staged, verified, cursor int64
		var seed, chain []byte
		if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT sealed,staged_fragments,verified_fragments,after_fragment_id,seed,chain FROM search_image_seals WHERE publication_id=?`, result.ImageID).Scan(&sealed, &staged, &verified, &cursor, &seed, &chain); err != nil {
			t.Fatal(err)
		}
		if sealed != 0 || staged != 0 || verified != 0 || cursor != 0 || !reflect.DeepEqual(seed, result.SealChain[:]) || !reflect.DeepEqual(chain, seed) {
			t.Fatal("allocation fabricated completion")
		}
		var title, names, phase, digest string
		var live, heldID int64
		if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT title,person_name_tokens FROM search_publications WHERE id=?`, result.ImageID).Scan(&title, &names); err != nil {
			t.Fatal(err)
		}
		if title != "Owner" || names != "" {
			t.Fatal("foreign owner names entered a work", title, names)
		}
		if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT phase,live_publication_id,id FROM search_reclaim_jobs WHERE publication_id=?`, result.ImageID).Scan(&phase, &live, &heldID); err != nil {
			t.Fatal(err)
		}
		if phase != "held" || live != result.ImageID || heldID <= 0 {
			t.Fatal("hidden image lacks retained native custody")
		}
		if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT content_digest FROM search_publish_jobs WHERE id=?`, job).Scan(&digest); err != nil || digest != "" {
			t.Fatal("BEGIN changed legacy text digest", digest, err)
		}
		if sharedAfter := searchImageSharedFacts(t, turn); sharedAfter != sharedBefore {
			t.Fatal("publisher changed source/global/generation driver state", sharedBefore, sharedAfter)
		}
		var acceptedHead sql.NullInt64
		var acceptedGeneration int64
		if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT published_document_id,published_generation FROM search_publish_jobs WHERE id=?`, job).Scan(&acceptedHead, &acceptedGeneration); err != nil {
			t.Fatal(err)
		}
		if acceptedHead != command.request.owner.publishedImage || acceptedGeneration != command.request.owner.publishedGeneration {
			t.Fatal("hidden allocation moved serving head")
		}
		if err := turn.closeRows(); err != nil {
			t.Fatal(err)
		}
		if _, err := turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		// Current source/input/job/lease may advance without invalidating the
		// ORIGINAL immutable result. The image/header must not be allocated again.
		if _, err := w.db.ExecContext(t.Context(), `UPDATE search_publish_jobs SET job_revision=?,source_revision=102,input_generation=99,job_lease_expires_ms=0 WHERE id=?`, int64(math.MaxInt64), job); err != nil {
			t.Fatal(err)
		}
		turn = searchImageStageTurn(t, w, registry)
		// History needs neither a fresh memory partition nor any fresh/effect
		// definition; current facts above intentionally no longer match input.
		turn.transaction.custody.(*governedTurn).searchImageMemory = searchImageMemoryBudget{}
		for _, definition := range searchImageBeginDefinitions() {
			delete(turn.steps, definition.ID)
		}
		delete(turn.steps, searchPublisherImagePut)
		again, err := applySearchImageStage(t.Context(), turn, command)
		if err != nil || again != result {
			t.Fatal("fresh/overflow/lease checks hid historical receipt", again, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchImageStageNativeWitnessRefusalRollsBack(t *testing.T) {
	w, registry := searchImageStageFixture(t)
	command := searchImageStageNativeCommand(t, 29)
	for _, mutation := range []string{
		`UPDATE catalog_entities SET revision=12 WHERE id=41`,
		`UPDATE catalog_entities SET retired=1 WHERE id=41`,
		`UPDATE search_generations SET unicode_fingerprint='wrong' WHERE id=7`,
		`UPDATE search_revision SET active_generation_id=NULL WHERE singleton=1`,
		`UPDATE search_publish_jobs SET job_lease_generation=3 WHERE id=29`,
		`UPDATE search_publish_jobs SET content_digest='old-stage-proof' WHERE id=29`,
		`UPDATE search_publish_jobs SET job_revision=20 WHERE id=29`,
	} {
		turn := searchImageStageTurn(t, w, registry)
		if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, mutation); err != nil {
			t.Fatal(err)
		}
		result, err := applySearchImageStage(t.Context(), turn, command)
		if !errors.Is(err, ErrConflict) || result != (SearchImageStageResult{}) {
			t.Fatal(mutation, result, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	var images, seals, held, receipts int64
	for i, q := range []string{`SELECT count(*) FROM search_publications`, `SELECT count(*) FROM search_image_seals`, `SELECT count(*) FROM search_reclaim_jobs`, `SELECT count(*) FROM search_publish_receipts WHERE publish_job_id=29`} {
		target := []*int64{&images, &seals, &held, &receipts}[i]
		if err := w.db.QueryRowContext(t.Context(), q).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if images != 2 || seals != 0 || held != 0 || receipts != 0 {
		t.Fatal("refusal left a prefix", images, seals, held, receipts)
	}
}

func TestSearchImageStageRollbackAtEveryEffect(t *testing.T) {
	w, registry := searchImageStageFixture(t)
	command := searchImageStageNativeCommand(t, 29)
	// Two receipt reads + six native fresh reads, then five effects. Refuse
	// each effect admission in the SAME frame; rollback removes every prefix.
	for at := 0; at < 5; at++ {
		turn := searchImageStageTurn(t, w, registry)
		ids := [...]stmt.ID{searchImageInsert, searchImageSealInsert, searchImageReclaimInsert, searchImageAttachHeadDue, searchPublisherImagePut}
		id := ids[at]
		step := turn.steps[id]
		step.work.Rows = turn.transaction.workLimit.Rows + 1
		turn.steps[id] = step
		result, err := applySearchImageStage(t.Context(), turn, command)
		if !errors.Is(err, ErrQuantum) || result != (SearchImageStageResult{}) {
			t.Fatal(result, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
}

type searchImageShared struct {
	serving, input, pending, revision, leaseOwner, leaseGeneration, leaseExpires, afterEntity, afterPerson, afterReading, afterTable, captured, indexedRows, indexedBytes int64
	phase                                                                                                                                                                 string
	due                                                                                                                                                                   sql.NullInt64
}

func searchImageSharedFacts(t *testing.T, turn *commandTurn) searchImageShared {
	t.Helper()
	var v searchImageShared
	if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT revision,input_sequence FROM search_revision WHERE singleton=1`).Scan(&v.serving, &v.input); err != nil {
		t.Fatal(err)
	}
	if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT pending_subjects,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,after_entity_id,after_person_id,after_reading_id,after_table,captured_input_sequence,indexed_rows,indexed_bytes,phase,job_next_attempt_ms FROM search_generations WHERE id=7`).Scan(&v.pending, &v.revision, &v.leaseOwner, &v.leaseGeneration, &v.leaseExpires, &v.afterEntity, &v.afterPerson, &v.afterReading, &v.afterTable, &v.captured, &v.indexedRows, &v.indexedBytes, &v.phase, &v.due); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSearchImageStageReceiptCollisionRollsBackWholeImage(t *testing.T) {
	w, registry := searchImageStageFixture(t)
	r := searchImageStageNativeCommand(t, 29).request
	identity := CommandIdentity{Incarnation: [16]byte{17}, Sequence: 8, OperationID: [16]byte{30}}
	command, err := newSearchImageStageCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	turn := searchImageStageTurn(t, w, registry)
	out, err := applySearchImageStage(t.Context(), turn, command)
	if err == nil || out != (SearchImageStageResult{}) {
		t.Fatal("receipt UNIQUE failure exposed a partial allocation", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM search_publications WHERE generation=5`).Scan(&count); err != nil || count != 0 {
		t.Fatal("receipt failure committed image prefix", count, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM search_image_seals`).Scan(&count); err != nil || count != 0 {
		t.Fatal("receipt failure committed seal prefix", count, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM search_reclaim_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("receipt failure committed reclaim prefix", count, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT job_revision FROM search_publish_jobs WHERE id=29`).Scan(&count); err != nil || count != 19 {
		t.Fatal("receipt failure advanced job", count, err)
	}
}
