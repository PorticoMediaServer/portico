package writer

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Distinct durable populations, not labels on an identical missing row. The
// saved historical image need not still exist: historical values have no live image FK.
func searchPublisherHistorySeed(t *testing.T) *executor {
	t.Helper()
	w, _, _ := testExecutor(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(3,'publisher-fixture','/publisher')`)
	// A verified canonical clone already owns the global movie-kind dictionary
	// row. Preserve it; only the unchanged standalone fixture creates that row.
	if !publisherHasCanonicalPopulation(t) {
		exec(`INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`)
	}
	exec(`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
VALUES(7,'v1',?,?,?,'active',1000)`, searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint)
	exec(`UPDATE search_revision SET active_generation_id=7 WHERE singleton=1`)
	inc := [16]byte{17}
	source := [16]byte{23}
	for job := int64(29); job <= 34; job++ {
		entity := job + 12
		public := [16]byte{byte(entity)}
		exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,3,1)`, entity, public[:])
		exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation)
VALUES(?,?,3,1,'Owner','owner','O','owner','O',0,?,zeroblob(16))`, entity, public[:], source[:])
		last, ack := int64(8), int64(7)
		if job == 29 {
			last = 7
		}
		if job == 32 || job == 34 {
			ack = 8
		}
		// The fresh mutable facts deliberately diverge from the saved result.
		// Historical lookup must not check this revision, input or expired lease.
		exec(`INSERT INTO search_publish_jobs(id,entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,command_incarnation,last_command_sequence,acknowledged_command_sequence,job_revision)
VALUES(?,?,7,99,?,101,?,'capture',?,?,?,?)`, job, entity, source[:], searchpolicy.RecipeV1, inc[:], last, ack, int64(math.MaxInt64))
		if job < 30 || job > 32 {
			continue
		}
		operation := [16]byte{byte(job)}
		digest := RequestDigest{91}
		if job == 31 {
			digest[0] = 92
		}
		chain := [32]byte{27}
		exec(`INSERT INTO search_publish_receipts(publish_job_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,input_generation,image_id,fragment_id,after_field,after_ordinal,after_fragment_id,seal_chain,published_generation,seal_algorithm)
VALUES(?,?,8,?,?,'image_stage',20,1000,5,?,NULL,0,0,0,?,3,?)`, job, inc[:], operation[:], digest[:], int64(1000)+job, chain[:], searchmanifest.Algorithm)
	}
	// Preserve AUTOINCREMENT history: subsequent allocations must never reuse
	// an ID carried by a historical receipt after its native row is retired.
	exec(`INSERT INTO search_publications(id,entity_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title_fold,title_prefix_tokens,initial_prefix_tokens)
