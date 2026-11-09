package writer

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// IA22's fixture registers the subtitle/sidecar reads next to IA20's core so a
// walk and the Step it names share one native snapshot. The C019 journal
// fragment and security producers are reused; no new authority is invented.
func iaRefreshSubtitleFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRefreshRequest) {
	t.Helper()
	w, r, p, q := iaRefreshFixture(t)
	defs := append([]stmt.Definition{}, iaRefreshDefinitions()...)
	defs = append(defs, iaRefreshSubtitleSidecarDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	r.Registry = registry
	return w, r, p, q
}

// IA22 subtitle samples. Every helper here reads the exact native columns; no
// test asserts wall-clock timing and no fixture invents new authority.
func iaRefreshSubtitleFactNow(t *testing.T, w *executor, asset string) (iaRefreshSubtitleFactRow, bool) {
	t.Helper()
	var r iaRefreshSubtitleFactRow
	err := w.db.QueryRow(`SELECT asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence FROM asset_subtitle_facts WHERE asset_id=?`, asset).
		Scan(&r.AssetID, &r.Revision, &r.Size, &r.ModifiedNS, &r.OriginUs, &r.TimingKnown, &r.Status, &r.Fingerprint, &r.HasSubtitleInventoryEvidence, &r.InventoryRevision, &r.InventoryEvidence)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

func iaRefreshSubtitleNow(t *testing.T, w *executor, id string) (iaRefreshSubtitleRow, bool) {
	t.Helper()
	var r iaRefreshSubtitleRow
	err := w.db.QueryRow(`SELECT id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired FROM asset_subtitles WHERE id=?`, id).
		Scan(&r.ID, &r.AssetID, &r.Origin, &r.Locator, &r.StreamIndex, &r.SidecarID, &r.Format, &r.Language, &r.Title, &r.IsDefault, &r.IsForced, &r.Reason, &r.HearingImpaired)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

func iaRefreshSeedSubtitleFact(t *testing.T, w *executor, asset string) {
	t.Helper()
	iaRefreshSeed(t, w, iaRefreshSubtitleFactRow{AssetID: asset, Revision: 1, Size: 42, ModifiedNS: 11, Status: "ready"})
}

func iaRefreshSeedForeignAsset(t *testing.T, w *executor, id int64, token string) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,7,'/media/other',1,1,'','','',0,0,0)`, id, token)
}

func TestIARefreshSubtitlePageOrderedKeysetAndFactKey(t *testing.T) {
	w, r, _, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	for _, id := range []string{"sub-9", "sub-1", "sub-5", "sub-3"} {
		iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: id, AssetID: "refresh-asset", Origin: "embedded", Locator: id, StreamIndex: 1, Format: "srt"})
	}
	iaRefreshSeedForeignAsset(t, w, 42, "other-asset")
	iaRefreshSeedSubtitleFact(t, w, "other-asset")
	iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "foreign", AssetID: "other-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt"})
	fact, err := iaRefreshSubtitleFactKey("refresh-asset")
	if err != nil || fact.family != 5 || fact.token != "refresh-asset" || fact.index != 0 {
		t.Fatal(fact, err)
	}
	if _, err = iaRefreshSubtitleFactKey(""); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty asset key accepted", err)
	}
	turn, b := iaRefreshTurn(t, w, r, q)
	page, err := iaRefreshSubtitlePage(t.Context(), turn, b, "refresh-asset", iaRefreshKey{}, 2)
	if err != nil || !page.more || !reflect.DeepEqual(page.keys, []iaRefreshKey{{7, "sub-1", 0}, {7, "sub-3", 0}}) {
		t.Fatal(page, err)
	}
	page, err = iaRefreshSubtitlePage(t.Context(), turn, b, "refresh-asset", page.keys[len(page.keys)-1], 2)
	if err != nil || page.more || !reflect.DeepEqual(page.keys, []iaRefreshKey{{7, "sub-5", 0}, {7, "sub-9", 0}}) {
		t.Fatal(page, err)
	}
	page, err = iaRefreshSubtitlePage(t.Context(), turn, b, "refresh-asset", page.keys[len(page.keys)-1], 2)
	if err != nil || page.more || len(page.keys) != 0 {
		t.Fatal(page, err)
	}
}

func TestIARefreshSubtitlePageRefusesBadLimitsCursorsAndBudget(t *testing.T) {
	w, r, _, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub-1", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt"})
	turn, b := iaRefreshTurn(t, w, r, q)
	for name, call := range map[string]func() error{
		"zero_limit": func() error {
			_, err := iaRefreshSubtitlePage(t.Context(), turn, b, "refresh-asset", iaRefreshKey{}, 0)
			return err
		},
		"over_page": func() error {
			_, err := iaRefreshSubtitlePage(t.Context(), turn, b, "refresh-asset", iaRefreshKey{}, iaRefreshWalkPage+1)
			return err
		},
		"empty_asset": func() error {
			_, err := iaRefreshSubtitlePage(t.Context(), turn, b, "", iaRefreshKey{}, 1)
			return err
		},
		"foreign_after_family": func() error {
			_, err := iaRefreshSubtitlePage(t.Context(), turn, b, "refresh-asset", iaRefreshKey{family: 6, token: "sidecar"}, 1)
			return err
		},
		"sidecar_after_family": func() error {
			_, err := iaRefreshSidecarPage(t.Context(), turn, b, "refresh-asset", iaRefreshKey{family: 7, token: "sub-1"}, 1)
			return err
		},
		"subtitle_budget": func() error {
			_, err := iaRefreshSubtitlePage(t.Context(), turn, iaEntryBudget{memory: 1}, "refresh-asset", iaRefreshKey{}, 1)
			return err
		},
		"sidecar_budget": func() error {
			_, err := iaRefreshSidecarPage(t.Context(), turn, iaEntryBudget{memory: 1}, "refresh-asset", iaRefreshKey{}, 1)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !errors.Is(err, ErrInvalid) {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestIARefreshSubtitleUniqueTupleDefers(t *testing.T) {
	w, r, _, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub-a", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt"})
	turn, b := iaRefreshTurn(t, w, r, q)
	cases := []struct {
		name string
		row  iaRefreshSubtitleRow
		want bool
	}{
		{"same_tuple_other_id", iaRefreshSubtitleRow{ID: "sub-b", AssetID: "refresh-asset", Origin: "embedded", Locator: "0"}, true},
		{"same_id", iaRefreshSubtitleRow{ID: "sub-a", AssetID: "refresh-asset", Origin: "embedded", Locator: "0"}, false},
		{"other_locator", iaRefreshSubtitleRow{ID: "sub-b", AssetID: "refresh-asset", Origin: "embedded", Locator: "1"}, false},
		{"other_origin", iaRefreshSubtitleRow{ID: "sub-b", AssetID: "refresh-asset", Origin: "sidecar", Locator: "0"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			held, err := iaRefreshSubtitleUniqueHeld(t.Context(), turn, b, c.row)
			if err != nil || held != c.want {
				t.Fatal(held, err)
			}
		})
	}
	for _, bad := range []iaRefreshSubtitleRow{
		{ID: "", AssetID: "refresh-asset", Origin: "embedded", Locator: "0"},
		{ID: "sub-b", AssetID: "", Origin: "embedded", Locator: "0"},
		{ID: "sub-b", AssetID: "refresh-asset", Origin: "unknown", Locator: "0"},
		{ID: "sub-b", AssetID: "refresh-asset", Origin: "embedded", Locator: ""},
	} {
		if _, err := iaRefreshSubtitleUniqueHeld(t.Context(), turn, b, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid tuple probe accepted", bad, err)
		}
	}
	if _, err := iaRefreshSubtitleUniqueHeld(t.Context(), turn, iaEntryBudget{memory: 1}, cases[0].row); err == nil {
		t.Fatal("unfunded tuple probe accepted")
	}
}

func TestIARefreshSubtitleFactRestoresFoldedInventoryEvidence(t *testing.T) {
	revision := int64(7)
	evidence := "folded-old-evidence"
	revision2 := int64(8)
	evidence2 := "folded-new-evidence"
	for _, present := range []bool{true, false} {
		t.Run(map[bool]string{true: "present", false: "absent"}[present], func(t *testing.T) {
			w, r, p, q := iaRefreshSubtitleFixture(t)
			old := iaRefreshSubtitleFactRow{AssetID: "refresh-asset", Revision: 3, Size: 42, ModifiedNS: 11, OriginUs: 5, TimingKnown: 1, Status: "ready", Fingerprint: "fp-old"}
			if present {
				old.HasSubtitleInventoryEvidence, old.InventoryRevision, old.InventoryEvidence = 1, &revision, &evidence
			}
			iaRefreshSeed(t, w, old)
			begin := iaRefreshApply(t, w, r, p, q)
			key, err := iaRefreshSubtitleFactKey("refresh-asset")
			if err != nil {
				t.Fatal(err)
			}
			next := old
			next.Revision, next.Fingerprint = 4, "fp-new"
			next.HasSubtitleInventoryEvidence, next.InventoryRevision, next.InventoryEvidence = 1, &revision2, &evidence2
			q = iaRefreshNext(t, w, q, begin, IARefreshStep, key, next)
			step := iaRefreshApply(t, w, r, p, q)
			q = iaRefreshNext(t, w, q, step, IARefreshRollback, key, nil)
			iaRefreshApply(t, w, r, p, q)
			got, found := iaRefreshSubtitleFactNow(t, w, "refresh-asset")
			if !found || !reflect.DeepEqual(got, old) {
				t.Fatal("folded subtitle fact before-image lost", got, old)
			}
			if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_subtitle_facts WHERE old_exists=1`) != 1 {
				t.Fatal("presence tombstone lost")
			}
		})
	}
}

