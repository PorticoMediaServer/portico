package writer

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The fixture's seeded debt plus the pin's new shape row, which stays charged
// like a retired input's: everything the walk staged has been refunded.
const basicAbandonTestEndDebt = 1000 + 168 + 2*3 + 2*4

func basicAbandonSeedBudget(t *testing.T, walk basicWalk, used int64) {
	t.Helper()
	if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1`, used); err != nil {
		t.Fatal(err)
	}
}

func basicAbandonExpected(t *testing.T, walk basicWalk) *basicAbandonExpectation {
	t.Helper()
	a := &basicAbandonExpectation{after: walk.probe.after}
	if err := walk.e.db.QueryRowContext(t.Context(), `SELECT playback_origin_revision,prepared_source_revision FROM catalog_assets WHERE id=41`).Scan(&a.playbackRevision, &a.preparedRevision); err != nil {
		t.Fatal(err)
	}
	return a
}

func basicAbandonRequest(s basicStreamFactStage, revision int64, a *basicAbandonExpectation) basicControlRequest {
	return basicControlRequest{operation: basicControlAbandon, source: s, changeRevision: revision, abandon: a}
}

// Drives every abandon turn by its actual continuation until the header is gone.
func basicAbandonToEnd(t *testing.T, walk basicWalk, revision int64, a *basicAbandonExpectation) {
	t.Helper()
	for turns := 0; ; turns++ {
		if turns > 16 {
			t.Fatal("abandon did not finish")
		}
		got, err := walk.chain.stage(basicAbandonRequest(walk.s, revision, a))
		if err != nil {
			t.Fatal("abandon turn", turns, err)
		}
		if got.Continuation == (BasicContinuation{}) {
			return
		}
	}
}

func basicAbandonAssertRolledBack(t *testing.T, walk basicWalk) {
	t.Helper()
	var container, video, audio string
	var width, height, playback, prepared, pending, refs, headers, used int64
	var duration float64
	var link sql.NullInt64
	e := walk.e
	if err := e.db.QueryRowContext(t.Context(), `SELECT container,video_codec,audio_codec,width,height,duration,playback_origin_revision,prepared_source_revision,info_publication_pending FROM catalog_assets WHERE id=41`).Scan(&container, &video, &audio, &width, &height, &duration, &playback, &prepared, &pending); err != nil {
		t.Fatal(err)
	}
	if container != "old" || video != "vp9" || audio != "aac" || width != 1920 || height != 720 || duration != 90 || playback != 101 || prepared != 103 || pending != 0 {
		t.Fatal("asset not rolled back", container, video, audio, width, height, duration, playback, prepared, pending)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41),(SELECT ref_count FROM catalog_info_shapes WHERE id=?),(SELECT count(*) FROM catalog_info_changes WHERE id=51),(SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1)`, walk.shape).Scan(&link, &refs, &headers, &used); err != nil {
		t.Fatal(err)
	}
	if link.Valid || refs != 0 || headers != 0 || used != basicAbandonTestEndDebt {
		t.Fatal("abandon end state", link, refs, headers, used)
	}
	for _, table := range basicAbandonTables {
		var rows int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table.name+` WHERE change_id=51`).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("staged rows survived", table.name, rows, err)
		}
	}
	basicDispatchReturned(t, e, walk.authority)
}

