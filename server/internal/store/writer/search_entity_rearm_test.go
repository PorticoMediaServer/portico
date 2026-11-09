package writer

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Native SQL/component evidence only. The actual caller must still compose
// receipt/authority, full Work, current input and physical terminal outcome.
func searchRearmFixture(t *testing.T) (*executor, *stmt.Registry, *searchHeadPublishCommand) {
	t.Helper()
	w, old := searchHeadNativeSeed(t)
	defs := searchEntityRearmDefinitions()
	r := cachedNativeInventoryFixtureRegistry(t, w, defs, "search_entity_rearm_test.go", "search_entity_rearm_statements.go")
	return w, r, old
}

func searchRearmTurn(t *testing.T, w *executor, r *stmt.Registry) *commandTurn {
	t.Helper()
	return searchRearmTurnPurpose(t, w, r, InventoryAccept)
}

func searchRearmTurnPurpose(t *testing.T, w *executor, r *stmt.Registry, purpose CommandID) *commandTurn {
	t.Helper()
	// Test-only frame, no new production command ID or dispatcher is installed.
	tx, err := w.begin(t.Context(), purpose, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{Rows: 1, VMOps: 100, Pages: 1, WALBytes: 128, Hold: time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func searchRearmInput(t *testing.T, building bool) *searchEntityRearmInput {
	t.Helper()
	var b *searchEntityRearmBank
	if building {
		b = &searchEntityRearmBank{8, searchpolicy.RecipeV1}
	}
	in, err := newSearchEntityRearmInput(41, [16]byte{23}, 12, searchEntityRearmBank{7, searchpolicy.RecipeV1}, b)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func searchRearmExec(t *testing.T, turn *commandTurn, q string, args ...any) {
	t.Helper()
	// Fixture/control mutation, never a production callback/executor seam.
	if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}

func searchRearmChanges(t *testing.T, turn *commandTurn) int64 {
	t.Helper()
	var n int64
	if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, "SELECT total_changes()").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSearchEntityRearmNativeAtomicBanksVisibilityCustodyAndNoop(t *testing.T) {
	w, registry, _ := searchRearmFixture(t)
	for _, building := range []bool{false, true} {
		name := "active"
		if building {
			name = "active_and_building"
		}
		t.Run(name, func(t *testing.T) {
			turn := searchRearmTurn(t, w, registry)
			searchRearmExec(t, turn, `UPDATE catalog_entities SET revision=12 WHERE id=41`)
			if building {
				searchRearmExec(t, turn, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) SELECT 8,'next',recipe_version,engine_version,unicode_fingerprint,'building',1000 FROM search_generations WHERE id=7`)
				searchRearmExec(t, turn, `UPDATE search_revision SET building_generation_id=8 WHERE singleton=1`)
				searchRearmExec(t, turn, `INSERT INTO search_publish_jobs(id,entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase) SELECT 81,41,8,1,source_incarnation,11,recipe_version,'capture' FROM search_publish_jobs WHERE id=29`)
				searchRearmExec(t, turn, `UPDATE search_generations SET pending_subjects=1 WHERE id=8`)
			}
			var beforeTitle string
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT title_fold FROM search_publications WHERE id=13`).Scan(&beforeTitle); err != nil {
				t.Fatal(err)
			}
			in := searchRearmInput(t, building)
			out, err := rearmSearchEntity(t.Context(), turn, in)
			if err != nil || out.EntityID != 41 || out.SourceRevision != 12 || out.BankCount != in.count || out.Banks[0] != (searchEntityRearmObligation{7, 29, 6}) {
				t.Fatalf("not one complete bank obligation: %+v %v", out, err)
			}
			if building && out.Banks[1] != (searchEntityRearmObligation{8, 81, 2}) {
				t.Fatal("building silently omitted", out)
			}
			var intact bool
			err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
(SELECT published_document_id=13 AND published_generation=3 AND document_id IS NULL AND input_generation=6 AND source_revision=12 AND phase='capture' AND job_revision=20 AND job_lease_generation=3 AND job_lease_owner=0 AND job_lease_expires_ms=0 AND last_command_sequence=7 AND acknowledged_command_sequence=7 AND job_next_attempt_ms=0 FROM search_publish_jobs WHERE id=29)
AND (SELECT live_publication_id=307 AND phase='ready' AND job_next_attempt_ms=0 FROM search_reclaim_jobs WHERE publication_id=307)
AND (SELECT live_publication_id=13 AND phase='held' FROM search_reclaim_jobs WHERE publication_id=13)
AND (SELECT title_fold=? FROM search_publications WHERE id=13)
AND (SELECT count(*)=3 FROM search_fragments WHERE publication_id=307)
AND (SELECT count(*)=1 FROM search_image_seals WHERE publication_id=307)
AND (SELECT pending_subjects=3 FROM search_generations WHERE id=7)
AND (SELECT revision=19 AND input_sequence=72 FROM search_revision WHERE singleton=1)`, beforeTitle).Scan(&intact)
			if err != nil || !intact {
				t.Fatal("lost published visibility, detached custody/bytes or exact clocks", intact, err)
			}
			before := searchRearmChanges(t, turn)
			again, err := rearmSearchEntity(t.Context(), turn, in)
			if err != nil || again != out || searchRearmChanges(t, turn) != before {
				t.Fatal("same tuple created work or changed stable result", again, out, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSearchEntityRearmNativeRefusalRollsBackMetadataAndEveryBank(t *testing.T) {
	w, registry, _ := searchRearmFixture(t)
	for _, tc := range []struct {
		name   string
		edit   string
		reason searchRearmReason
	}{
		{"never_indexed", `DELETE FROM search_publish_receipts; DELETE FROM search_publish_jobs`, searchRearmNeverIndexed},
		{"missing_active", `DELETE FROM search_publish_receipts WHERE publish_job_id=29; DELETE FROM search_publish_jobs WHERE id=29; INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) SELECT 9,'history',recipe_version,engine_version,unicode_fingerprint,'building',1000 FROM search_generations WHERE id=7; INSERT INTO search_publish_jobs(entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase) SELECT 41,9,1,source_incarnation,11,recipe_version,'capture' FROM search_publish_jobs WHERE id=35`, searchRearmMissingBankJob},
		{"missing_building", `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) SELECT 8,'next',recipe_version,engine_version,unicode_fingerprint,'building',1000 FROM search_generations WHERE id=7; UPDATE search_revision SET building_generation_id=8`, searchRearmMissingBankJob},
		{"unexpected_building", `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) SELECT 8,'next',recipe_version,engine_version,unicode_fingerprint,'building',1000 FROM search_generations WHERE id=7; UPDATE search_revision SET building_generation_id=8`, searchRearmSourceOrBankChanged},
		{"missing_custody", `DELETE FROM search_reclaim_jobs WHERE publication_id=307`, searchRearmCustodyMismatch},
		{"wrong_custody", `UPDATE search_reclaim_jobs SET phase='ready' WHERE publication_id=307`, searchRearmCustodyMismatch},
		{"stage_is_head", `UPDATE search_publish_jobs SET document_id=published_document_id WHERE id=29`, searchRearmCustodyMismatch},
		{"older_source", `UPDATE search_publish_jobs SET source_revision=13 WHERE id=29`, searchRearmSourceOrBankChanged},
		{"input_overflow", `UPDATE search_publish_jobs SET input_generation=9223372036854775807 WHERE id=29`, searchRearmCounterExhausted},
		{"job_overflow", `UPDATE search_publish_jobs SET job_revision=9223372036854775807 WHERE id=29`, searchRearmCounterExhausted},
		{"lease_overflow", `UPDATE search_publish_jobs SET job_lease_generation=9223372036854775807 WHERE id=29`, searchRearmCounterExhausted},
		{"custody_overflow", `UPDATE search_reclaim_jobs SET job_revision=9223372036854775807 WHERE publication_id=307`, searchRearmCounterExhausted},
		{"input_clock_overflow", `UPDATE search_revision SET input_sequence=9223372036854775807`, searchRearmCounterExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turn := searchRearmTurn(t, w, registry)
			searchRearmExec(t, turn, `UPDATE catalog_entities SET revision=12 WHERE id=41`)
			searchRearmExec(t, turn, tc.edit)
			in := searchRearmInput(t, tc.name == "missing_building")
			// Overflow needs a valid old stage identity for the OLD input;
			// detach in fixture, preserving all immutable old native bytes.
			if tc.name == "input_overflow" || tc.name == "older_source" {
				searchRearmExec(t, turn, `UPDATE search_publish_jobs SET document_id=NULL WHERE id=29`)
			}
			out, err := rearmSearchEntity(t.Context(), turn, in)
			var refusal *searchRearmRefusal
			if !errors.As(err, &refusal) || refusal.Reason != tc.reason || out != (searchEntityRearmResult{}) {
				t.Fatalf("wrong typed zero-output refusal: %+v %v", out, err)
			}
			if turn.transaction.ctx.Err() == nil {
				t.Fatal("helper error left acceptance usable")
			}
			if _, err := turn.transaction.commit(); err == nil {
				t.Fatal("ignored helper refusal committed metadata")
			}
			var revision int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM catalog_entities WHERE id=41`).Scan(&revision); err != nil || revision != 11 {
				t.Fatal("failed cohort metadata escaped rollback", revision, err)
			}
		})
	}
}