func TestIARefreshSubtitleRowsExactRestore(t *testing.T) {
	w, r, p, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	sidecar := iaRefreshSidecarRow{ID: "sc", Path: "/media/sub.srt", Size: 9, ModifiedNS: 6, Digest: "d1"}
	iaRefreshSeed(t, w, sidecar)
	embedded := iaRefreshSubtitleRow{ID: "sub-embedded", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt", Language: "en", Title: "English", IsDefault: 1, Reason: "matched"}
	sidecarRow := iaRefreshSubtitleRow{ID: "sub-sidecar", AssetID: "refresh-asset", Origin: "sidecar", Locator: "sub", StreamIndex: -1, SidecarID: &sidecar.ID, Format: "srt", Language: "fr", Title: "French", IsForced: 1, HearingImpaired: 1}
	iaRefreshSeed(t, w, embedded)
	iaRefreshSeed(t, w, sidecarRow)
	begin := iaRefreshApply(t, w, r, p, q)
	embeddedNext := embedded
	embeddedNext.Language, embeddedNext.Title, embeddedNext.IsDefault, embeddedNext.Reason = "de", "Neu", 0, "changed"
	sidecarNext := sidecarRow
	sidecarNext.Locator, sidecarNext.Title, sidecarNext.HearingImpaired = "sub-moved", "Nouveau", 0
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{7, embedded.ID, 0}, embeddedNext)
	step := iaRefreshApply(t, w, r, p, q)
	q = iaRefreshNext(t, w, q, step, IARefreshStep, iaRefreshKey{7, sidecarRow.ID, 0}, sidecarNext)
	step = iaRefreshApply(t, w, r, p, q)
	q = iaRefreshNext(t, w, q, step, IARefreshRollback, iaRefreshKey{7, embedded.ID, 0}, nil)
	restored := iaRefreshApply(t, w, r, p, q)
	if got, found := iaRefreshSubtitleNow(t, w, embedded.ID); !found || !reflect.DeepEqual(got, embedded) {
		t.Fatal("embedded subtitle before-image lost", got, embedded)
	}
	q = iaRefreshNext(t, w, q, restored, IARefreshRollback, iaRefreshKey{7, sidecarRow.ID, 0}, nil)
	iaRefreshApply(t, w, r, p, q)
	if got, found := iaRefreshSubtitleNow(t, w, sidecarRow.ID); !found || !reflect.DeepEqual(got, sidecarRow) {
		t.Fatal("sidecar subtitle before-image lost", got, sidecarRow)
	}
}

