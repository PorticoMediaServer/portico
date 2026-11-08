package read

import (
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

//go:embed testdata/rb-group-tv-contract.json
var rbTVFrozenContract []byte

// Frozen expected groupOrder tuples are independent of legacy packages. Native
// test binaries must never import the legacy implementation as a parity oracle.
func TestRBGroupTVNative(t *testing.T) { synctest.Test(t, testRBGroupTVNative) }
func testRBGroupTVNative(t *testing.T) {
	var frozen map[string][]int64
	if e := json.Unmarshal(rbTVFrozenContract, &frozen); e != nil {
		t.Fatal(e)
	}
	source := rbGroupSource(t)
	source.exec(t, `UPDATE catalog_entities SET season_id=2012,numbering='date',episode_number=1 WHERE id=5001`)
	source.exec(t, `UPDATE catalog_entities SET season_id=NULL,numbering='absolute',episode_number=100 WHERE id=5003`)
	source.exec(t, `UPDATE catalog_entities SET newest_first=1,absolute_numbering=1,hide_seasons=1,show_settings_present=1,show_settings_revision=7 WHERE id=2001`)
	defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBTVDefinitions()...)
	f := composeBrowseFixture(t, source, defs)
	in := RBTVInput{View: f.input, Subject: 2001, Scope: RBTVShow}
	in.View.Kind = 4
	for _, desc := range []bool{false, true} {
		for _, group := range []RBTVGroup{RBTVAll, RBTVSpecials, RBTVUnassignedAbsolute} {
			in.View.Descending = desc
			in.Group = group
			p, _, e := f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
			if e != nil {
				t.Fatal(e)
			}
			key := "show_all"
			if group == RBTVSpecials {
				key = "show_specials"
			}
			if group == RBTVUnassignedAbsolute {
				key = "show_unassigned_absolute"
			}
			if desc {
				key += "_desc"
			} else {
				key += "_asc"
			}
			want := frozen[key]

			var got []int64
			for _, ep := range p.Items {
				got = append(got, ep.ID)
				if ep.ID == 5001 && (ep.SeasonNumber != 0 || ep.Numbering != "date") {
					t.Fatal("source mode/season lost")
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal(group, desc, got, want)
			}
		}
	}
	in.Scope = RBTVSeason
	in.Subject = 2002
	in.Group = RBTVAll
	in.View.Descending = false
	p, _, e := f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if e != nil || len(p.Items) != 69 {
		t.Fatal("season membership", len(p.Items), e)
	}
	f.exec(t, "ANALYZE")
	captureBrowse(t, f, RBTVDefinitions(), plan.Analyzed)
	// An unversioned source-settings change still invalidates the private draft.
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE catalog_entities SET newest_first=0 WHERE id=2001`) }}
	p, _, e = f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(p, RBTVResult{}) {
		t.Fatal("settings witness failed", e)
	}
}
