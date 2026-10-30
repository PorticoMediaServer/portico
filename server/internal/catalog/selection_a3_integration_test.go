package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func a3PrepareOrder(t *testing.T, s *Service, v Viewer, request BrowseRequest) (BrowseResult, BrowseSelectionPreparing, string, BrowseSelectionPreparationResult) {
	t.Helper()
	access := SelectionPreparationAccess{Caller: "a3-order-test", Check: func(*Service) error { return nil }}
	var original BrowseResult
	var pending BrowseSelectionPreparing
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		var err error
		original, err = read.BrowseEntities(v, request)
		if err != nil {
			return err
		}
		pending, err = read.SubmitBrowseSelectionPreparation(request, v, *original.Bound, access)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var result BrowseSelectionPreparationResult
		err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			var err error
			result, err = s.WithContext(ctx).PollBrowseSelectionPreparation(request.Library, v, pending.QueryID, access, HomeActionAuthority{})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Ready != nil {
			return original, pending, original.PageInfo.Revision, result
		}
		result.Release()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("A3 prepared order did not finish")
	return BrowseResult{}, BrowseSelectionPreparing{}, "", BrowseSelectionPreparationResult{}
}

func TestA3GroupPreparedConversionQueueAndCommandKeys(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("a", "Movies", "movie", "/a")
	var films []catalogtest.Item
	for i, title := range []string{"averylongprefix Delta", "averylongprefix Alpha", "Other Bravo", "averylongprefix Charlie"} {
		films = append(films, c.Movie(lib, fmt.Sprintf("/a/%d.mkv", i), title, 2000+i))
	}
	shelf := c.Collection(lib, "Shelf", films[3], films[0], films[2], films[1])
	c.Drain()
	s := New(c.DB)
	localOrderedStore(t, s)
	v := Viewer{Profile: "p", Fence: "a3-browse", Libraries: []string{"a"}}
	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			target := BrowseNode{Field: "collection", Operator: "equals", Value: shelf.Public}
			query := &BrowseNode{All: []BrowseNode{target, {Field: "search", Operator: "contains", Value: "averylongprefix"}}}
			sorts := []BrowseSortSelection{{Field: "groupOrder", Direction: direction}}
			original, pending, revision, ready := a3PrepareOrder(t, s, v, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Sort: sorts, Limit: 100})
			defer ready.Release()
			if ready.Ready.Total != 3 {
				t.Fatal(ready.Ready.Total)
			}
			if unavailable := ready.Ready.UnavailableActions; unavailable != nil && (unavailable.Play != "" || unavailable.Queue != "" || unavailable.Playlist != "" || unavailable.Delete != "") {
				t.Fatal("bounded group actions left unavailable", unavailable)
			}
			want := entryIDs(original.Entries)
			err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
				page, err := s.WithContext(ctx).BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Sort: sorts, SelectionBasis: pending.basis, Limit: 200, Range: &BrowseRange{Start: 0, Revision: revision}})
				if err != nil {
					return err
				}
				defer page.Release()
				if got := entryIDs(page.Entries); !reflect.DeepEqual(got, want) {
					t.Fatalf("prepared IDs %v, Browse %v", got, want)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			got := localOrderedDrain(t, localOrderedFixture(t, s, v, QueueQuery{LibraryID: "a", Pivot: "movies", Query: query, Sort: sorts}))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Queue %v, Browse %v", got, want)
			}
			ids := []int64{films[0].ID, films[1].ID, films[3].ID}
			basis, witness := smallOrderBasis(t, s, "a", "p")
			order, err := NewSmallGroupOrder(ids, &target, direction, basis)
			if err != nil {
				t.Fatal(err)
			}
			for !order.Complete() {
				order, _, err = s.StepSmallSelectionOrder(context.Background(), order, witness, SmallSelectionOrderLimits{})
				if err != nil {
					t.Fatal(err)
				}
			}
			keys, err := order.GroupCommandKeys(nil)
			if err != nil {
				t.Fatal(err)
			}
			filter, _ := json.Marshal(target)
			legacy := JobSelector{Query: &JobQuery{LibraryID: "a", Pivot: "movies", Filter: filter, Sort: sorts}}
			if err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
				tx := dbwork.Snapshot(ctx)
				read := s.WithContext(ctx)
				for _, a := range ids {
					for _, b := range ids {
						ka, err := read.bulkSortKey(ctx, tx, v, legacy, a)
						if err != nil {
							return err
						}
						kb, err := read.bulkSortKey(ctx, tx, v, legacy, b)
						if err != nil {
							return err
						}
						if sign(strings.Compare(keys[a], keys[b])) != sign(strings.Compare(ka, kb)) {
							t.Fatalf("changed Playlist command direction/tie %d/%d", a, b)
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

func TestA3ForYouPreparedConversionAndActivityRevalidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	w.s.Clock = func() time.Time { return now }
	localOrderedStore(t, w.s)
	w.watch("p", w.scifi[0])
	w.c.Drain()
	item := w.others[0]
	w.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,60000,0,'old')`, item.ID)
	w.c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','m',?,?,'paused')`, item.ID, now.AddDate(0, 0, -30).Format(time.RFC3339Nano))
	w.c.Drain()
	v := Viewer{Profile: "p", Fence: "a3-for-you", Libraries: []string{"m"}}
	query := &BrowseNode{Any: []BrowseNode{{Field: "search", Operator: "contains", Value: "star"}, {Field: "search", Operator: "contains", Value: "orbit"}}}
	sorts := []BrowseSortSelection{{Field: "forYou", Direction: "desc"}}
	original, pending, revision, ready := a3PrepareOrder(t, w.s, v, BrowseRequest{Library: "m", Pivot: "movies", Query: query, Sort: sorts, Limit: 100})
	defer ready.Release()
	if err := dbwork.WithReadSnapshot(context.Background(), w.s.db, func(ctx context.Context) error {
		page, err := w.s.WithContext(ctx).BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: query, Sort: sorts, SelectionBasis: pending.basis, Limit: 200, Range: &BrowseRange{Start: 0, Revision: revision}})
		if err != nil {
			return err
		}
		defer page.Release()
		if !reflect.DeepEqual(entryIDs(page.Entries), entryIDs(original.Entries)) {
			t.Fatal("ForYou prepared ordering differs from actual Browse")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := smallOrderBasis(t, w.s, "m", "p")
	w.c.Exec(`UPDATE progress_activity SET updated_at=? WHERE profile_id='p' AND item_id=?`, now.Format(time.RFC3339Nano), item.ID)
	after, _ := smallOrderBasis(t, w.s, "m", "p")
	if before != after {
		t.Fatal("activity-only revalidation authored a shared epoch")
	}
	var rejected bool
	err := dbwork.WithReadSnapshot(context.Background(), w.s.db, func(ctx context.Context) error {
		converted, err := w.s.WithContext(ctx).ResolvePreparedBrowseSelection(v, ready.Ready.Selector.Query, revision)
		if err == nil {
			converted.Release()
			return fmt.Errorf("activity-only drift reused ready order")
		}
		rejected = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rejected {
		t.Fatal("missing fresh activity witness")
	}
}

func TestA3ForYouActivityRawWindowBoundsAndHeldInstant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("outside", "Outside", "movie", "/outside")
	now := time.Now().UTC().Truncate(time.Millisecond)
	var last catalogtest.Item
	for i := 0; i < 201; i++ {
		last = c.Movie(lib, fmt.Sprintf("/outside/%d.mkv", i), "Rejected", 2000)
		// Completed entries have no abandonment contribution, but still occupy
		// the production raw top-200 window, including foreign libraries.
		c.Exec(`INSERT INTO progress(profile_id,item_id,position,completed,unit,playback_id) VALUES('p',?,0,1,0,'old')`, last.ID)
		c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','outside',?,?,'paused')`, last.ID, now.AddDate(0, 0, -30).Add(-time.Duration(i)*time.Hour).Format(time.RFC3339Nano))
	}
	c.Drain()
	s := New(c.DB)
	fence := func(rows, bytes int) (string, SelectionBatchWork, error) {
		var stamp string
		var work SelectionBatchWork
		err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			var err error
			stamp, work, err = s.WithContext(ctx).smallForYouActivityFence("p", now, rows, bytes, nil)
			return err
		})
		return stamp, work, err
	}
	before, work, err := fence(4096, 4<<20)
	if err != nil || work.Rows != 800 || work.DecodedBytes == 0 || work.ExaminedBytes <= work.DecodedBytes {
		t.Fatal("raw-before-rejection window was not fully charged", work, err)
	}
	if _, _, err = fence(799, 4<<20); !errors.Is(err, ErrSelectionBatchCapacity) {
		t.Fatal("raw window escaped row admission", err)
	}
	if _, _, err = fence(4096, 64); !errors.Is(err, ErrSelectionBatchCapacity) {
		t.Fatal("timestamp source escaped byte admission", err)
	}
	c.Exec(`UPDATE progress_activity SET updated_at=? WHERE profile_id='p' AND item_id=?`, now.AddDate(0, 0, -60).Format(time.RFC3339Nano), last.ID)
	outside, _, err := fence(4096, 4<<20)
	if err != nil || outside != before {
		t.Fatal("outside-window mutation changed exact raw dependency", err)
	}
	c.Exec(`UPDATE progress_activity SET updated_at=? WHERE profile_id='p' AND item_id=?`, now.AddDate(0, 0, -22).Format(time.RFC3339Nano), last.ID)
	displaced, _, err := fence(4096, 4<<20)
	if err != nil || displaced == before {
		t.Fatal("foreign rejected row did not displace raw window", err)
	}
	// Later wall-clock progression must not move the held cutoff.
	s.Clock = func() time.Time { return now.AddDate(0, 0, 100) }
	held, _, err := fence(4096, 4<<20)
	if err != nil || held != displaced {
		t.Fatal("validation moved ranking instant", err)
	}
}

