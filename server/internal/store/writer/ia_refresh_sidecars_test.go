package writer

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func iaRefreshSidecarNow(t *testing.T, w *executor, id string) (iaRefreshSidecarRow, bool) {
	t.Helper()
	var r iaRefreshSidecarRow
	err := w.db.QueryRow(`SELECT id,path,size,modified_ns,digest FROM subtitle_sidecars WHERE id=?`, id).
		Scan(&r.ID, &r.Path, &r.Size, &r.ModifiedNS, &r.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

func iaRefreshAssetRevisions(t *testing.T, w *executor, id int64) [3]int64 {
	t.Helper()
	var out [3]int64
	if err := w.db.QueryRow(`SELECT playback_origin_revision,prepared_source_revision,info_invalidation_revision FROM catalog_assets WHERE id=?`, id).
		Scan(&out[0], &out[1], &out[2]); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIARefreshSidecarPageOnlyReferencedAndDeduped(t *testing.T) {
	w, r, _, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	for _, id := range []string{"sc-9", "sc-1", "sc-5", "sc-unreferenced"} {
		iaRefreshSeed(t, w, iaRefreshSidecarRow{ID: id, Path: "/media/" + id + ".srt", Size: 1, ModifiedNS: 2, Digest: "d"})
	}
	// sc-1 then sc-9 then sc-1 again: one duplicate reference and one foreign one.
	iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub-embedded", AssetID: "refresh-asset", Origin: "embedded", Locator: "emb", StreamIndex: 1, Format: "srt"})
	for j, id := range []string{"sc-9", "sc-1", "sc-1"} {
		iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: fmt.Sprintf("sub-%d", j), AssetID: "refresh-asset", Origin: "sidecar", Locator: fmt.Sprintf("l%d", j), StreamIndex: -1, SidecarID: &id, Format: "srt"})
	}
	iaRefreshSeedForeignAsset(t, w, 42, "other-asset")
	iaRefreshSeedSubtitleFact(t, w, "other-asset")
	foreignSidecar := "sc-foreign"
	iaRefreshSeed(t, w, iaRefreshSidecarRow{ID: foreignSidecar, Path: "/media/foreign.srt", Size: 1, ModifiedNS: 2, Digest: "d"})
	iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub-foreign", AssetID: "other-asset", Origin: "sidecar", Locator: "l", StreamIndex: -1, SidecarID: &foreignSidecar, Format: "srt"})
	turn, b := iaRefreshTurn(t, w, r, q)
	page, err := iaRefreshSidecarPage(t.Context(), turn, b, "refresh-asset", iaRefreshKey{}, 1)
	if err != nil || !page.more || !reflect.DeepEqual(page.keys, []iaRefreshKey{{6, "sc-1", 0}}) {
		t.Fatal(page, err)
	}
	page, err = iaRefreshSidecarPage(t.Context(), turn, b, "refresh-asset", page.keys[0], 2)
	if err != nil || page.more || !reflect.DeepEqual(page.keys, []iaRefreshKey{{6, "sc-9", 0}}) {
		t.Fatal(page, err)
	}
}

func TestIARefreshSidecarCohortRefusesAboveCap(t *testing.T) {
	w, r, _, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	iaRefreshSeed(t, w, iaRefreshSidecarRow{ID: "sc", Path: "/media/sc.srt", Size: 1, ModifiedNS: 2, Digest: "d"})
	sidecar := "sc"
	for j := 0; j < iaRefreshSubtitleCap+1; j++ {
		wc008NativeExec(t, w, `INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(?,?,'sidecar',?,-1,?,'srt','','',0,0,'',0)`,
			fmt.Sprintf("cap-%d", j), "refresh-asset", fmt.Sprintf("l%d", j), sidecar)
	}
	turn, b := iaRefreshTurn(t, w, r, q)
	page, err := iaRefreshSidecarPage(t.Context(), turn, b, "refresh-asset", iaRefreshKey{}, 1)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || len(page.keys) != 0 {
		t.Fatal("oversized sidecar cohort was trimmed instead of refused", page, err)
	}
}

func TestIARefreshSidecarWitnessPathAndUnlinked(t *testing.T) {
	w, r, _, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	sidecar := iaRefreshSidecarRow{ID: "sc-1", Path: "/media/a.srt", Size: 5, ModifiedNS: 6, Digest: "d1"}
	iaRefreshSeed(t, w, sidecar)
	iaRefreshSeed(t, w, iaRefreshSidecarRow{ID: "sc-3", Path: "/media/c.srt", Size: 1, ModifiedNS: 1, Digest: "d"})
	iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub-1", AssetID: "refresh-asset", Origin: "sidecar", Locator: "l", StreamIndex: -1, SidecarID: &sidecar.ID, Format: "srt"})
	turn, b := iaRefreshTurn(t, w, r, q)
	current, err := iaRefreshSidecarCurrentRow(t.Context(), turn, b, "sc-1")
	if err != nil || !current.found || !reflect.DeepEqual(current.row, sidecar) {
		t.Fatal(current, err)
	}
	current, err = iaRefreshSidecarCurrentRow(t.Context(), turn, b, "missing")
	if err != nil || current.found {
		t.Fatal(current, err)
	}
	for _, c := range []struct {
		row  iaRefreshSidecarRow
		want bool
	}{
		{iaRefreshSidecarRow{ID: "sc-2", Path: "/media/a.srt"}, true},
		{iaRefreshSidecarRow{ID: "sc-1", Path: "/media/a.srt"}, false},
		{iaRefreshSidecarRow{ID: "sc-2", Path: "/media/b.srt"}, false},
	} {
		held, err := iaRefreshSidecarPathHeld(t.Context(), turn, b, c.row)
		if err != nil || held != c.want {
			t.Fatal(c, held, err)
		}
	}
	if unlinked, err := iaRefreshSidecarUnlinked(t.Context(), turn, b, "sc-1"); err != nil || unlinked {
		t.Fatal("referenced shared sidecar reported unlinked", unlinked, err)
	}
	if unlinked, err := iaRefreshSidecarUnlinked(t.Context(), turn, b, "sc-3"); err != nil || !unlinked {
		t.Fatal("unreferenced sidecar reported linked", unlinked, err)
	}
	for name, err := range map[string]error{
		"empty_witness_id": func() error {
			_, e := iaRefreshSidecarCurrentRow(t.Context(), turn, b, "")
			return e
		}(),
		"empty_path": func() error {
			_, e := iaRefreshSidecarPathHeld(t.Context(), turn, b, iaRefreshSidecarRow{ID: "sc-2"})
			return e
		}(),
		"empty_unlink_id": func() error {
			_, e := iaRefreshSidecarUnlinked(t.Context(), turn, b, "")
			return e
		}(),
		"witness_budget": func() error {
			_, e := iaRefreshSidecarCurrentRow(t.Context(), turn, iaEntryBudget{memory: 1}, "sc-1")
			return e
		}(),
		"path_budget": func() error {
			_, e := iaRefreshSidecarPathHeld(t.Context(), turn, iaEntryBudget{memory: 1}, iaRefreshSidecarRow{ID: "sc-2", Path: "/media/x"})
			return e
		}(),
		"unlink_budget": func() error {
			_, e := iaRefreshSidecarUnlinked(t.Context(), turn, iaEntryBudget{memory: 1}, "sc-1")
			return e
		}(),
	} {
		if name == "empty_witness_id" || name == "empty_path" || name == "empty_unlink_id" {
			if !errors.Is(err, ErrInvalid) {
				t.Fatal(name, err)
			}
			continue
		}
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
			t.Fatal(name, err)
		}
	}
}

