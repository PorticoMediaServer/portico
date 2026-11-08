package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed ia_tv_test.go
var iaTVJournal string

func iaTVHash(t *testing.T) string {
	h := sha256.Sum256([]byte(readfixture.ForegroundHash(t) + iaTVJournal))
	return hex.EncodeToString(h[:])
}

func iaTVEntityRow(t *testing.T, f *browseFixture, id, kind, library int64, parent, show, season, seasonNumber, episodeNumber, age any, retired int64, title string) {
	t.Helper()
	public := make([]byte, 16)
	binary.LittleEndian.PutUint64(public, uint64(id))
	var numbering any
	if kind == 4 {
		numbering = "seasonal"
	}
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, id, public, library, kind)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,parent_id,show_id,season_id,season_number,episode_number,numbering,tv_incarnation,tv_source_revision,tv_hierarchy_revision,tv_selection_revision,tv_descriptive_revision,admission_own_age,retired) VALUES(?,?,?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?,?,1,1,1,1,?,?)`,
		id, public, library, kind, title, "tv", "t", "tv", "t", public, public,
		parent, show, season, seasonNumber, episodeNumber, numbering, public, age, retired)
}

// newIATVSource extends the merged foreground fixture with a small accepted TV
// hierarchy: one show, one season, seventy season episodes, six direct episodes
// and one asset linked to eight episodes. No existing fixture journal is edited.
func newIATVSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0)`)
	f.exec(t, "BEGIN")
	iaTVEntityRow(t, f, 2000, 2, 1, nil, nil, nil, nil, nil, nil, 0, "é Show")
	iaTVEntityRow(t, f, 2100, 3, 1, int64(2000), int64(2000), nil, int64(1), nil, nil, 0, "é Season")
	for id := int64(2201); id <= 2270; id++ {
		iaTVEntityRow(t, f, id, 4, 1, int64(2100), int64(2000), int64(2100), nil, id-2200, int64(12), 0, fmt.Sprintf("épisode %d 🦊", id))
	}
	for id := int64(2301); id <= 2306; id++ {
		iaTVEntityRow(t, f, id, 4, 1, int64(2000), int64(2000), nil, nil, id-2300, int64(12), 0, "direct episode")
	}
	// A second show whose whole direct cohort is retired proves the raw cursor
	// still advances through a window with no visible child.
	iaTVEntityRow(t, f, 2500, 2, 1, nil, nil, nil, nil, nil, nil, 0, "retired show")
	for id := int64(2501); id <= 2564; id++ {
		iaTVEntityRow(t, f, id, 4, 1, int64(2500), int64(2500), nil, nil, id-2500, int64(12), 1, "retired episode")
	}
	// Duplicate parents are FK-valid rows whose parent/season pointers do not
	// agree; they must be refused as an inconsistent accepted hierarchy. They
	// live under a separate active show so they do not inflate show 2000's
	// chronology.
	iaTVEntityRow(t, f, 2700, 2, 1, nil, nil, nil, nil, nil, nil, 0, "other show")
	iaTVEntityRow(t, f, 2701, 3, 1, int64(2700), int64(2700), nil, int64(1), nil, nil, 0, "other season")
	iaTVEntityRow(t, f, 2400, 4, 1, int64(2000), int64(2700), int64(2701), nil, int64(1), int64(12), 0, "duplicate parent")
	iaTVEntityRow(t, f, 2401, 4, 1, int64(2301), int64(2700), int64(2701), nil, int64(2), int64(12), 0, "duplicate parent episode")
	iaTVEntityRow(t, f, 3000, 2, 2, nil, nil, nil, nil, nil, nil, 0, "foreign show")
	iaTVEntityRow(t, f, 3100, 3, 2, int64(3000), int64(3000), nil, int64(1), nil, nil, 0, "foreign season")
	iaTVEntityRow(t, f, 3200, 4, 2, int64(3100), int64(3000), int64(3100), nil, int64(1), int64(12), 0, "foreign episode")
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(2000,'ia-tv-asset','/media/tv.mkv',1000,1,'matroska','h264','aac',1920,1080,100.0,1)`)
	for id := int64(2201); id <= 2208; id++ {
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,part_index,start_seconds,end_seconds,edition) VALUES(?,2000,1,?,0,10,'édition 🦊')`, id, id-2200)
	}
	f.exec(t, "COMMIT")
	return f
}

func newIATVFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := newIATVSource(t)
	f = composeReadFixture(t, f, append(browseDefinitions(), IATVHarnessDefinitions()...), readfixture.LiveConfig(time.Now), true, iaTVHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func iaTVInput(f *browseFixture, item, kind int64) IATVInput {
	in := IATVInput{View: f.input, Item: item}
	in.View.Kind = kind
	return in
}

func TestIATVShowSeasonsAndDirectEpisodes(t *testing.T) {
	f, r := newIATVFixture(t)
	page, result, e := r.iaTVShow(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, 2000, 2))
	if e != nil {
		t.Fatal(e)
	}
	if page.Hierarchy.Item.ID != 2000 || page.Hierarchy.Item.Title != "é Show" {
		t.Fatalf("show hierarchy=%+v", page.Hierarchy.Item)
	}
	if len(page.Seasons) != 1 || page.Seasons[0].ID != 2100 || page.Seasons[0].SeasonNumber == nil || *page.Seasons[0].SeasonNumber != 1 {
		t.Fatalf("show seasons=%+v", page.Seasons)
	}
	got := []int64{}
	for _, child := range page.Children {
		got = append(got, child.ID)
	}
	if !reflect.DeepEqual(got, []int64{2301, 2302, 2303, 2304, 2305, 2306}) || !page.Exhausted || page.Next != nil {
		t.Fatalf("direct episodes=%v exhausted=%v next=%+v", got, page.Exhausted, page.Next)
	}
	// Five common fence statements plus the show point pair; then the two
	// ordered windows, then the same fence and source point at publication.
	if result.LookupStatements != 9 || result.PublicationStatements != 7 {
		t.Fatalf("statement accounting=%+v", result)
	}
}