func TestSearchEntityRearmNativeCaughtUpAndPendingCounterEdges(t *testing.T) {
	w, registry, _ := searchRearmFixture(t)
	for _, pending := range []int64{0, math.MaxInt64 - 1, math.MaxInt64} {
		turn := searchRearmTurn(t, w, registry)
		searchRearmExec(t, turn, `UPDATE catalog_entities SET revision=12 WHERE id=41`)
		searchRearmExec(t, turn, `UPDATE search_publish_jobs SET input_generation=published_generation,document_id=NULL,phase='idle' WHERE id=29`)
		searchRearmExec(t, turn, `UPDATE search_generations SET pending_subjects=? WHERE id=7`, pending)
		out, err := rearmSearchEntity(t.Context(), turn, searchRearmInput(t, false))
		if pending == math.MaxInt64 {
			var refusal *searchRearmRefusal
			if !errors.As(err, &refusal) || refusal.Reason != searchRearmCounterExhausted {
				t.Fatal("pending overflow not refused", err)
			}
		} else {
			var next int64
			if err != nil || out.Banks[0].InputGeneration != 4 {
				t.Fatal(out, err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT pending_subjects FROM search_generations WHERE id=7`).Scan(&next); err != nil || next != pending+1 {
				t.Fatal("caught-up delta is not exactly one", next, err)
			}
		}
		_ = turn.closeRows()
		_, _ = turn.transaction.rollback(nil)
	}
	turn := searchRearmTurn(t, w, registry)
	searchRearmExec(t, turn, `UPDATE catalog_entities SET revision=12 WHERE id=41`)
	searchRearmExec(t, turn, `UPDATE search_generations SET pending_subjects=? WHERE id=7`, int64(math.MaxInt64))
	if _, err := rearmSearchEntity(t.Context(), turn, searchRearmInput(t, false)); err != nil {
		t.Fatal("already pending +0 falsely overflows", err)
	}
}

func TestSearchEntityRearmConstructorAndPoison(t *testing.T) {
	for _, tc := range []struct {
		entity, revision int64
		inc              [16]byte
	}{{0, 1, [16]byte{1}}, {1, 0, [16]byte{1}}, {1, 1, [16]byte{}}} {
		if _, err := newSearchEntityRearmInput(tc.entity, tc.inc, tc.revision, searchEntityRearmBank{7, "recipe"}, nil); err == nil {
			t.Fatal("invalid source admitted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := rearmSearchEntity(ctx, nil, nil); err == nil || out != (searchEntityRearmResult{}) {
		t.Fatal("invalid/cancelled entry exposed result")
	}
}
