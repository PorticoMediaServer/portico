package read

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newIATVSource(t *testing.T) *browseFixture {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0)`)
	add := func(id, kind, library int64, parent, show, season, number any) {
		b := make([]byte, 16)
		binary.LittleEndian.PutUint64(b, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, id, b, library, kind)
		var sn, en, numbering any
		if kind == 3 {
			sn = number
		}
		if kind == 4 {
			en = number
			numbering = "seasonal"
		}
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,parent_id,show_id,season_id,season_number,episode_number,numbering,tv_incarnation,tv_source_revision,tv_hierarchy_revision,tv_selection_revision,tv_descriptive_revision) VALUES(?,?,?,?,'épisode 🦊','tv','t','tv','t',0,?,?,?,?,?,?,?,?,?,1,1,1,1)`, id, b, library, kind, b, b, parent, show, season, sn, en, numbering, b)
	}
	f.exec(t, "BEGIN")
	add(2000, 2, 1, nil, nil, nil, nil)
	add(2100, 3, 1, int64(2000), int64(2000), nil, int64(1))
	for id := int64(2201); id <= 2270; id++ {
		add(id, 4, 1, int64(2100), int64(2000), int64(2100), id-2200)
	}
	add(3000, 2, 2, nil, nil, nil, nil)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(2000,'ia-tv-asset','/media/tv.mkv',1000,1,'mkv','h264','aac',1920,1080,100,1)`)
	for id := int64(2201); id <= 2208; id++ {
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,part_index,start_seconds,end_seconds,edition) VALUES(?,2000,1,?,0,10,'édition 🦊')`, id, id-2200)
	}
	f.exec(t, "COMMIT")
	return f
}
func newIATVFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	f := composeReadFixture(t, newIATVSource(t), append(browseDefinitions(), IATVDefinitions()...), iaTVTestGovernorConfig(), true, iaTVFixtureHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func iaTVInput(f *browseFixture, item, kind int64) IATVInput {
	v := f.input
	v.Kind = kind
	return IATVInput{View: v, Item: item}
}
func TestIATVHierarchyAndCompleteMembership(t *testing.T) {
	f, r := newIATVFixture(t)
	for _, tc := range []struct{ id, kind int64 }{{2000, 2}, {2100, 3}, {2201, 4}} {
		d, _, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, tc.id, tc.kind), "point")
		if e != nil {
			t.Fatal(e)
		}
		if d.Hierarchy.Item.ID != tc.id || tc.kind > 2 && (d.Hierarchy.Show == nil || d.Hierarchy.Show.ID != 2000) || tc.kind == 4 && (d.Hierarchy.Season == nil || d.Hierarchy.Season.ID != 2100) {
			t.Fatalf("hierarchy %+v", d)
		}
	}
	in := iaTVInput(f, 0, 4)
	in.Asset = 2000
	d, _, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "members")
	if e != nil {
		t.Fatal(e)
	}
	if !d.AssetAvailable || !d.Exhausted || d.RawRows != 8 || len(d.Members) != 8 || d.Members[0].Hierarchy.Item.EpisodeNumber == nil || *d.Members[0].Hierarchy.Item.EpisodeNumber != 1 {
		t.Fatalf("complete cohort %+v", d)
	}
	for i, m := range d.Members {
		if m.Hierarchy.Item.ID != int64(2201+i) || m.Hierarchy.Season.ID != 2100 || m.Part != int64(i+1) || m.Edition != "édition 🦊" || m.End == nil || *m.End != 10 {
			t.Fatalf("member %+v", m)
		}
	}
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=2000`)
	f.exec(t, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=2208 AND asset_id=2000`)
	missing, _, err := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "members")
	if err != nil || missing.AssetAvailable || len(missing.Members) != 8 || missing.Members[7].Available {
		t.Fatal("retained unavailable membership", err)
	}
	f.exec(t, `UPDATE catalog_assets SET available=1 WHERE id=2000`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(2209,2000,1)`)
	d, _, e = r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "members")
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(d, IATVResult{}) {
		t.Fatal("ninth raw member escaped", e)
	}
}
func TestIATVRawCursorAndFence(t *testing.T) {
	f, r := newIATVFixture(t)
	in := iaTVInput(f, 2100, 3)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id BETWEEN 2201 AND 2264`)
	d, receipt, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "children")
	if e != nil || len(d.Children) != 0 || d.Exhausted || d.RawRows != 64 || d.RawPositions != 64 || d.Next == nil || d.Next.last != 2264 || len(receipt.LookupWindows) != 3 || receipt.LookupStatements != 12 {
		t.Fatalf("raw rejected window %+v %v", d, e)
	}
	in.After = d.Next
	d, _, e = r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "children")
	if e != nil || len(d.Children) != 6 || !d.Exhausted || d.Next != nil || d.Children[0].ID != 2265 {
		t.Fatalf("raw tail %+v %v", d, e)
	}
	f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=2 WHERE id=2100`)
	stale, _, err := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "children")
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(stale, IATVResult{}) {
		t.Fatal("parent source cursor drift escaped", err)
	}
	f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=1 WHERE id=2100`)
	f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
	d, _, e = r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "children")
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(d, IATVResult{}) {
		t.Fatal("cursor drift escaped", e)
	}
}
func TestIATVSameLibraryAndAuthority(t *testing.T) {
	for _, mode := range []string{"point", "children", "members"} {
		t.Run(mode, func(t *testing.T) {
			f, r := newIATVFixture(t)
			in := iaTVInput(f, 2000, 2)
			if mode == "members" {
				in = iaTVInput(f, 0, 4)
				in.Asset = 2000
			}
			r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
			d, _, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, mode)
			if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(d, IATVResult{}) {
				t.Fatalf("publication authority %s %+v %v", mode, d, e)
			}
		})
	}
	f, r := newIATVFixture(t)
	for _, in := range []IATVInput{iaTVInput(f, 3000, 2), func() IATVInput { in := iaTVInput(f, 2201, 4); in.View.Library = 2; return in }()} {
		d, _, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "point")
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(d, IATVResult{}) {
			t.Fatal("same-library denial", e)
		}
	}
	in := iaTVInput(f, 0, 4)
	in.Asset = 2000
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2208`)
	d, _, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, "members")
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(d, IATVResult{}) {
		t.Fatal("incomplete authorized membership escaped", e)
	}
}
func TestIATVPublicationDriftAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			f, r := newIATVFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if cancelled {
					if phase == 2 {
						cancel()
					}
				} else {
					f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=tv_hierarchy_revision+1 WHERE id=2201`)
				}
			}}
			d, _, e := r.iaTV(ctx, foregroundRequest(), time.Second, f.output, iaTVInput(f, 2201, 4), "point")
			want := ErrStale
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(e, want) || !reflect.DeepEqual(d, IATVResult{}) {
				t.Fatal("publication drift/cancel", e)
			}
		})
	}
}