func TestIATVShowPagingCrossesStreams(t *testing.T) {
	f, r := newIATVFixture(t)
	in := iaTVInput(f, 2000, 2)
	in.View.Limit = 1
	var seasons, children []int64
	for n := 0; n < 12; n++ {
		page, _, e := r.iaTVShow(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range page.Seasons {
			seasons = append(seasons, v.ID)
		}
		for _, v := range page.Children {
			children = append(children, v.ID)
		}
		if page.Exhausted {
			if page.Next != nil {
				t.Fatal("terminal cursor retained")
			}
			break
		}
		if page.Next == nil {
			t.Fatal("unfinished page has no continuation")
		}
		in.After = page.Next
	}
	if !reflect.DeepEqual(seasons, []int64{2100}) || !reflect.DeepEqual(children, []int64{2301, 2302, 2303, 2304, 2305, 2306}) {
		t.Fatalf("paged show=%v/%v", seasons, children)
	}
}

func TestIATVAllRejectedRawCursor(t *testing.T) {
	f, r := newIATVFixture(t)
	in := iaTVInput(f, 2500, 2)
	page, _, e := r.iaTVShow(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(page.Seasons) != 0 || len(page.Children) != 0 || page.Exhausted || page.Next == nil || page.Next.phase != 1 || page.Next.id != 2564 || page.RawRows != 64 || page.RawPositions != 64 {
		t.Fatalf("all-rejected window=%+v err=%v", page, e)
	}
	in.After = page.Next
	page, _, e = r.iaTVShow(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(page.Children) != 0 || !page.Exhausted || page.Next != nil {
		t.Fatalf("all-rejected tail=%+v err=%v", page, e)
	}
}

func TestIATVSeasonOrderAndRawCursor(t *testing.T) {
	f, r := newIATVFixture(t)
	in := iaTVInput(f, 2100, 3)
	page, result, e := r.iaTVSeason(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || page.Hierarchy.Item.ID != 2100 || page.Hierarchy.Show == nil || page.Hierarchy.Show.ID != 2000 {
		t.Fatalf("season hierarchy=%+v err=%v", page.Hierarchy, e)
	}
	if len(page.Children) != 40 || page.Children[0].ID != 2201 || page.Children[39].ID != 2240 || page.Exhausted || page.Next == nil || page.RawRows != 64 || page.RawPositions != 40 {
		t.Fatalf("season first page=%d first%d last%d exhausted%v raw%d/%d", len(page.Children), page.Children[0].ID, page.Children[len(page.Children)-1].ID, page.Exhausted, page.RawRows, page.RawPositions)
	}
	if result.LookupStatements != 10 || result.PublicationStatements != 9 {
		t.Fatalf("season statements=%+v", result)
	}
	in.After = page.Next
	page, _, e = r.iaTVSeason(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(page.Children) != 30 || page.Children[0].ID != 2241 || page.Children[29].ID != 2270 || !page.Exhausted || page.Next != nil {
		t.Fatalf("season tail=%d err=%v", len(page.Children), e)
	}

	// Every row in the first raw window is retired: visibility is empty but the
	// cursor still advances past all 64 raw positions.
	f, r = newIATVFixture(t)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id BETWEEN 2201 AND 2264`)
	in = iaTVInput(f, 2100, 3)
	page, _, e = r.iaTVSeason(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(page.Children) != 0 || page.Exhausted || page.Next == nil || page.Next.id != 2264 || page.RawPositions != 64 {
		t.Fatalf("all-rejected season=%+v err=%v", page, e)
	}
	in.After = page.Next
	page, _, e = r.iaTVSeason(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(page.Children) != 6 || page.Children[0].ID != 2265 || !page.Exhausted || page.Next != nil {
		t.Fatalf("all-rejected season tail=%+v err=%v", page, e)
	}
}

func TestIATVHierarchyAndParents(t *testing.T) {
	f, r := newIATVFixture(t)
	for _, tc := range []struct {
		item     int64
		wantShow bool
		wantSea  bool
	}{{2000, false, false}, {2100, true, false}, {2201, true, true}, {2301, true, false}} {
		kind := int64(2)
		if tc.item == 2100 {
			kind = 3
		} else if tc.wantShow {
			kind = 4
		}
		in := iaTVInput(f, tc.item, kind)
		var page IATVResult
		var e error
		switch kind {
		case 2:
			page, _, e = r.iaTVShow(t.Context(), foregroundRequest(), time.Second, f.output, in)
		case 3:
			page, _, e = r.iaTVSeason(t.Context(), foregroundRequest(), time.Second, f.output, in)
		default:
			page, _, e = r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
		}
		if e != nil || page.Hierarchy.Item.ID != tc.item || (page.Hierarchy.Show != nil) != tc.wantShow || (page.Hierarchy.Season != nil) != tc.wantSea {
			t.Fatalf("hierarchy %d=%+v err=%v", tc.item, page.Hierarchy, e)
		}
	}
	for _, item := range []int64{2400, 2401} {
		in := iaTVInput(f, item, 4)
		page, _, e := r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(page, IATVResult{}) {
			t.Fatalf("duplicate/cross parent %d=%+v err=%v", item, page, e)
		}
	}
	in := iaTVInput(f, 3200, 4)
	page, _, e := r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatalf("foreign episode=%+v err=%v", page, e)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2202`)
	in = iaTVInput(f, 2202, 4)
	page, _, e = r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatalf("retired subject=%+v err=%v", page, e)
	}
}

func TestIATVEpisodeLinksAndOctets(t *testing.T) {
	f, r := newIATVFixture(t)
	in := iaTVInput(f, 2201, 4)
	page, result, e := r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(page.Links) != 1 || page.Links[0].Asset != 2000 || page.Links[0].Part != 1 || page.Links[0].Edition != "édition 🦊" || page.Links[0].End == nil || *page.Links[0].End != 10 || !page.Exhausted {
		t.Fatalf("episode links=%+v err=%v", page.Links, e)
	}
	if result.LookupStatements != 14 || result.PublicationStatements != 11 {
		t.Fatalf("episode statements=%+v", result)
	}
	long := strings.Repeat("é", 4000)
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=2201`, long)
	page, _, e = r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || page.Hierarchy.Item.Title != long {
		t.Fatal("actual byte point", e)
	}
	f.exec(t, `UPDATE catalog_entities SET tv_incarnation=NULL WHERE id=2201`)
	page, _, e = r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatalf("missing incarnation=%+v err=%v", page, e)
	}
	f.exec(t, `UPDATE catalog_entities SET title='épisode',parent_id=2700 WHERE id=2201`)
	page, _, e = r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatalf("mismatched parent=%+v err=%v", page, e)
	}
}

func TestIATVAssetMembershipBoundary(t *testing.T) {
	f, r := newIATVFixture(t)
	in := IATVInput{View: f.input, Asset: 2000}
	in.View.Kind = 4
	page, result, e := r.iaTVAssetMembership(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || !page.AssetAvailable || !page.Exhausted || len(page.Members) != 8 || page.RawRows != 8 {
		t.Fatalf("complete cohort=%+v err=%v", page, e)
	}
	for i, m := range page.Members {
		if m.Hierarchy.Item.ID != int64(2201+i) || m.Hierarchy.Season == nil || m.Hierarchy.Season.ID != 2100 || m.Link.Part != int64(i+1) || m.Link.Available != true || m.Link.Edition != "édition 🦊" {
			t.Fatalf("member %d=%+v", i, m)
		}
	}
	if result.LookupStatements != 72 || result.PublicationStatements != 7 {
		t.Fatalf("membership statements=%+v", result)
	}
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=2000`)
	f.exec(t, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=2208`)
	page, _, e = r.iaTVAssetMembership(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || page.AssetAvailable || len(page.Members) != 8 || page.Members[7].Link.Available {
		t.Fatal("retained unavailable membership", e)
	}
	f.exec(t, `UPDATE catalog_assets SET available=1 WHERE id=2000`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(2209,2000,1)`)
	page, _, e = r.iaTVAssetMembership(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatalf("ninth raw member=%+v err=%v", page, e)
	}
}

func TestIATVRetiredMemberAndForeignAsset(t *testing.T) {
	f, r := newIATVFixture(t)
	in := IATVInput{View: f.input, Asset: 2000}
	in.View.Kind = 4
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2208`)
	page, _, e := r.iaTVAssetMembership(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatalf("retired member=%+v err=%v", page, e)
	}
	in.View.Library = 2
	page, _, e = r.iaTVAssetMembership(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatalf("foreign asset=%+v err=%v", page, e)
	}
}

func TestIATVInputAndCustody(t *testing.T) {
	f, r := newIATVFixture(t)
	for _, tc := range []struct {
		name string
		in   IATVInput
		call func(context.Context, BrowseOutput, IATVInput) (IATVResult, BrowseReadResult, error)
	}{
		{"show", iaTVInput(f, 0, 2), func(ctx context.Context, out BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
			return r.IATVShow(ctx, foregroundRequest(), time.Second, out, in)
		}},
		{"season", iaTVInput(f, 2100, 4), func(ctx context.Context, out BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
			return r.IATVSeason(ctx, foregroundRequest(), time.Second, out, in)
		}},
		{"episode", iaTVInput(f, 2201, 2), func(ctx context.Context, out BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
			return r.IATVEpisode(ctx, foregroundRequest(), time.Second, out, in)
		}},
		{"asset", IATVInput{View: f.input}, func(ctx context.Context, out BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
			return r.IATVAssetMembership(ctx, foregroundRequest(), time.Second, out, in)
		}},
	} {
		page, _, e := tc.call(t.Context(), BrowseOutput{}, tc.in)
		if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IATVResult{}) {
			t.Fatalf("input %s=%+v err=%v", tc.name, page, e)
		}
	}
	page, _, e := r.iaTVShow(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, 9999, 2))
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IATVResult{}) {
		t.Fatal("missing show", e)
	}
}

// iaTVBoundary mutates only after SQLite has closed the previous physical
// snapshot, then leaves the real pool to open the next one.
type iaTVBoundary struct {
	*governor.Snapshots
	phase  int
	mutate func(int)
}

func (b *iaTVBoundary) ReadForecasted(ctx context.Context, r governor.Request, budget time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, e := b.Snapshots.ReadForecasted(ctx, r, budget, f, fn)
	b.phase++
	b.mutate(b.phase)
	return result, e
}

func TestIATVPublicationDriftCancelAndRetry(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		f, r := newIATVFixture(t)
		r.snapshots = &iaTVBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
			if phase == 1 {
				f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=2 WHERE id=2201`)
			}
		}}
		page, result, e := r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, 2201, 4))
		if e != nil || result.Attempts != 2 || page.Hierarchy.Item.HierarchyRevision != 2 {
			t.Fatalf("retry revision=%d attempts=%d err=%v", page.Hierarchy.Item.HierarchyRevision, result.Attempts, e)
		}
	})
	t.Run("drift", func(t *testing.T) {
		f, r := newIATVFixture(t)
		r.snapshots = &iaTVBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
			if phase%2 == 1 {
				f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=tv_hierarchy_revision+1 WHERE id=2201`)
			}
		}}
		page, _, e := r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, 2201, 4))
		if !errors.Is(e, ErrStale) || !errors.Is(e, governor.ErrBusy) || !reflect.DeepEqual(page, IATVResult{}) {
			t.Fatalf("drift=%+v err=%v", page, e)
		}
	})
	t.Run("authority", func(t *testing.T) {
		f, r := newIATVFixture(t)
		r.snapshots = &iaTVBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
			if phase == 1 {
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
			}
		}}
		page, _, e := r.iaTVEpisode(t.Context(), foregroundRequest(), time.Second, f.output, iaTVInput(f, 2201, 4))
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IATVResult{}) {
			t.Fatalf("authority=%+v err=%v", page, e)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		f, r := newIATVFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r.snapshots = &iaTVBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
			if phase == 2 {
				cancel()
			}
		}}
		page, _, e := r.iaTVEpisode(ctx, foregroundRequest(), time.Second, f.output, iaTVInput(f, 2201, 4))
		if !errors.Is(e, context.Canceled) || !reflect.DeepEqual(page, IATVResult{}) {
			t.Fatalf("cancel=%+v err=%v", page, e)
		}
	})
}
