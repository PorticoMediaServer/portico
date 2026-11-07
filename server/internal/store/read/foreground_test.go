package read

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func newForegroundSource(t *testing.T) *browseFixture {
	t.Helper()
	return attachReadSource(readfixture.Foreground(t))
}
func newForegroundFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeBrowseFixture(t, newForegroundSource(t), append(browseDefinitions(), foregroundDefinitions()...))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func foregroundRequest() governor.Request { return readfixture.ReadRequest() }
func TestForegroundDetailAndPlayback(t *testing.T) {
	f, r := newForegroundFixture(t)
	in := TitleInput{View: f.input, Item: 1, Asset: 1}
	d, result, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil {
		t.Fatal(e)
	}
	if d.Item.ID != 1 || d.State.ResumePosition != 500 || len(d.Cast) != 100 || len(d.Crew) != 50 || len(d.Children) != 40 || d.ChildrenExhausted || d.CreditsExhausted || len(d.Images) != 1 || d.Images[0].Provider != "owner" || len(d.Versions) != 1 || d.File == nil || len(d.File.Streams) != 2 || len(d.File.Chapters) != 2 || len(result.LookupWindows) != 3 || result.LookupStatements != 18 || result.PublicationStatements != 8 {
		t.Fatalf("detail counts children%d cast%d crew%d windows%d SQL%d+%d", len(d.Children), len(d.Cast), len(d.Crew), len(result.LookupWindows), result.LookupStatements, result.PublicationStatements)
	}
	if d.Next == nil {
		t.Fatal("missing child continuation")
	}
	in.After = d.Next
	next, _, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(next.Children) == 0 || next.Children[0].ID <= d.Children[39].ID {
		t.Fatal("child continuation", e)
	}
	in.After = nil
	file, result, e := r.playback(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || file.Path != "/media/title.mkv" || file.Size != 1000 || len(file.Streams) != 2 || result.LookupStatements != 11 {
		t.Fatalf("playback=%+v result=%+v err=%v", file, result, e)
	}
	f.exec(t, `UPDATE catalog_assets SET modified_ns=2 WHERE id=1`)
	file, _, e = r.playback(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(file, PlaybackFile{}) {
		t.Fatal("stale file facts escaped", e)
	}
}
func TestForegroundContinueOwnStateAndRawTraversal(t *testing.T) {
	f, r := newForegroundFixture(t)
	in := ContinueInput{View: f.input}
	in.View.Limit = 7
	var ids []int64
	var last *ContinueCursor
	for n := 0; n < 100; n++ {
		page, result, e := r.continueWatching(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil {
			t.Fatal(e)
		}
		if page.RawRows > MaxRaw || page.RawPositions > page.RawRows || result.LookupStatements > 7 {
			t.Fatal("unbounded continue")
		}
		for _, item := range page.Items {
			if item.State.ResumePosition != 500 {
				t.Fatal("another profile state")
			}
			ids = append(ids, item.ID)
		}
		if page.Exhausted {
			break
		}
		if page.Next == nil {
			t.Fatal("missing raw continuation")
		}
		last = page.Next
		in.After = page.Next
	}
	// Independent known fixture IDs, ordered by their activity timestamp/public bytes.
	var want []int64
	for id := int64(1); id <= 200; id++ {
		if id%9 != 0 {
			want = append(want, id)
		}
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("continue got%v want%v", ids, want)
	}
	f.exec(t, `INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('p','movies',2)`)
	in.After = last
	page, _, e := r.continueWatching(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(page, ContinuePage{}) {
		t.Fatal("stale own state cursor", e)
	}
	in.After = nil
	f.exec(t, `UPDATE catalog_entities SET retired=1`)
	page, _, e = r.continueWatching(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(page.Items) != 0 || page.Exhausted || page.RawPositions != MaxRaw || page.Next == nil {
		t.Fatalf("hidden raw tail: %+v %v", page, e)
	}
}
func TestForegroundPublicationDeniesAllRecipes(t *testing.T) {
	for _, recipe := range []string{"detail", "playback", "continue"} {
		t.Run(recipe, func(t *testing.T) {
			f, r := newForegroundFixture(t)
			r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
			var value any
			var e error
			switch recipe {
			case "detail":
				var d TitleDetail
				d, _, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
				value = d
				if !reflect.DeepEqual(d, TitleDetail{}) {
					t.Fatal("detail leak")
				}
			case "playback":
				var d PlaybackFile
				d, _, e = r.playback(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
				value = d
				if !reflect.DeepEqual(d, PlaybackFile{}) {
					t.Fatal("playback leak")
				}
			case "continue":
				var d ContinuePage
				d, _, e = r.continueWatching(t.Context(), foregroundRequest(), time.Second, f.output, ContinueInput{View: f.input})
				value = d
				if !reflect.DeepEqual(d, ContinuePage{}) {
					t.Fatal("continue leak")
				}
			}
			if !errors.Is(e, ErrDenied) {
				t.Fatalf("%+v %v", value, e)
			}
		})
	}
}
func TestForegroundCancellation(t *testing.T) {
	f, r := newForegroundFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: cancel}
	d, _, e := r.detail(ctx, foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
	if !errors.Is(e, context.Canceled) || !reflect.DeepEqual(d, TitleDetail{}) {
		t.Fatal("cancellation leaked output", e)
	}
}

// Run mutations after each callback while its read view still exists, then let
// the real pool close/return the snapshot before the next window.
type foregroundBoundary struct {
	*governor.Snapshots
	phase  int
	mutate func(int)
}

func (s *foregroundBoundary) Read(ctx context.Context, r governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.phase++
	phase := s.phase
	return s.Snapshots.Read(ctx, r, budget, func(scope *governor.ReadScope) error { e := read(scope); s.mutate(phase); return e })
}
func TestForegroundSourceDriftRetriesOnce(t *testing.T) {
	for _, boundary := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			f, r := newForegroundFixture(t)
			r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == boundary {
					f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=1`)
				}
			}}
			d, result, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
			if e != nil || d.Item.Revision != 2 || result.Attempts != 2 {
				t.Fatalf("retry revision%d attempts%d err%v", d.Item.Revision, result.Attempts, e)
			}
		})
	}
	f, r := newForegroundFixture(t)
	r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(int) { f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=1`) }}
	d, result, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
	if !errors.Is(e, governor.ErrBusy) || !errors.Is(e, ErrStale) || result.Attempts != 2 || result.Refusal == nil || result.Refusal.Code != governor.Conflict || !reflect.DeepEqual(d, TitleDetail{}) {
		t.Fatalf("retry bound attempts%d err%v", result.Attempts, e)
	}
	for _, w := range append(result.LookupWindows, result.PublicationWindows...) {
		if w.Deadline.After(result.LookupWindows[0].Deadline) {
			t.Fatal("retry renewed original deadline")
		}
	}
}
func TestForegroundPublicationAfterEveryBusinessWindow(t *testing.T) {
	for _, boundary := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			f, r := newForegroundFixture(t)
			r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == boundary {
					f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
				}
			}}
			d, _, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
			if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(d, TitleDetail{}) {
				t.Fatal("late revocation", e)
			}
		})
	}
}
func TestForegroundLegalCapsAndOverLimitRefusal(t *testing.T) {
	f, r := newForegroundFixture(t)
	f.exec(t, "BEGIN")
	for n := int64(2); n < 256; n++ {
		f.exec(t, `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES('asset',?,'video','h264','','',0,'',0,0)`, n)
	}
	for n := int64(2); n < 512; n++ {
		f.exec(t, `INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES('asset',?,'Chapter',0.0,1.0)`, n)
	}
	f.exec(t, "COMMIT")
	in := TitleInput{View: f.input, Item: 1, Asset: 1}
	file, _, e := r.playback(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(file.Streams) != 256 || len(file.Chapters) != 512 {
		t.Fatal("legal caps refused", e)
	}
	f.exec(t, `INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES('asset',512,'Excess',0.0,1.0)`)
	file, _, e = r.playback(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(file, PlaybackFile{}) {
		t.Fatal("chapter overflow", e)
	}
	f.exec(t, `DELETE FROM asset_chapters WHERE chapter_index=512`)
	f.exec(t, `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES('asset',256,'video','h264','','',0,'',0,0)`)
	file, _, e = r.playback(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(file, PlaybackFile{}) {
		t.Fatal("stream class overflow", e)
	}
}
func TestForegroundCreditsContinueWithoutManualTrimming(t *testing.T) {
	f, r := newForegroundFixture(t)
	f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(1,1,150,1,2,'Owner extra','owner','extra',150)`)
	in := TitleInput{View: f.input, Item: 1}
	d, _, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(d.Cast) != 100 || len(d.Crew) != 50 || d.CreditsExhausted || d.Next == nil {
		t.Fatal("credit cap continuation", e)
	}
	in.After = d.Next
	d, _, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(d.Cast) != 1 || d.Cast[0].Name != "Owner extra" || !d.CreditsExhausted {
		t.Fatal("manual credit lost", e)
	}
}

func TestForegroundInvalidClockAndContinuationSource(t *testing.T) {
	f, r := newForegroundFixture(t)
	in := TitleInput{View: f.input, Item: 1}
	in.View.Now = time.Time{}
	d, _, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(d, TitleDetail{}) {
		t.Fatal("missing trusted clock accepted", e)
	}
	in.View = f.input
	d, _, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || d.Next == nil {
		t.Fatal("source cursor fixture", e)
	}
	in.After = d.Next
	// Even an unversioned library change must not let the old item cursor bind a
	// new root/source revision.
	f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=1`)
	d, _, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(d, TitleDetail{}) {
		t.Fatal("continuation source drift escaped", e)
	}
}

func TestForegroundMiddleWindowsRunBusinessOnly(t *testing.T) {
	f, _ := newForegroundFixture(t)
	observed := &foregroundBoundary{Snapshots: f.snapshots, mutate: func(int) {}}
	var sourceWindows []int
	root := func(q browseQuery) ([]any, error) {
		sourceWindows = append(sourceWindows, observed.phase)
		rows, e := q("foreground.source", int64(1), f.input.Library, f.input.Kind)
		if e != nil {
			return nil, e
		}
		return rows[0], nil
	}
	business := func(ctx context.Context, q browseQuery, fence browseFence, draft *[]int64) error {
		rows, e := q("foreground.versions", int64(1), int64(17))
		if e != nil {
			return e
		}
		for _, row := range rows {
			*draft = append(*draft, row[0].(int64))
		}
		return ctx.Err()
	}
	values, result, e := publishRead(t.Context(), observed, foregroundRequest(), time.Second, f.output, f.input, []readStep[[]int64]{business, business, business}, root)
	if e != nil || !reflect.DeepEqual(values, []int64{1, 1, 1}) {
		t.Fatal("business windows", values, e)
	}
	if !reflect.DeepEqual(sourceWindows, []int{1, 4}) || observed.phase != 4 || result.LookupStatements != 9 || result.PublicationStatements != 6 {
		t.Fatalf("unexpected middle fencing: source windows%v physical%d SQL%d+%d", sourceWindows, observed.phase, result.LookupStatements, result.PublicationStatements)
	}
}
