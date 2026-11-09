package writer

import (
	"bytes"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func searchRearmCombinedRegistry(t *testing.T, w *executor, extra ...[]stmt.Definition) *stmt.Registry {
	t.Helper()
	disk, err := os.ReadFile("../schema/schema.sql")
	if err != nil || !bytes.Equal(disk, []byte(schema.SQL())) {
		t.Fatal("historical matrix/schema source differs from embed", err)
	}
	defs := searchEntityRearmDefinitions()
	seen := map[stmt.ID]bool{}
	for _, d := range defs {
		seen[d.ID] = true
	}
	for _, family := range extra {
		for _, d := range family {
			if !seen[d.ID] {
				defs = append(defs, d)
				seen[d.ID] = true
			}
		}
	}
	return cachedNativeInventoryFixtureRegistry(t, w, defs, "search_entity_rearm_history_test.go", "search_entity_rearm.go", "search_entity_rearm_statements.go")
}

func TestSearchEntityRearmNativeStaleImageFenceAndCAS(t *testing.T) {
	w := searchImageStagePlanFixture(t)
	want, identity := searchImageBeginRequestFixture(t)
	p := want.bank.profile
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_generations SET recipe_version=?,engine_version=?,unicode_fingerprint=?,pending_subjects=1 WHERE id=7`, p.RecipeVersion, p.EngineVersion, p.UnicodeFingerprint); err != nil {
		t.Fatal(err)
	}
	c, err := newSearchImageStageCommand(identity, want)
	if err != nil {
		t.Fatal(err)
	}
	r := searchRearmCombinedRegistry(t, w, searchImageBeginDefinitions(), searchPublisherReceiptDefinitions())
	turn := searchRearmTurnPurpose(t, w, r, SearchImageStage)
	if _, err := searchImageStageFreshFacts(t.Context(), turn, want); err != nil {
		t.Fatal("old worker was not initially current", err)
	}
	searchRearmExec(t, turn, `UPDATE catalog_entities SET revision=12 WHERE id=41`)
	if _, err := rearmSearchEntity(t.Context(), turn, searchRearmInput(t, false)); err != nil {
		t.Fatal(err)
	}
	before := searchRearmChanges(t, turn)
	if _, err := searchImageStageFreshFacts(t.Context(), turn, want); !errors.Is(err, ErrConflict) {
		t.Fatal("stale fresh image source was accepted", err)
	}
	id, args := searchImageStageAttachValues(c, 13, 20)
	row, err := turn.queryRow(t.Context(), turn.steps[id].statementStep, args...)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = row.Scan(&revision); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old image CAS changed new generation", err)
	}
	if searchRearmChanges(t, turn) != before {
		t.Fatal("stale image wrote rows")
	}
}

func TestSearchEntityRearmNativeStaleFragmentFenceAndCAS(t *testing.T) {
	w, c := searchFragmentNativeSeed(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_generations SET pending_subjects=1 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	r := searchRearmCombinedRegistry(t, w, searchFragmentDefinitions())
	turn := searchRearmTurnPurpose(t, w, r, SearchFragmentStage)
	if _, err := searchFragmentStageFreshFacts(t.Context(), turn, c.request.snapshot); err != nil {
		t.Fatal("old fragment worker was not initially current", err)
	}
	searchRearmExec(t, turn, `UPDATE catalog_entities SET revision=12 WHERE id=41`)
	if _, err := rearmSearchEntity(t.Context(), turn, searchRearmInput(t, false)); err != nil {
		t.Fatal(err)
	}
	before := searchRearmChanges(t, turn)
	if _, err := searchFragmentStageFreshFacts(t.Context(), turn, c.request.snapshot); !errors.Is(err, ErrConflict) {
		t.Fatal("stale fresh fragment accepted", err)
	}
	next, err := planSearchFragmentStage(c.request.snapshot, c.payload)
	if err != nil {
		t.Fatal(err)
	}
	id, args := searchFragmentJobValues(c, 501, next)
	row, err := turn.queryRow(t.Context(), turn.steps[id].statementStep, args...)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = row.Scan(&revision); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale fragment CAS changed new generation", err)
	}
	if searchRearmChanges(t, turn) != before {
		t.Fatal("stale fragment wrote rows")
	}
}

func TestSearchEntityRearmNativeStaleHeadAndSealBoundary(t *testing.T) {
	w, c := searchHeadNativeSeed(t)
	r := searchRearmCombinedRegistry(t, w, searchHeadDefinitions())
	turn := searchRearmTurnPurpose(t, w, r, SearchHeadPublish)
	if _, err := searchHeadPublishFreshFacts(t.Context(), turn, c.request); err != nil {
		t.Fatal("old sealed head was not initially current", err)
	}
	next, err := planSearchHeadPublish(c.request.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	searchRearmExec(t, turn, `UPDATE catalog_entities SET revision=12 WHERE id=41`)
	if _, err := rearmSearchEntity(t.Context(), turn, searchRearmInput(t, false)); err != nil {
		t.Fatal(err)
	}
	before := searchRearmChanges(t, turn)
	if _, err := searchHeadPublishFreshFacts(t.Context(), turn, c.request); !errors.Is(err, ErrConflict) {
		t.Fatal("stale sealed head accepted", err)
	}
	id, args := searchHeadJobValues(c, next)
	row, err := turn.queryRow(t.Context(), turn.steps[id].statementStep, args...)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = row.Scan(&revision); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale head CAS changed new generation", err)
	}
	// SearchSeal has a descriptor but NO merged body/dispatch. Do not invent a
	// test-only implementation and call it native certification. Prove the
	// existing sealed OLD image cannot pass current publisher source ownership;
	// its bytes/seal remain retained for the existing reclaimer.
	if _, err := searchPublisherStageFacts(t.Context(), turn, SearchHeadPublish, c.request.snapshot.stage); !errors.Is(err, ErrConflict) {
		t.Fatal("old sealed image recovered current ownership", err)
	}
	if searchRearmChanges(t, turn) != before {
		t.Fatal("stale head/sealed image wrote rows")
	}
}

func TestSearchEntityRearmNativeOriginalReplayAndValidACKSurvive(t *testing.T) {
	w := searchPublisherHistorySeed(t)
	// Job30 has a real retained ORIGINAL image receipt, last8/ACK7. Unlike
	// the normal fresh head, its historical image no longer needs to exist.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_publish_jobs SET job_revision=19,source_revision=11 WHERE id=30; UPDATE catalog_entities SET revision=12 WHERE id=42; UPDATE search_generations SET pending_subjects=1 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	r := searchRearmCombinedRegistry(t, w, searchPublisherReceiptDefinitions())
	turn := searchRearmTurnPurpose(t, w, r, SearchImageStage)
	in, err := newSearchEntityRearmInput(42, [16]byte{23}, 12, searchEntityRearmBank{7, searchRearmInput(t, false).banks[0].Recipe}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rearmSearchEntity(t.Context(), turn, in); err != nil {
		t.Fatal(err)
	}
	q := PublisherReceiptRequest{JobID: 30, LibraryID: 3, EntityID: 42, Receipt: ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{17}, Sequence: 8, OperationID: [16]byte{30}}, RequestDigest: RequestDigest{91}}}
	before := searchRearmChanges(t, turn)
	lookup, err := lookupSearchImageStage(t.Context(), turn, q)
	if err != nil || lookup.decision != receiptReplay || lookup.result.ImageID != 1030 || lookup.result.InputGeneration != 5 {
		t.Fatal("original receipt was rebuilt from new generation", lookup, err)
	}
	if searchRearmChanges(t, turn) != before {
		t.Fatal("historical replay performed work")
	}
	ack, err := acknowledgeSearchPublisherFacts(t.Context(), turn, q, lookup.owner, lookup.decision, 30)
	if err != nil || ack != 8 {
		t.Fatal("valid original ACK stranded", ack, err)
	}
	after := searchRearmChanges(t, turn)
	if after != before+1 {
		t.Fatal("ACK did more than advance original floor", before, after)
	}
	if _, err := acknowledgeSearchPublisherFacts(t.Context(), turn, q, lookup.owner, lookup.decision, 30); !errors.Is(err, ErrConflict) {
		t.Fatal("stale ACK floor/reuse accepted", err)
	}
	if searchRearmChanges(t, turn) != after {
		t.Fatal("stale ACK changed rows")
	}
	var intact bool
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT input_generation=100 AND source_revision=12 AND job_revision=20 AND phase='capture' AND last_command_sequence=8 AND acknowledged_command_sequence=8 FROM search_publish_jobs WHERE id=30`).Scan(&intact); err != nil || !intact {
		t.Fatal("history recovery altered new obligation", err)
	}
}