func TestBasicAbandonRollsBackAndReclaims(t *testing.T) {
	// Begin's own charge is seeded with its header; staging adds the rest.
	setup := func(t *testing.T) basicWalk {
		walk := basicWalkThroughPin(t)
		return walk
	}
	seedUsed := func(t *testing.T, walk basicWalk) {
		fixed, err := basicAbandonHeaderCharge(walk.s)
		if err != nil {
			t.Fatal(err)
		}
		var used int64
		if err := walk.e.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used); err != nil {
			t.Fatal(err)
		}
		basicAbandonSeedBudget(t, walk, used+fixed)
	}
	t.Run("after_pin_at_phase_0", func(t *testing.T) {
		walk := setup(t)
		seedUsed(t, walk)
		basicAbandonToEnd(t, walk, walk.revision, basicAbandonExpected(t, walk))
		basicAbandonAssertRolledBack(t, walk)
	})
	t.Run("after_freeze_and_entity", func(t *testing.T) {
		walk := setup(t)
		seedUsed(t, walk)
		a := basicAbandonExpected(t, walk)
		revision := walk.revision
		for _, op := range []basicControlOperation{basicControlFreeze, basicControlSelectEntity} {
			if _, err := walk.chain.stage(basicControlRequest{operation: op, source: walk.s, changeRevision: revision, entityID: 31, shapeID: walk.shape}); err != nil {
				t.Fatal(op, err)
			}
			revision++
		}
		basicAbandonToEnd(t, walk, revision, a)
		basicAbandonAssertRolledBack(t, walk)
	})
	t.Run("restart_after_the_marker_ignores_the_tuple", func(t *testing.T) {
		walk := setup(t)
		seedUsed(t, walk)
		a := basicAbandonExpected(t, walk)
		if _, err := walk.chain.stage(basicAbandonRequest(walk.s, walk.revision, a)); err != nil {
			t.Fatal(err)
		}
		// The scalars are already OLD; a stale expectation must not matter now.
		basicAbandonToEnd(t, walk, walk.revision, a)
		basicAbandonAssertRolledBack(t, walk)
	})
	t.Run("marked_header_refuses_the_walk", func(t *testing.T) {
		walk := setup(t)
		a := basicAbandonExpected(t, walk)
		if _, err := walk.chain.stage(basicAbandonRequest(walk.s, walk.revision, a)); err != nil {
			t.Fatal(err)
		}
		if _, err := walk.chain.stage(basicControlRequest{operation: basicControlFreeze, source: walk.s, changeRevision: walk.revision + 1, entityID: 31, shapeID: walk.shape}); !errors.Is(err, ErrConflict) {
			t.Fatal("freeze on a marked header", err)
		}
		if _, err := walk.chain.stage(basicControlRequest{operation: basicControlStreamFact, source: walk.s, family: 1}); !errors.Is(err, ErrConflict) {
			t.Fatal("stage on a marked header", err)
		}
		basicDispatchReturned(t, walk.e, walk.authority)
	})
	refused := func(t *testing.T, walk basicWalk, a *basicAbandonExpectation) {
		t.Helper()
		if _, err := walk.chain.stage(basicAbandonRequest(walk.s, walk.revision, a)); !errors.Is(err, ErrConflict) {
			t.Fatal("abandon was not refused", err)
		}
		basicDispatchReturned(t, walk.e, walk.authority)
		var container string
		var aux, revision int64
		if err := walk.e.db.QueryRowContext(t.Context(), `SELECT (SELECT container FROM catalog_assets WHERE id=41),(SELECT copy_aux FROM catalog_info_changes WHERE id=51),(SELECT job_revision FROM catalog_info_changes WHERE id=51)`).Scan(&container, &aux, &revision); err != nil {
			t.Fatal(err)
		}
		if container != "mkv" || aux < 0 || revision != walk.revision {
			t.Fatal("refused abandon had an effect", container, aux, revision)
		}
	}
	t.Run("before_the_probe_nothing_to_restore", func(t *testing.T) {
		walk := basicWalkStaged(t)
		seedUsed(t, walk)
		// A producer's intended tuple the probe never applied must not refuse.
		a := &basicAbandonExpectation{after: basicProbeScalars{"mkv", "h264", "", 0, 1080, 100}, playbackRevision: 999, preparedRevision: 999}
		basicAbandonToEnd(t, walk, walk.revision, a)
		basicDispatchReturned(t, walk.e, walk.authority)
		var container string
		var playback, headers, used int64
		if err := walk.e.db.QueryRowContext(t.Context(), `SELECT (SELECT container FROM catalog_assets WHERE id=41),(SELECT playback_origin_revision FROM catalog_assets WHERE id=41),(SELECT count(*) FROM catalog_info_changes WHERE id=51),(SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1)`).Scan(&container, &playback, &headers, &used); err != nil {
			t.Fatal(err)
		}
		if container != "old" || playback != 101 || headers != 0 || used != 1000 {
			t.Fatal("abandon before the probe", container, playback, headers, used)
		}
	})
	copyStarted := func(t *testing.T, walk basicWalk, a *basicAbandonExpectation, revision, phase, family int64) {
		t.Helper()
		if _, err := walk.chain.stage(basicAbandonRequest(walk.s, revision, a)); !errors.Is(err, ErrConflict) {
			t.Fatal("abandon after copy started", err)
		}
		basicDispatchReturned(t, walk.e, walk.authority)
		var gotPhase, gotFamily, aux, gotRevision, pending int64
		var container string
		var link sql.NullInt64
		if err := walk.e.db.QueryRowContext(t.Context(), `SELECT (SELECT phase FROM catalog_info_changes WHERE id=51),(SELECT copy_family FROM catalog_info_changes WHERE id=51),(SELECT copy_aux FROM catalog_info_changes WHERE id=51),(SELECT job_revision FROM catalog_info_changes WHERE id=51),(SELECT info_publication_pending FROM catalog_assets WHERE id=41),(SELECT container FROM catalog_assets WHERE id=41),(SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&gotPhase, &gotFamily, &aux, &gotRevision, &pending, &container, &link); err != nil {
			t.Fatal(err)
		}
		if gotPhase != phase || gotFamily != family || aux < 0 || gotRevision != revision || pending != 1 || container != "mkv" || !link.Valid || link.Int64 != 51 {
			t.Fatal("refused abandon had an effect", gotPhase, gotFamily, aux, gotRevision, pending, container, link)
		}
	}
	t.Run("refused_once_copy_has_started", func(t *testing.T) {
		walk := setup(t)
		a := basicAbandonExpected(t, walk)
		revision := walk.revision
		for _, op := range []basicControlOperation{basicControlFreeze, basicControlSelectEntity} {
			if _, err := walk.chain.stage(basicControlRequest{operation: op, source: walk.s, changeRevision: revision, entityID: 31, shapeID: walk.shape}); err != nil {
				t.Fatal(op, err)
			}
			revision++
		}
		in := basicControlRequest{operation: basicControlCopyFamily, family: 1, source: walk.s, changeRevision: revision, entityID: 31, shapeID: walk.shape}
		if _, err := walk.chain.stage(in); err != nil {
			t.Fatal("copy family 1", err)
		}
		copyStarted(t, walk, a, revision+1, 2, 2)
	})
	t.Run("refused_at_phase_3", func(t *testing.T) {
		walk := setup(t)
		a := basicAbandonExpected(t, walk)
		revision := basicWalkCopyAll(t, walk)
		copyStarted(t, walk, a, revision, 3, 0)
	})
	mark := func(t *testing.T, walk basicWalk) {
		t.Helper()
		if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET copy_aux=-1 WHERE id=51`); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("marked_header_refuses_entity_and_copy", func(t *testing.T) {
		walk := setup(t)
		revision := walk.revision
		if _, err := walk.chain.stage(basicControlRequest{operation: basicControlFreeze, source: walk.s, changeRevision: revision, entityID: 31, shapeID: walk.shape}); err != nil {
			t.Fatal(err)
		}
		revision++
		entity := basicControlRequest{operation: basicControlSelectEntity, source: walk.s, changeRevision: revision, entityID: 31, shapeID: walk.shape}
		copy1 := basicControlRequest{operation: basicControlCopyFamily, family: 1, source: walk.s, changeRevision: revision + 1, entityID: 31, shapeID: walk.shape}
		mark(t, walk)
		if _, err := walk.chain.stage(entity); !errors.Is(err, ErrConflict) {
			t.Fatal("entity on a marked header", err)
		}
		if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET copy_aux=0 WHERE id=51`); err != nil {
			t.Fatal(err)
		}
		if _, err := walk.chain.stage(entity); err != nil {
			t.Fatal(err)
		}
		mark(t, walk)
		if _, err := walk.chain.stage(copy1); !errors.Is(err, ErrConflict) {
			t.Fatal("copy on a marked header", err)
		}
		basicDispatchReturned(t, walk.e, walk.authority)
	})
	t.Run("marked_header_refuses_activation", func(t *testing.T) {
		walk := setup(t)
		revision := basicWalkCopyAll(t, walk)
		mark(t, walk)
		if _, err := basicWalkAccept(t, walk, revision); err == nil {
			t.Fatal("Accept on a marked header")
		}
		basicDispatchReturned(t, walk.e, walk.authority)
		var phase int64
		if err := walk.e.db.QueryRowContext(t.Context(), `SELECT phase FROM catalog_info_changes WHERE id=51`).Scan(&phase); err != nil || phase != 3 {
			t.Fatal("refused Accept moved the change", phase, err)
		}
	})
	// Settle and publish run only after activation in one Accept, so the header
	// guard above stops them first; each also binds a zero cursor itself.
	t.Run("settle_and_publish_bind_the_zero_cursor", func(t *testing.T) {
		accept := basicFinalDefinitions(basicAcceptBounds{nfo: true})
		for _, id := range []stmt.ID{basicSettleProof, basicPublishLeafProof} {
			found := false
			for _, d := range accept {
				if d.ID == id {
					found = true
					if !strings.Contains(d.SQL, "j.copy_aux=0") {
						t.Fatal("no zero-cursor guard", id, d.SQL)
					}
				}
			}
			if !found {
				t.Fatal("statement not in the Accept set", id)
			}
		}
	})
	t.Run("expectation_only_on_abandon", func(t *testing.T) {
		walk := setup(t)
		in := basicControlRequest{operation: basicControlFreeze, source: walk.s, changeRevision: walk.revision, entityID: 31, shapeID: walk.shape, abandon: basicAbandonExpected(t, walk)}
		if _, err := walk.chain.stage(in); !errors.Is(err, ErrInvalid) {
			t.Fatal("freeze carrying an abandon expectation", err)
		}
		if _, err := walk.chain.stage(basicAbandonRequest(walk.s, walk.revision, nil)); !errors.Is(err, ErrInvalid) {
			t.Fatal("abandon with no expectation", err)
		}
	})
	t.Run("scalar_mismatch_refuses", func(t *testing.T) {
		walk := setup(t)
		a := basicAbandonExpected(t, walk)
		a.after.height++
		refused(t, walk, a)
	})
	t.Run("counter_mismatch_refuses", func(t *testing.T) {
		walk := setup(t)
		a := basicAbandonExpected(t, walk)
		a.preparedRevision++
		refused(t, walk, a)
	})
	t.Run("released_shape_refuses", func(t *testing.T) {
		walk := setup(t)
		a := basicAbandonExpected(t, walk)
		if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_shapes SET ref_count=0 WHERE id=?`, walk.shape); err != nil {
			t.Fatal(err)
		}
		refused(t, walk, a)
	})
}