func TestIATVExactBytesAndHierarchyControls(t *testing.T) {
	f, r := newIATVFixture(t)
	// A long multibyte source exercises actual octets, not rune count or a
	// columns*max estimate. The native preflight and copied values must agree.
	title := strings.Repeat("é", 4000)
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=2201`, title)
	d, _, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, 2201, 4), "point")
	if e != nil || d.Hierarchy.Item.Title != title {
		t.Fatal("actual byte point", e)
	}
	f.exec(t, `UPDATE catalog_entities SET parent_id=2000 WHERE id=2201`)
	d, _, e = r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, 2201, 4), "point")
	if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(d, IATVResult{}) {
		t.Fatal("incompatible accepted hierarchy", e)
	}
}

// Both mutants alter the real compiled SQL family, then execute the native GV
// recipe with its actual Capture receipt. They are not fake query callbacks.
func TestIATVMaterialMutants(t *testing.T) {
	for _, mutant := range []string{"truncate-membership", "wrong-episode-number"} {
		t.Run(mutant, func(t *testing.T) {
			source := newIATVSource(t)
			defs := IATVDefinitions()
			for i := range defs {
				switch {
				case mutant == "truncate-membership" && defs[i].ID == readStatementID("foreground.ia.tv.members"):
					defs[i].SQL = strings.Replace(defs[i].SQL, "LIMIT 9", "LIMIT 1", 1)
				case mutant == "wrong-episode-number" && defs[i].ID == readStatementID("foreground.ia.tv.entity"):
					defs[i].SQL = strings.Replace(defs[i].SQL, "episode_number,numbering", "show_id,numbering", 1)
				}
			}
			f := composeReadFixture(t, source, append(browseDefinitions(), defs...), iaTVTestGovernorConfig(), true, iaTVFixtureHash(t))
			r, _ := NewForegroundReader(f.snapshots)
			in := iaTVInput(f, 2201, 4)
			mode := "point"
			if mutant == "truncate-membership" {
				in = iaTVInput(f, 0, 4)
				in.Asset = 2000
				mode = "members"
			}
			d, _, e := r.iaTV(t.Context(), foregroundRequest(), time.Second, f.output, in, mode)
			if e != nil {
				t.Fatal("mutant did not reach semantic oracle", e)
			}
			if mutant == "truncate-membership" {
				if len(d.Members) != 1 {
					t.Fatal("truncation mutant not activated")
				}
				if len(d.Members) == 8 {
					t.Fatal("complete-eight-member oracle survived mutant")
				}
			} else {
				if d.Hierarchy.Item.EpisodeNumber == nil || *d.Hierarchy.Item.EpisodeNumber != 2000 {
					t.Fatal("numbering mutant not activated")
				}
				if *d.Hierarchy.Item.EpisodeNumber == 1 {
					t.Fatal("accepted episode-number oracle survived mutant")
				}
			}
			t.Log("killed native production SQL mutant with independent accepted fixture oracle:", mutant)
		})
	}
}

func TestIATVPublicForegroundCustody(t *testing.T) {
	f := composeReadFixture(t, newIATVSource(t), append(browseDefinitions(), IATVDefinitions()...), readfixture.LiveConfig(time.Now), false, iaTVFixtureHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"point", "children", "members"} {
		q, request, output := liveRequests("ia-tv-" + mode)
		in := iaTVInput(f, 2201, 4)
		if mode == "children" {
			in = iaTVInput(f, 2100, 3)
		}
		if mode == "members" {
			in = iaTVInput(f, 0, 4)
			in.Asset = 2000
		}
		call := func(ctx context.Context, out BrowseOutput) (IATVResult, BrowseReadResult, error) {
			switch mode {
			case "point":
				return r.IATVPoint(ctx, request, time.Second, out, in)
			case "children":
				return r.IATVChildren(ctx, request, time.Second, out, in)
			default:
				return r.IATVEpisodeMembership(ctx, request, time.Second, out, in)
			}
		}
		d, receipt, e := call(t.Context(), BrowseOutput{})
		if !errors.Is(e, ErrInput) || !reflect.DeepEqual(d, IATVResult{}) || receipt.LookupStatements != 0 {
			t.Fatal("public read bypassed LIVE", e)
		}
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			d, receipt, e := call(ctx, out)
			if e != nil {
				return e
			}
			if receipt.PublicationStatements != 7 || mode == "members" && len(d.Members) != 8 || mode == "point" && d.Hierarchy.Item.ID != 2201 || mode == "children" && len(d.Children) != 40 {
				t.Fatal("public TV recipe result", mode)
			}
			assertLiveUsage(t, f, 1, 0)
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		assertLiveUsage(t, f, 0, 0)
	}
}

func iaTVTestGovernorConfig() governor.Config {
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	return gc
}