VALUES(1032,44,7,3,1,5,?,11,?,'','','')`, source[:], searchpolicy.RecipeV1)
	exec(`DELETE FROM search_publications WHERE id=1032`)
	// An ACKed body may be invalid/retired. The ORIGINAL floor wins before
	// result-body SQL or validation; an unrelated identity conflict does not.
	exec(`UPDATE search_publish_receipts SET image_id=NULL,seal_algorithm='' WHERE publish_job_id=32`)
	return w
}

func searchPublisherHistoryFixture(t *testing.T) (*executor, *stmt.Registry) {
	t.Helper()
	w := searchPublisherHistorySeed(t)
	r := nativeInventoryFixtureRegistry(t, w, searchPublisherReceiptDefinitions(), "search_publish_history_test.go", "search_publish_receipts.go")
	return w, r
}

func searchPublisherReference(job int64) PublisherReceiptRequest {
	return PublisherReceiptRequest{JobID: job, LibraryID: 3, EntityID: job + 12,
		Receipt: ReceiptReference{CommandIdentity{Incarnation: [16]byte{17}, Sequence: 8, OperationID: [16]byte{byte(job)}}, RequestDigest{91}}}
}

func searchPublisherHistoryTurn(t *testing.T, w *executor, r *stmt.Registry) *commandTurn {
	t.Helper()
	return searchPublisherFixtureTurn(t, w, r, searchImageMemoryBudget{})
}

// Registration is complete BEFORE W's ONE helper admits/begins. This adapter
// installs only the finite domain steps on its SAME transaction; no local
// recipe, governor, grant binder, alternate pool or writer constructor exists.
func searchPublisherFixtureTurn(t *testing.T, w *executor, r *stmt.Registry, budget searchImageMemoryBudget) *commandTurn {
	t.Helper()
	var seq int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	frame, _ := searchResourcesFixture(t, w, path, budget, searchImageStatementFixture)
	if frame.transaction.settings.Synchronous != int(sqliteconn.Normal) {
		t.Fatal("actual command is not NORMAL", frame.transaction.settings)
	}
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(frame.transaction, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestSearchPublisherOriginalHistoryAndFloor(t *testing.T) {
	w, registry := searchPublisherHistoryFixture(t)
	for _, tt := range []struct {
		job  int64
		want receiptDecision
		err  error
	}{{29, receiptFresh, nil}, {30, receiptReplay, nil}, {31, 0, ErrConflict},
		{32, receiptRetired, nil}, {33, 0, ErrResolveRequired}, {34, receiptRetired, nil}, {999, 0, ErrResolveRequired}} {
		turn := searchPublisherHistoryTurn(t, w, registry)
		if tt.want == receiptRetired {
			delete(turn.steps, searchPublisherImageRead) // Floor MUST bypass body SQL.
		}
		out, err := lookupSearchImageStage(t.Context(), turn, searchPublisherReference(tt.job))
		if !errors.Is(err, tt.err) || tt.err == nil && out.decision != tt.want {
			t.Fatalf("job%d decision%d err%v", tt.job, out.decision, err)
		}
		if tt.err != nil && !reflect.DeepEqual(out, publisherReceiptLookup{}) {
			t.Fatal("error released owner/receipt result", out)
		}
		if tt.want == receiptReplay {
			if out.result.AcceptedJobRevision != 20 || out.result.ImageID != 1030 || out.result.InputGeneration != 5 || out.result.PublishedGeneration != 3 || out.result.SealChain != [32]byte{27} || out.result.Outcome != (ReceiptOutcome{}) {
				t.Fatal("mutable job reconstructed historical output", out.result)
			}
		} else if out.result != (SearchImageStageResult{}) {
			t.Fatal("nonreplay disclosed an image", out.result)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchPublisherAckAndCancellation(t *testing.T) {
	w, registry := searchPublisherHistoryFixture(t)
	turn := searchPublisherHistoryTurn(t, w, registry)
	q := searchPublisherReference(30)
	lookup, err := lookupSearchImageStage(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := acknowledgeSearchPublisher(t.Context(), turn, q, lookup)
	if err != nil || ack != 8 {
		t.Fatal(ack, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	turn = searchPublisherHistoryTurn(t, w, registry)
	again, err := lookupSearchImageStage(t.Context(), turn, q)
	if err != nil || again.decision != receiptRetired || again.result != (SearchImageStageResult{}) {
		t.Fatal(again, err)
	}
	if ack, err := acknowledgeSearchPublisher(t.Context(), turn, q, again); err != nil || ack != 8 {
		t.Fatal(ack, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if value, err := lookupSearchImageStage(ctx, turn, q); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(value, publisherReceiptLookup{}) {
		t.Fatal(value, err)
	}
	if value, err := acknowledgeSearchPublisher(ctx, turn, q, again); !errors.Is(err, context.Canceled) || value != 0 {
		t.Fatal(value, err)
	}
}

func TestSearchPublisherRejectsIncompleteHistoricalImage(t *testing.T) {
	w, registry := searchPublisherHistoryFixture(t)
	for _, column := range []string{"accepted_revision=0", "image_id=NULL", "fragment_id=99", "after_field=1", "after_ordinal=1", "after_fragment_id=1", "staged_fragments=1", "staged_bytes=1", "verified_fragments=1", "verified_bytes=1", "seal_chain=NULL", "seal_algorithm='wrong'", "published_generation=5"} {
		turn := searchPublisherHistoryTurn(t, w, registry)
		if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, "UPDATE search_publish_receipts SET "+column+" WHERE publish_job_id=30"); err != nil {
			t.Fatal(err)
		}
		got, err := lookupSearchImageStage(t.Context(), turn, searchPublisherReference(30))
		if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, publisherReceiptLookup{}) {
			t.Fatal(column, got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchPublisherScopeIdentityAndStaleAck(t *testing.T) {
	w, registry := searchPublisherHistoryFixture(t)
	for _, edit := range []func(*PublisherReceiptRequest){
		func(q *PublisherReceiptRequest) { q.EntityID++ },
		func(q *PublisherReceiptRequest) { q.Receipt.Identity.OperationID[0]++ },
		func(q *PublisherReceiptRequest) { q.Receipt.RequestDigest[0]++ },
		func(q *PublisherReceiptRequest) { q.Receipt.Identity.Incarnation[0]++ },
	} {
		turn := searchPublisherHistoryTurn(t, w, registry)
		q := searchPublisherReference(30)
		edit(&q)
		got, err := lookupSearchImageStage(t.Context(), turn, q)
		if err == nil || !reflect.DeepEqual(got, publisherReceiptLookup{}) {
			t.Fatal("foreign original proof released a result", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	turn := searchPublisherHistoryTurn(t, w, registry)
	q := searchPublisherReference(30)
	lookup, err := lookupSearchImageStage(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, `UPDATE search_publish_jobs SET acknowledged_command_sequence=8 WHERE id=30`); err != nil {
		t.Fatal(err)
	}
	if ack, err := acknowledgeSearchPublisher(t.Context(), turn, q, lookup); !errors.Is(err, ErrConflict) || ack != 0 {
		t.Fatal("stale ACK CAS succeeded", ack, err)
	}
}

// The shared protocol record is W-owned. These assignments pin the exact
// finite adapter signatures without a second record/interface or dispatcher.
var (
	_ func(context.Context, *commandTurn, PublisherReceiptRequest) (publisherReceiptLookup, error)        = lookupSearchImageStage
	_ func(context.Context, *commandTurn, PublisherReceiptRequest, publisherReceiptLookup) (int64, error) = acknowledgeSearchPublisher
	_ func(context.Context, *commandTurn, *searchImageStageCommand) (SearchImageStageResult, error)       = applySearchImageStage
)

func TestSearchPublisherAckProtocolFactsCannotAdvanceOtherOwner(t *testing.T) {
	w, registry := searchPublisherHistoryFixture(t)
	turn := searchPublisherHistoryTurn(t, w, registry)
	q := searchPublisherReference(30)
	lookup, err := lookupSearchImageStage(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*PublisherReceiptRequest){
		func(q *PublisherReceiptRequest) { q.JobID++ },
		func(q *PublisherReceiptRequest) { q.Receipt.Identity.Incarnation[0]++ },
		func(q *PublisherReceiptRequest) { q.Receipt.Identity.Sequence++ },
	} {
		wrong := q
		change(&wrong)
		if ack, err := acknowledgeSearchPublisher(t.Context(), turn, wrong, lookup); !errors.Is(err, ErrResolveRequired) || ack != 0 {
			t.Fatal("unrelated protocol facts advanced owner", ack, err)
		}
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// Observation of the existing exact-t transport, AFTER testExecutor consumed
// it. This supplies no opener/SQL/authority and cannot skip standalone setup.
func publisherHasCanonicalPopulation(t *testing.T) bool {
	qualificationPopulationAttachments.Lock()
	defer qualificationPopulationAttachments.Unlock()
	a := qualificationPopulationAttachments.byTest[t]
	return a != nil && a.consumed
}