func TestIARefreshSubtitleAbsentRowTombstone(t *testing.T) {
	w, r, p, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	begin := iaRefreshApply(t, w, r, p, q)
	absent := iaRefreshSubtitleRow{ID: "sub-new", AssetID: "refresh-asset", Origin: "embedded", Locator: "9", StreamIndex: 9, Format: "srt"}
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{7, absent.ID, 0}, absent)
	step := iaRefreshApply(t, w, r, p, q)
	q = iaRefreshNext(t, w, q, step, IARefreshRollback, iaRefreshKey{7, absent.ID, 0}, nil)
	iaRefreshApply(t, w, r, p, q)
	if _, found := iaRefreshSubtitleNow(t, w, absent.ID); found || iaRefreshCount(t, w, `SELECT old_exists FROM catalog_info_undo_asset_subtitles WHERE id='sub-new'`) != 0 {
		t.Fatal("absent subtitle tombstone lost")
	}
}

func TestIARefreshSubtitleTupleConflictRefusesWithoutEffects(t *testing.T) {
	w, r, p, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	first := iaRefreshSubtitleRow{ID: "sub-a", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt"}
	second := iaRefreshSubtitleRow{ID: "sub-b", AssetID: "refresh-asset", Origin: "embedded", Locator: "1", StreamIndex: 2, Format: "srt"}
	iaRefreshSeed(t, w, first)
	iaRefreshSeed(t, w, second)
	begin := iaRefreshApply(t, w, r, p, q)
	conflict := second
	conflict.Locator = "0"
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{7, second.ID, 0}, conflict)
	turn, b := iaRefreshTurn(t, w, r, q)
	out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
		t.Fatalf("occupied native tuple was overwritten: %v %v", out, err)
	}
	turn.transaction.rollback(err)
	if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_subtitles`) != 0 {
		t.Fatal("failed tuple move left a captured before-image")
	}
	if got, found := iaRefreshSubtitleNow(t, w, second.ID); !found || !reflect.DeepEqual(got, second) {
		t.Fatal("failed tuple move changed the other source", got)
	}
}
