package writer

import (
	"errors"
	"fmt"
	"testing"
)

// Every literal population and manifest is set while phase0, before the one
// FULL startup Capture. Subsequent turns reuse its registry and real owner
// acceptance/prefence; only the native position CAS enters the tested family.
func basicEmptyFamilyFixture(t *testing.T, family int64, nonempty, stale bool) (*commandTurn, basicStreamCopyFence) {
	t.Helper()
	w, input := basicCompleteAcceptFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Old parents deliberately retain historical revisions, rather than
	// manufacturing a current parent to satisfy a copy component.
	exec(`INSERT INTO asset_stream_facts VALUES('basic-asset',17,39,-19,'old',1)`)
	exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,has_asset_stream_breadth,breadth_audio_variant) VALUES('basic-asset',9,'audio','aac','fr','old stream',2,'stereo',0,0,1,'old breadth')`)
	exec(`INSERT INTO asset_chapter_facts VALUES('basic-asset',17,39,-19,'old','old:17')`)
	exec(`INSERT INTO asset_chapters VALUES('basic-asset',9,'old chapter',1,2)`)
	exec(`INSERT INTO subtitle_sidecars VALUES('shared','/shared.srt',2,3,'keep')`)
	var captured []basicMediaRow
	if nonempty {
		// These match the actual staged rows, and refusal is due to the
		// frozen family being nonempty, before any native operation.
		captured = []basicMediaRow{basicChapter{1, "Opening", 0, 3}, basicChapter{2, "Closing", 3, 9}}
	} else if !stale {
		if family == 2 || family == 3 {
			exec(`DELETE FROM catalog_info_new_stream_breadth WHERE change_id=51`)
			exec(`UPDATE catalog_info_input_families SET rows_expected=0,rows_staged=0 WHERE change_id=51 AND family=3`)
			exec(`DELETE FROM catalog_info_new_streams WHERE change_id=51`)
			exec(`UPDATE catalog_info_input_families SET rows_expected=0,rows_staged=0 WHERE change_id=51 AND family=2`)
			input.counts[1] = 0
			input.counts[2] = 0
			input.stream.rawShape.Streams = nil
		}
		if family == 7 {
			exec(`DELETE FROM catalog_info_new_subtitles WHERE change_id=51`)
			exec(`UPDATE catalog_info_input_families SET rows_expected=0,rows_staged=0 WHERE change_id=51 AND family=8`)
			input.counts[7] = 0
		}
		tables := map[int64]string{2: "catalog_info_new_streams", 3: "catalog_info_new_stream_breadth", 5: "catalog_info_new_chapters", 7: "catalog_info_new_subtitle_sidecars"}
		exec(`DELETE FROM ` + tables[family] + ` WHERE change_id=51`)
		exec(`UPDATE catalog_info_input_families SET rows_expected=0,rows_staged=0 WHERE change_id=51 AND family=?`, family)
		input.counts[family-1] = 0
		var zero bool
		if err := w.db.QueryRowContext(t.Context(), `SELECT
          (SELECT phase=0 FROM catalog_info_changes WHERE id=51)
          AND (SELECT rows_expected=0 AND rows_staged=0 AND sealed=1 FROM catalog_info_input_families WHERE change_id=51 AND family=?)
          AND (SELECT count(*)=0 FROM `+tables[family]+` WHERE change_id=51)`, family).Scan(&zero); err != nil || !zero {
			t.Fatal("fixture is not a real phase0 sealed zero-row family", zero, err)
		}
	}
	// stale preserves a consistent, genuinely nonzero accepted native manifest
	// while the caller's frozen capture is empty. No post-Capture SQL mutation.
	f, err := prepareBasicMediaFamily(input.stream.source, family, 2, captured)
	if err != nil {
		t.Fatal(err)
	}
	defs := basicCopyStepTestDefinitions()
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_family_step.go", "basic_copy_step_statements.go", "basic_copy_empty_family_test.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	accepted, err := acceptBasicOrdinaryInput(t.Context(), turn, input, 2000)
	if err != nil {
		t.Fatal(err)
	}
	input.stream.changeRevision = accepted.changeRevision
	input.snapshot.changeRevision = accepted.changeRevision
	basicMediaCommit(t, turn)
	turn = basicHandlerTurn(t, w, BasicStage, defs, registry)
	p, err := prefenceBasicOrdinary(t.Context(), turn, basicPrefenceEffects{input: input})
	if err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	turn = basicHandlerTurn(t, w, BasicStage, defs, registry)
	next := p
	next.family = family
	p, err = advanceBasicCopiedPosition(t.Context(), turn, f.source, p, next, input.stream.shapeID)
	if err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	return basicHandlerTurn(t, w, BasicStage, defs, registry), basicStreamCopyFence{family: f, position: p, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision}
}

func basicEmptyFamilyAssertNative(t *testing.T, turn *commandTurn, want basicCopyPosition) {
	t.Helper()
	var native basicCopyPosition
	err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT id,job_revision,phase,after_entity,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3 FROM catalog_info_changes WHERE id=51`).Scan(&native.changeID, &native.changeRevision, &native.phase, &native.afterEntity, &native.family, &native.copyPhase, &native.id, &native.aux, &native.key, &native.key2, &native.key3)
	if err != nil || native != want {
		t.Fatal("same-turn native cursor", native, err, "want", want)
	}
	var intact bool
	err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT info_epoch=17 AND info_publication_pending=1 AND available=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 AND info_shape_id IS NULL AND available=1 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=2 AND target_available=1 FROM catalog_info_changes WHERE id=51)
 AND (SELECT count(*)=0 FROM scan_command_receipts)
 AND (SELECT count(*)=1 FROM asset_streams)
 AND (SELECT title='old stream' AND has_asset_stream_breadth=1 AND breadth_audio_variant='old breadth' FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=9)
 AND (SELECT revision=17 AND fingerprint='old' FROM asset_stream_facts WHERE asset_id='basic-asset')
 AND (SELECT count(*)=1 FROM asset_chapters)
 AND (SELECT title='old chapter' AND start_seconds=1 AND end_seconds=2 FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=9)
 AND (SELECT revision=17 AND fingerprint='old:17' FROM asset_chapter_facts WHERE asset_id='basic-asset')
 AND (SELECT count(*)=1 FROM subtitle_sidecars)
 AND (SELECT path='/shared.srt' AND size=2 AND modified_ns=3 AND digest='keep' FROM subtitle_sidecars WHERE id='shared')`).Scan(&intact)
	if err != nil || !intact {
		t.Fatal("old canonical children/publication fences changed", intact, err)
	}
}

func TestBasicEmptyFamilyEntersNativeCleanup(t *testing.T) {
	for _, family := range []int64{2, 3, 5} {
		t.Run(fmt.Sprint(family), func(t *testing.T) {
			turn, in := basicEmptyFamilyFixture(t, family, false, false)
			got, err := startBasicEmptyFamily(t.Context(), turn, in)
			want := in.position
			want.changeRevision++
			want.copyPhase = 1
			if err != nil || got != want {
				t.Fatal(got, err, "want", want)
			}
			basicEmptyFamilyAssertNative(t, turn, want)
			basicMediaCommit(t, turn)
		})
	}
}

func TestBasicEmptySidecarFamilyRetainsGlobalRows(t *testing.T) {
	turn, in := basicEmptyFamilyFixture(t, 7, false, false)
	got, err := startBasicEmptyFamily(t.Context(), turn, in)
	want := in.position
	want.changeRevision++
	want.family = 8
	if err != nil || got != want {
		t.Fatal(got, err, "want", want)
	}
	basicEmptyFamilyAssertNative(t, turn, want)
	basicMediaCommit(t, turn)
}

func TestBasicEmptyFamilyRefusesNonemptyFrozenOrStaleManifest(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonempty_frozen", true: "stale_actual_manifest"}[stale], func(t *testing.T) {
			turn, in := basicEmptyFamilyFixture(t, 5, !stale, stale)
			got, err := startBasicEmptyFamily(t.Context(), turn, in)
			wantErr := ErrInvalid
			if stale {
				wantErr = ErrConflict
			}
			if !errors.Is(err, wantErr) || got != (basicCopyPosition{}) {
				t.Fatal(got, err, "want", wantErr)
			}
			basicEmptyFamilyAssertNative(t, turn, in.position)
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
		})
	}
}