func TestIARefreshSidecarAbsentPresentCASAndPathRace(t *testing.T) {
	t.Run("present_restore", func(t *testing.T) {
		w, r, p, q := iaRefreshSubtitleFixture(t)
		sidecar := iaRefreshSidecarRow{ID: "sc", Path: "/media/a.srt", Size: 5, ModifiedNS: 6, Digest: "d1"}
		iaRefreshSeed(t, w, sidecar)
		begin := iaRefreshApply(t, w, r, p, q)
		next := sidecar
		next.Path, next.Size, next.ModifiedNS, next.Digest = "/media/b.srt", 7, 8, "d2"
		key := iaRefreshKey{6, sidecar.ID, 0}
		q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, next)
		step := iaRefreshApply(t, w, r, p, q)
		q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
		iaRefreshApply(t, w, r, p, q)
		if got, found := iaRefreshSidecarNow(t, w, sidecar.ID); !found || !reflect.DeepEqual(got, sidecar) {
			t.Fatal("sidecar before-image lost", got, sidecar)
		}
	})
	t.Run("absent_tombstone", func(t *testing.T) {
		w, r, p, q := iaRefreshSubtitleFixture(t)
		begin := iaRefreshApply(t, w, r, p, q)
		next := iaRefreshSidecarRow{ID: "sc-new", Path: "/media/new.srt", Size: 1, ModifiedNS: 2, Digest: "d"}
		key := iaRefreshKey{6, next.ID, 0}
		q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, next)
		step := iaRefreshApply(t, w, r, p, q)
		q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
		iaRefreshApply(t, w, r, p, q)
		if _, found := iaRefreshSidecarNow(t, w, next.ID); found || iaRefreshCount(t, w, `SELECT old_exists FROM catalog_info_undo_subtitle_sidecars WHERE id='sc-new'`) != 0 {
			t.Fatal("absent sidecar tombstone lost")
		}
	})
	t.Run("cas_race", func(t *testing.T) {
		w, r, p, q := iaRefreshSubtitleFixture(t)
		sidecar := iaRefreshSidecarRow{ID: "sc", Path: "/media/a.srt", Size: 5, ModifiedNS: 6, Digest: "d1"}
		iaRefreshSeed(t, w, sidecar)
		begin := iaRefreshApply(t, w, r, p, q)
		key := iaRefreshKey{6, sidecar.ID, 0}
		next := sidecar
		next.Digest = "d2"
		q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, next)
		step := iaRefreshApply(t, w, r, p, q)
		// Another accepted source publishes the same shared row after this step.
		wc008NativeExec(t, w, `UPDATE subtitle_sidecars SET digest='newer-accepted' WHERE id='sc'`)
		q = iaRefreshNext(t, w, q, step, IARefreshStep, key, next)
		turn, b := iaRefreshTurn(t, w, r, q)
		out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
		if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
			t.Fatal("shared CAS race overwrote a newer accepted tuple", out, err)
		}
		turn.transaction.rollback(err)
		if got, found := iaRefreshSidecarNow(t, w, "sc"); !found || got.Digest != "newer-accepted" {
			t.Fatal("newer accepted shared tuple lost", got)
		}
	})
	t.Run("path_race", func(t *testing.T) {
		w, r, p, q := iaRefreshSubtitleFixture(t)
		sidecar := iaRefreshSidecarRow{ID: "sc-1", Path: "/media/a.srt", Size: 5, ModifiedNS: 6, Digest: "d1"}
		iaRefreshSeed(t, w, sidecar)
		begin := iaRefreshApply(t, w, r, p, q)
		key := iaRefreshKey{6, sidecar.ID, 0}
		next := sidecar
		next.Path = "/media/b.srt"
		q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, next)
		step := iaRefreshApply(t, w, r, p, q)
		// Another accepted source now owns the path the journal wants to restore.
		wc008NativeExec(t, w, `INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES('sc-2','/media/a.srt',1,2,'other')`)
		q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
		turn, b := iaRefreshTurn(t, w, r, q)
		out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
		if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
			t.Fatal("old path restore overwrote another accepted source", out, err)
		}
		turn.transaction.rollback(err)
		if got, found := iaRefreshSidecarNow(t, w, "sc-2"); !found || got.Path != "/media/a.srt" {
			t.Fatal("other accepted path lost", got)
		}
		if got, found := iaRefreshSidecarNow(t, w, "sc-1"); !found || got.Path != "/media/b.srt" {
			t.Fatal("shared sidecar changed during a refused restore", got)
		}
	})
	t.Run("unlink_while_referenced", func(t *testing.T) {
		w, r, p, q := iaRefreshSubtitleFixture(t)
		iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
		sidecar := iaRefreshSidecarRow{ID: "sc", Path: "/media/a.srt", Size: 5, ModifiedNS: 6, Digest: "d1"}
		iaRefreshSeed(t, w, sidecar)
		iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub", AssetID: "refresh-asset", Origin: "sidecar", Locator: "l", StreamIndex: -1, SidecarID: &sidecar.ID, Format: "srt"})
		begin := iaRefreshApply(t, w, r, p, q)
		q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{6, sidecar.ID, 0}, nil)
		turn, b := iaRefreshTurn(t, w, r, q)
		out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
		if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
			t.Fatal("referenced shared sidecar was unlinked", out, err)
		}
		turn.transaction.rollback(err)
		if _, found := iaRefreshSidecarNow(t, w, "sc"); !found || iaRefreshCount(t, w, `SELECT count(*) FROM asset_subtitles WHERE id='sub'`) != 1 {
			t.Fatal("referenced sidecar/subtitle lost")
		}
	})
}