func TestA3ForYouActivityDriftRejectsContinuationWithoutEpoch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	item := w.others[0]
	w.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,60000,0,'old')`, item.ID)
	w.c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','m',?,?,'paused')`, item.ID, now.AddDate(0, 0, -30).Format(time.RFC3339Nano))
	w.c.Drain()
	basis, witness := smallOrderBasis(t, w.s, "m", "p")
	order, err := NewSmallForYouOrder([]int64{w.scifi[0].ID}, "p", "m", basis, now)
	if err != nil {
		t.Fatal(err)
	}
	order, _, err = w.s.StepSmallSelectionOrder(context.Background(), order, witness, SmallSelectionOrderLimits{Rows: 7, Bytes: 1024})
	if err != nil || order.Complete() {
		t.Fatal("expected bounded unfinished ranking", err)
	}
	w.c.Exec(`UPDATE progress SET position=61000 WHERE profile_id='p' AND item_id=?`, item.ID)
	after, _ := smallOrderBasis(t, w.s, "m", "p")
	if after != basis {
		t.Fatal("within-state resume update bumped global epoch")
	}
	if _, _, err = w.s.StepSmallSelectionOrder(context.Background(), order, witness, SmallSelectionOrderLimits{}); !errors.Is(err, ErrSelectionBatchStale) {
		t.Fatal("changed actual abandonment dependency reused continuation", err)
	}
}
