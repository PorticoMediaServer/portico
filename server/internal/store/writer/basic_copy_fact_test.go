package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCopyFactFixture(t *testing.T) (*executor, basicStreamCopyEffects) {
	t.Helper()
	w, in := basicPrefenceFixture(t)
	turn := basicComponentTurn(t, w, basicPrefenceTestDefinitions())
	p, err := prefenceBasicOrdinary(t.Context(), turn, in)
	if err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	return w, basicStreamCopyEffects{source: in.input.stream.source, position: p, shapeID: in.input.stream.shapeID,
		fileSizeRevision: in.input.snapshot.fileSizeRevision, invalidation: in.input.snapshot.invalidationRevision}
}

func basicCopyFactTestDefinitions() []stmt.Definition {
	return append(basicPrefenceTestDefinitions(), basicCopyFactDefinitions()...)
}

func basicCopySeedPrior(t *testing.T, w *executor, in *basicStreamCopyEffects) {
	t.Helper()
	in.hadBefore = true
	in.before = basicCanonicalStreamFact{30, 40, -12, 0, "old-probe"}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_stream_facts VALUES('basic-asset',30,40,-12,'old-probe',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES('basic-asset',2,'video','old-codec','en','retained child',0,'',1,0)`); err != nil {
		t.Fatal(err)
	}
}

func TestBasicCopyStreamFactInsertAndReplaceKeepFences(t *testing.T) {
	for _, name := range []string{"insert", "replace"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicCopyFactFixture(t)
			if name == "replace" {
				basicCopySeedPrior(t, w, &in)
			}
			turn := basicComponentTurn(t, w, basicCopyFactTestDefinitions())
			got, err := installBasicStreamFact(t.Context(), turn, in)
			want := basicCanonicalStreamFact{in.source.factRevision, in.source.size, in.source.modifiedNS, in.source.detailVersion, in.source.fingerprint}
			if err != nil || got != want {
				t.Fatal(got, err)
			}
			basicMediaCommit(t, turn)
			var actual basicCanonicalStreamFact
			if err = w.db.QueryRowContext(t.Context(), `SELECT revision,size,modified_ns,detail_version,fingerprint FROM asset_stream_facts WHERE asset_id='basic-asset'`).Scan(&actual.revision, &actual.size, &actual.modifiedNS, &actual.detailVersion, &actual.fingerprint); err != nil || actual != want {
				t.Fatal(actual, err)
			}
			var exact bool
			if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=2 AND job_revision=15 AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 FROM catalog_info_changes WHERE id=51)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
				t.Fatal("copy invented activation/cursor/receipt", exact, err)
			}
			if name == "replace" {
				var children int64
				if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=2 AND codec='old-codec' AND title='retained child'`).Scan(&children); err != nil || children != 1 {
					t.Fatal("parent replacement cascaded", children, err)
				}
			}
		})
	}
}

func TestBasicCopyStreamFactRejectsCapturedDrift(t *testing.T) {
	for _, name := range []string{"cursor", "pending", "prior_fact", "newer_fact"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicCopyFactFixture(t)
			basicCopySeedPrior(t, w, &in)
			switch name {
			case "cursor":
				in.position.changeRevision++
			case "pending":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET info_publication_pending=0 WHERE id=41`); err != nil {
					t.Fatal(err)
				}
			case "prior_fact":
				in.before.fingerprint = "uncaptured-fingerprint"
			case "newer_fact":
				in.before.revision = in.source.factRevision + 1
			}
			turn := basicComponentTurn(t, w, basicCopyFactTestDefinitions())
			got, err := installBasicStreamFact(t.Context(), turn, in)
			if !errors.Is(err, ErrConflict) || got != (basicCanonicalStreamFact{}) {
				t.Fatal(got, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var fingerprint string
			if err = w.db.QueryRowContext(t.Context(), `SELECT fingerprint FROM asset_stream_facts WHERE asset_id='basic-asset'`).Scan(&fingerprint); err != nil || fingerprint != "old-probe" {
				t.Fatal("failed copy changed canonical source", fingerprint, err)
			}
		})
	}
}

func TestBasicCopyStreamFactReturningFailureRollsBackNativeWrite(t *testing.T) {
	w, in := basicCopyFactFixture(t)
	basicCopySeedPrior(t, w, &in)
	defs := basicCopyFactTestDefinitions()
	for n := range defs {
		if defs[n].ID == basicCopyFactUpdate {
			// Actual native write, deliberately wrong native return column. The
			// fault definition receives its own real component Capture receipt.
			defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING asset_id,revision,size,modified_ns,fingerprint,detail_version", "RETURNING asset_id,detail_version,size,modified_ns,fingerprint,revision", 1)
		}
	}
	turn := basicComponentTurn(t, w, defs)
	got, err := installBasicStreamFact(t.Context(), turn, in)
	if !errors.Is(err, ErrConflict) || got != (basicCanonicalStreamFact{}) {
		t.Fatal(got, err)
	}
	var fingerprint string
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT fingerprint FROM asset_stream_facts WHERE asset_id='basic-asset'`).Scan(&fingerprint); e != nil || fingerprint != in.source.fingerprint {
		t.Fatal("fault did not follow native write", fingerprint, e)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	if err = w.db.QueryRowContext(t.Context(), `SELECT fingerprint FROM asset_stream_facts WHERE asset_id='basic-asset'`).Scan(&fingerprint); err != nil || fingerprint != "old-probe" {
		t.Fatal("late failure escaped rollback", fingerprint, err)
	}
}
