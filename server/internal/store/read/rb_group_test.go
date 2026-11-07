package read

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"
)

func rbGroupFixture(t *testing.T, live bool) *browseFixture {
	t.Helper()
	defs := append(browseDefinitions(), RBGroupDefinitions()...)
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	if live {
		gc = readfixture.LiveConfig(time.Now)
	}
	return composeReadFixture(t, rbGroupSource(t), defs, gc, !live, rbGroupFixtureHash())
}
func rbGroupRequest() governor.Request {
	r := readfixture.ReadRequest()
	r.Budget.RawRows = 256
	r.Budget.VMOps = 100000
	return r
}
func TestRBGroupTraversal(t *testing.T) {
	f := rbGroupFixture(t, false)
	for _, family := range []RBGroupFamily{RBGroupSeasonEpisodes, RBGroupAlbumSongs, RBGroupBookParts, RBGroupCollectionMembers, RBGroupPlaylistEntries, RBGroupShowSeasons, RBGroupShowEpisodes, RBGroupArtistAlbums, RBGroupAuthorBooks} {
		spec, _ := rbGroupSpecFor(family)
		for _, desc := range []bool{false, true} {
			in := RBGroupInput{View: f.input, Family: family, Subject: rbGroupFixtureSubject(family)}
			in.View.Kind = spec.kinds[0]
			in.View.Limit = 7
			in.View.Descending = desc
			if family == RBGroupCollectionMembers || family == RBGroupPlaylistEntries {
				in.View.Kind = 7
			}
			var got []int64
			for windows := 0; ; windows++ {
				if windows > 100 {
					t.Fatal("unending cursor")
				}
				page, res, e := f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
				if e != nil {
					t.Fatalf("%s desc%v: %v", spec.name, desc, e)
				}
				if page.RawRows > MaxRaw || page.RawPositions > page.RawRows || res.LookupStatements > 9 || res.PublicationStatements != 6 || page.SourceOrdered != spec.ordered {
					t.Fatalf("bounds %s %+v %+v", spec.name, page, res)
				}
				for _, item := range page.Items {
					got = append(got, item.ID)
				}
				if page.Exhausted {
					if page.Next != nil {
						t.Fatal("exhausted continuation")
					}
					break
				}
				if page.Next == nil {
					t.Fatal("incomplete tail omitted")
				}
				in.After = page.Next
			}
			want := rbGroupExpected(family)
			if desc {
				slices.Reverse(want)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s desc%v got%v want%v", spec.name, desc, got, want)
			}
		}
	}
}
func rbGroupExpected(f RBGroupFamily) []int64 {
	switch f {
	case RBGroupShowSeasons:
		return []int64{2002}
	case RBGroupArtistAlbums:
		return []int64{2004}
	case RBGroupAuthorBooks:
		return []int64{2006}
	}
	base := int64(3000)
	if f == RBGroupBookParts {
		base = 4000
	}
	if f == RBGroupSeasonEpisodes || f == RBGroupShowEpisodes {
		base = 5000
	}
	var ns []int64
	for n := int64(0); n < 80; n++ {
		if n%9 != 0 {
			ns = append(ns, n)
		}
	}
	if f == RBGroupAlbumSongs || f == RBGroupBookParts {
		sort.Slice(ns, func(i, j int) bool {
			a, b := ns[i]%3, ns[j]%3
			if ns[i]%5 == 0 {
				a = 0
			}
			if ns[j]%5 == 0 {
				b = 0
			}
			if a != b {
				return a < b
			}
			return ns[i] < ns[j]
		})
	}
	out := make([]int64, len(ns))
	for i, n := range ns {
		out[i] = base + n
	}
	return out
}
func TestRBGroupRawBudgetAndCursorBinding(t *testing.T) {
	f := rbGroupFixture(t, false)
	in := RBGroupInput{View: f.input, Family: RBGroupAlbumSongs, Subject: 2004}
	in.View.Kind = 7
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE album_id=2004`)
	p, _, e := f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if e != nil || len(p.Items) != 0 || p.Exhausted || p.RawPositions != MaxRaw || p.Next == nil {
		t.Fatal("hidden tail", p, e)
	}
	in.After = p.Next
	in.Subject = 2003
	p, _, e = f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(p, RBGroupPage{}) {
		t.Fatal("subject cursor", e)
	}
	in.Subject = 2004
	f.exec(t, `UPDATE libraries SET data_order_revision=data_order_revision+1 WHERE id='movies'`)
	p, _, e = f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(p, RBGroupPage{}) {
		t.Fatal("order cursor", e)
	}
}
func TestRBGroupPublicationAndPolicy(t *testing.T) {
	for _, change := range []string{`UPDATE authorization_session_families SET revoked=1`, `UPDATE catalog_entities SET retired=1 WHERE id=2004`, `UPDATE catalog_entities SET source_revision=2 WHERE id=2004`, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`} {
		t.Run(change, func(t *testing.T) {
			f := rbGroupFixture(t, false)
			f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, change) }}
			in := RBGroupInput{View: f.input, Family: RBGroupAlbumSongs, Subject: 2004}
			in.View.Kind = 7
			p, _, e := f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
			if e == nil || !reflect.DeepEqual(p, RBGroupPage{}) {
				t.Fatal("publication leak", p, e)
			}
		})
	}
	f := rbGroupFixture(t, false)
	in := RBGroupInput{View: f.input, Family: RBGroupPlaylistEntries, Subject: 1}
	in.View.Kind = 7
	f.exec(t, `UPDATE catalog_playlists SET owner_profile='q' WHERE id=1`)
	p, _, e := f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, RBGroupPage{}) {
		t.Fatal("playlist authority", e)
	}
}
func TestRBGroupPublicLIVE(t *testing.T) {
	f := rbGroupFixture(t, true)
	in := RBGroupInput{View: f.input, Family: RBGroupAlbumSongs, Subject: 2004}
	in.View.Kind = 7
	q, request, output := liveRequests("rb-group")
	p, _, e := f.reader.RBGroupWindow(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(p, RBGroupPage{}) {
		t.Fatal("LIVE bypass", e)
	}
	_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		p, res, e := f.reader.RBGroupWindow(ctx, request, time.Second, out, in)
		if e != nil {
			return e
		}
		if len(p.Items) != 40 || res.AdmissionElapsed <= 0 {
			t.Fatal("values/admission")
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	assertLiveUsage(t, f, 0, 0)
}
