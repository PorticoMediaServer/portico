package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func replayTestStore(t *testing.T, s *Service) *readprep.Store {
	t.Helper()
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	s.UseReadPreparations(store)
	return store
}

func replayTestQuery(t *testing.T, s *Service, v Viewer, filter string) JobQuery {
	t.Helper()
	q := JobQuery{LibraryID: "lib", Pivot: "episodes", Filter: json.RawMessage(filter)}
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		q.SelectionBasis, err = s.WithContext(ctx).browseSelectionWitness("lib", v.Profile, v.EffectiveRestrictions())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

func submitReplayTest(t *testing.T, s *Service, v Viewer, q JobQuery, after int64, access SelectionPreparationAccess) BrowseSelectionPreparing {
	t.Helper()
	var pending BrowseSelectionPreparing
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		bound, err := read.bulkReplayBound(v, &q)
		if err != nil {
			return err
		}
		pending, err = read.SubmitBrowseSelectionReplay(BrowseRequest{}, v, bound, after, access)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return pending
}

func pollReplayTest(t *testing.T, s *Service, v Viewer, id string, access SelectionPreparationAccess) (BrowseSelectionReplayPage, error) {
	t.Helper()
	var page BrowseSelectionReplayPage
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		page, err = s.WithContext(ctx).PollBrowseSelectionReplay("lib", v, id, access)
		return err
	})
	return page, err
}

func awaitReplayTest(t *testing.T, s *Service, v Viewer, id string, access SelectionPreparationAccess) BrowseSelectionReplayPage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page, err := pollReplayTest(t, s, v, id, access)
		if err != nil {
			t.Fatal(err)
		}
		if !page.Preparing {
			return page
		}
		page.Release()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("replay did not finish bounded page")
	return BrowseSelectionReplayPage{}
}

func TestSelectionReplayBoundedPagesAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, _, v, _, _, names := bulkFixture(t, 145)
	store := replayTestStore(t, s)
	q := replayTestQuery(t, s, v, `null`)
	access := SelectionPreparationAccess{Caller: "test/replay", Check: func(*Service) error { return nil }}
	after := int64(0)
	var got []int64
	pages := 0
	for {
		pending := submitReplayTest(t, s, v, q, after, access)
		page := awaitReplayTest(t, s, v, pending.QueryID, access)
		if len(page.IDs) > 128 {
			t.Fatal("oversize page", len(page.IDs))
		}
		for _, id := range page.IDs {
			if id <= after {
				t.Fatal("nonmonotone replay", id, after)
			}
			after = id
			got = append(got, id)
		}
		eof := page.EOF
		page.Release()
		if err := store.Cancel(context.Background(), access.Caller, readprep.Handle(pending.QueryID)); err != nil {
			t.Fatal(err)
		}
		// Restart loses every reducer and handle, but the accepted ID suffices.
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		s = New(s.db)
		store = replayTestStore(t, s)
		pages++
		if eof {
			break
		}
		if pages > 20 {
			t.Fatal("no bounded replay progress")
		}
	}
	var want []int64
	for i := 0; i < 145; i++ {
		want = append(want, names[fmt.Sprintf("ep%06d", i)].ID)
	}
	if !reflect.DeepEqual(got, want) || pages < 2 {
		t.Fatal("replay differs from leaf membership", len(got), len(want), pages)
	}
	if store.Stats().Handles != 0 || store.Stats().Deliveries != 0 {
		t.Fatal("lease leaked", store.Stats())
	}
}

func TestSelectionReplayPolicyDriftAndPrivateHandles(t *testing.T) {
	s, _, _, v, _, c, _ := bulkFixture(t, 4)
	store := replayTestStore(t, s)
	q := replayTestQuery(t, s, v, `null`)
	access := SelectionPreparationAccess{Caller: "test/replay", Check: func(*Service) error { return nil }}
	first := submitReplayTest(t, s, v, q, 0, access)
	second := submitReplayTest(t, s, v, q, 0, access)
	if first.QueryID == second.QueryID || store.Stats().Entries != 1 {
		t.Fatal("canonical sharing or private handles", store.Stats())
	}
	foreign := access
	foreign.Caller = "another/caller"
	if _, err := pollReplayTest(t, s, v, first.QueryID, foreign); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("foreign handle accepted", err)
	}
	page := awaitReplayTest(t, s, v, first.QueryID, access)
	page.Release()
	changed := v
	changed.Fence = "different"
	if _, err := pollReplayTest(t, s, changed, first.QueryID, access); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("changed policy accepted", err)
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('new-replay-input',18)`)
	if _, err := pollReplayTest(t, s, v, second.QueryID, access); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("ready drift accepted", err)
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("failed poll leaked delivery", store.Stats())
	}
}