func TestIARefreshSidecarChangeInvalidatesDerivedPlayback(t *testing.T) {
	// #1431 seam assumption: MP's dedicated mp_subtitle_invalidation files do
	// not exist on main. IA20's shared iaRefreshInvalidate bumps the accepted
	// playback-origin/prepared-source/info-invalidation witnesses on every own
	// mutation, so a changed shared tuple invalidates derived playback exactly
	// once and an identical rewrite invalidates nothing.
	w, r, p, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedForeignAsset(t, w, 42, "other-asset")
	sidecar := iaRefreshSidecarRow{ID: "sc", Path: "/media/a.srt", Size: 5, ModifiedNS: 6, Digest: "d1"}
	iaRefreshSeed(t, w, sidecar)
	begin := iaRefreshApply(t, w, r, p, q)
	key := iaRefreshKey{6, sidecar.ID, 0}
	next := sidecar
	next.ModifiedNS, next.Digest = 7, "d2"
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, next)
	step := iaRefreshApply(t, w, r, p, q)
	if got := iaRefreshAssetRevisions(t, w, 41); got != [3]int64{2, 2, 2} {
		t.Fatal("derived playback was not invalidated exactly once", got)
	}
	if got := iaRefreshAssetRevisions(t, w, 42); got != [3]int64{1, 1, 1} {
		t.Fatal("invalidation fanned out to another library asset", got)
	}
	// Repeating the identical tuple is not a publication and must not invalidate.
	q = iaRefreshNext(t, w, q, step, IARefreshStep, key, next)
	step = iaRefreshApply(t, w, r, p, q)
	if got := iaRefreshAssetRevisions(t, w, 41); got != [3]int64{2, 2, 2} {
		t.Fatal("identical sidecar rewrite invalidated derived playback", got)
	}
	q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
	iaRefreshApply(t, w, r, p, q)
	if got := iaRefreshAssetRevisions(t, w, 41); got != [3]int64{3, 3, 3} {
		t.Fatal("restored shared tuple did not invalidate derived playback", got)
	}
	if got, found := iaRefreshSidecarNow(t, w, "sc"); !found || !reflect.DeepEqual(got, sidecar) {
		t.Fatal("sidecar exact restore changed the tuple", got)
	}
}
