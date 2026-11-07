package read

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

// Frozen expected groupOrder tuples are independent of legacy packages. Native
// test binaries must never import the legacy implementation as a parity oracle.
func TestRBGroupTVNative(t *testing.T) {
	source := rbGroupSource(t)
	source.exec(t, `UPDATE catalog_entities SET season_id=2012,numbering='date',episode_number=1 WHERE id=5001`)
	source.exec(t, `UPDATE catalog_entities SET season_id=NULL,numbering='absolute',episode_number=100 WHERE id=5003`)
	source.exec(t, `UPDATE catalog_entities SET newest_first=1,absolute_numbering=1,hide_seasons=1,show_settings_revision=7 WHERE id=2001`)
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
			var want []int64
			if group == RBTVSpecials {
				want = []int64{5001}
			} else if group == RBTVUnassignedAbsolute {
				want = []int64{5003}
			} else {
				for n := int64(0); n < 80; n++ {
					if n%9 != 0 && n != 1 && n != 3 {
						want = append(want, 5000+n)
					}
				}
				want = append(want, 5003, 5001)
			}
			if desc {
				slices.Reverse(want)
			}
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
	// An unversioned source-settings change still invalidates the private draft.
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE catalog_entities SET newest_first=0 WHERE id=2001`) }}
	p, _, e = f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(p, RBTVResult{}) {
		t.Fatal("settings witness failed", e)
	}
}
