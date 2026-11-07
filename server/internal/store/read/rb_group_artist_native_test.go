package read

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRBGroupArtistNative(t *testing.T) {
	defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBArtistDefinitions()...)
	f := composeBrowseFixture(t, rbGroupSource(t), defs)
	in := RBArtistInput{View: f.input, Artist: 2010, Feed: RBArtistCredits}
	in.View.Kind = 7
	in.View.Limit = 7
	count := 0
	for n := 0; ; n++ {
		if n > 30 {
			t.Fatal("cursor stalled")
		}
		p, _, e := f.reader.rbArtistSource(t.Context(), rbGroupRequest(), time.Second, f.output, in)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range p.Items {
			if v.Album != 2004 || v.PrimaryArtist != 2003 || v.AlbumKey != "Album" {
				t.Fatal("appearance relationship hydration", v)
			}
			count++
		}
		if p.Exhausted {
			break
		}
		in.After = p.Next
	}
	if count != 71 {
		t.Fatal("credited source coverage", count)
	}
	in.After = nil
	in.Feed = RBArtistPrimary
	in.View.Kind = 6
	p, _, e := f.reader.rbArtistSource(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if e != nil || len(p.Items) != 0 || !p.Exhausted {
		t.Fatal("appearance became primary", p, e)
	}
	in.Feed = RBArtistCredits
	in.View.Kind = 7
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE libraries SET data_order_revision=data_order_revision+1 WHERE id='movies'`) }}
	p, _, e = f.reader.rbArtistSource(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(p, RBArtistCandidates{}) {
		t.Fatal("torn relationship published", e)
	}
}
