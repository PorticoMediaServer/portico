package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicPrefenceFixture(t *testing.T) (*executor, basicPrefenceEffects) {
	t.Helper()
	w, in := basicCompleteAcceptFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicCompleteTestDefinitions())
	got, err := acceptBasicOrdinaryInput(t.Context(), turn, in, 2000)
	if err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	// Capture actual post-freeze native revision; do not infer the next command
	// from a historical result or invent an input-family/copy cursor mapping.
	if err = w.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&in.stream.changeRevision); err != nil {
		t.Fatal(err)
	}
	if in.stream.changeRevision != got.changeRevision {
		t.Fatal("fixture lost native revision")
	}
	in.snapshot.changeRevision = in.stream.changeRevision
	return w, basicPrefenceEffects{input: in}
}
func basicPrefenceTestDefinitions() []stmt.Definition {
	return append(basicCompleteTestDefinitions(), basicPrefenceDefinitions()...)
}
func TestBasicPrefenceFencesAssetAndCompleteOrdinaryHolder(t *testing.T) {
	w, in := basicPrefenceFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicPrefenceTestDefinitions())
	got, err := prefenceBasicOrdinary(t.Context(), turn, in)
	if err != nil || got != (basicCopyPosition{changeID: 51, changeRevision: 15, phase: 2, afterEntity: 31}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 AND info_shape_id IS NULL FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=2 AND job_revision=15 AND has_after=1 AND after_entity=31 AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' FROM catalog_info_changes WHERE id=51)
 AND (SELECT count(*)=0 FROM asset_streams)`).Scan(&exact)
	if err != nil || !exact {
		t.Fatal("wrong copy fence/native poststate", exact, err)
	}
}
func TestBasicPrefenceLateCASRollsBackBothFences(t *testing.T) {
	w, in := basicPrefenceFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicPrefenceTestDefinitions())
	delete(turn.steps, basicPrefenceChangeCAS)
	got, err := prefenceBasicOrdinary(t.Context(), turn, in)
	if err == nil || got != (basicCopyPosition{}) {
		t.Fatal(got, err)
	}
	var fenced bool
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT info_publication_pending=1 FROM catalog_assets WHERE id=41) AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&fenced); e != nil || !fenced {
		t.Fatal("did not reach late boundary", fenced, e)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41) AND (SELECT info_pending_change IS NULL FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41) AND (SELECT phase=1 AND job_revision=14 FROM catalog_info_changes WHERE id=51)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
}
func TestBasicPrefenceRejectsStaleInvalidation(t *testing.T) {
	w, in := basicPrefenceFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET info_invalidation_revision=2 WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	turn := basicBoundedComponentTurn(t, w, basicPrefenceTestDefinitions())
	got, err := prefenceBasicOrdinary(t.Context(), turn, in)
	if !errors.Is(err, ErrConflict) || got != (basicCopyPosition{}) {
		t.Fatal(got, err)
	}
}
