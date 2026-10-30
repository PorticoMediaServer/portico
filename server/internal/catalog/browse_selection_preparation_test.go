package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func submitSelectionPreparation(t *testing.T, s *Service, v Viewer, access SelectionPreparationAccess, query ...*BrowseNode) (BrowseSelectionPreparing, string) {
	t.Helper()
	var pending BrowseSelectionPreparing
	var revision string
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		request := BrowseRequest{Library: "a", Pivot: "movies", Limit: 1}
		if len(query) > 0 {
			request.Query = query[0]
		}
		page, err := read.BrowseEntities(v, request)
		if err != nil {
			return err
		}
		revision = page.PageInfo.Revision
		pending, err = read.SubmitBrowseSelectionPreparation(request, v, *page.Bound, access)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return pending, revision
}

func pollSelectionPreparation(t *testing.T, s *Service, v Viewer, id string, access SelectionPreparationAccess, authority HomeActionAuthority) (BrowseSelectionPreparationResult, error) {
	t.Helper()
	var result BrowseSelectionPreparationResult
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		result, err = s.WithContext(ctx).PollBrowseSelectionPreparation("a", v, id, access, authority)
		return err
	})
	return result, err
}

func awaitSelectionPreparation(t *testing.T, s *Service, v Viewer, id string, access SelectionPreparationAccess, authority HomeActionAuthority) BrowseSelectionPreparationResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Synchronize through memory, leaving a real quiet gap for the cold
		// maintenance worker. The final public adapter poll still checks live
		// authority and the complete witness in its own snapshot.
		result, err := s.ReadPreparations().Poll(context.Background(), access.Caller, readprep.Handle(id))
		state := result.State
		result.Release()
		if err != nil {
			t.Fatal(err)
		}
		if state == readprep.Ready {
			checked, err := pollSelectionPreparation(t, s, v, id, access, authority)
			if err != nil {
				t.Fatal(err)
			}
			if checked.Ready == nil {
				checked.Release()
				t.Fatal("completed content did not produce a descriptor")
			}
			return checked
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("bounded selection preparation did not finish")
	return BrowseSelectionPreparationResult{}
}

func TestSelectionPreparationSharedContentLiveAuthorityAndLeases(t *testing.T) {
	_, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	check := func(read *Service) error {
		if dbwork.ClassFrom(read.Context(), dbwork.ClassInteractive) == dbwork.ClassBackgroundMedia {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-read.Context().Done():
				return read.Context().Err()
			}
		}
		return nil
	}
	access := SelectionPreparationAccess{Caller: "browse-selection/a/session-1", Check: check, CapturedBytes: 1024}
	first, revision := submitSelectionPreparation(t, s, v, access)
	second, otherRevision := submitSelectionPreparation(t, s, v, access)
	if first.QueryID == second.QueryID || revision != otherRevision || store.Stats().Entries != 1 {
		t.Fatal("equivalent content did not share one computation")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	result, err := pollSelectionPreparation(t, s, v, second.QueryID, access, HomeActionAuthority{})
	if err != nil || result.Preparing == nil || store.Stats().Deliveries != 1 {
		t.Fatal(result.Preparing, store.Stats(), err)
	}
	result.Release()
	result.Release()
	if store.Stats().Deliveries != 0 {
		t.Fatal("pending delivery leaked")
	}
	foreign := access
	foreign.Caller = "browse-selection/other-library/session-1"
	if _, err = pollSelectionPreparation(t, s, v, first.QueryID, foreign, HomeActionAuthority{}); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("foreign scope consumed a handle", err)
	}
	if err = store.Cancel(context.Background(), access.Caller, readprep.Handle(first.QueryID)); err != nil {
		t.Fatal(err)
	}
	close(release)
	ready := awaitSelectionPreparation(t, s, v, second.QueryID, access, HomeActionAuthority{Playback: true, DeleteLibrary: func(string) (bool, error) { return true, nil }})
	if ready.Ready.CatalogRevision != revision || ready.Ready.Total != 5 || ready.Ready.Types[0].CapabilityCounts.Play != 3 || ready.Ready.Types[0].CapabilityCounts.Delete != 5 {
		t.Fatalf("ready %+v", ready.Ready)
	}
	ready.Release()
	denied, err := pollSelectionPreparation(t, s, v, second.QueryID, access, HomeActionAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Ready == nil || denied.Ready.Types[0].CapabilityCounts != (SelectionCapabilityCounts{}) {
		t.Fatalf("cached command grants %+v", denied.Ready)
	}
	denied.Release()
	raw, err := store.Poll(context.Background(), access.Caller, readprep.Handle(second.QueryID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw.Payload), "CapabilityCounts") || strings.Contains(string(raw.Payload), "DeleteLibrary") {
		t.Fatal("shared payload retained command authority")
	}
	raw.Release()
	if store.Stats().Deliveries != 0 {
		t.Fatal("ready delivery leaked")
	}
}

func TestSelectionPreparationBasisIsSeparateFromOrdinaryPageRevision(t *testing.T) {
	db, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	access := SelectionPreparationAccess{Caller: "browse-selection/a/session", Check: func(*Service) error { return nil }}
	pending, revision := submitSelectionPreparation(t, s, v, access)
	ready := awaitSelectionPreparation(t, s, v, pending.QueryID, access, HomeActionAuthority{})
	ready.Release()
	// A raw decoration revision changes exact aggregate basis, while the
	// ordinary page continues to use its published order revision.
	if _, err = db.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='a'`); err != nil {
		t.Fatal(err)
	}
	page, err := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Revision != revision {
		t.Fatal("aggregate basis leaked into ordinary page revision")
	}
	if _, err = pollSelectionPreparation(t, s, v, pending.QueryID, access, HomeActionAuthority{}); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("raw decoration reused stale aggregate", err)
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("stale delivery leaked")
	}
	replacement, newRevision := submitSelectionPreparation(t, s, v, access)
	if newRevision != revision || replacement.QueryID == pending.QueryID {
		t.Fatal("drift failed to reprepare independently")
	}
	current := awaitSelectionPreparation(t, s, v, replacement.QueryID, access, HomeActionAuthority{})
	current.Release()
}

func TestSelectionPreparationSavedGenerationAndDriftErrorMapping(t *testing.T) {
	db, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	access := SelectionPreparationAccess{Caller: "browse-selection/a/session", Check: func(*Service) error { return nil }}
	pending, _ := submitSelectionPreparation(t, s, v, access)
	ready := awaitSelectionPreparation(t, s, v, pending.QueryID, access, HomeActionAuthority{})
	ready.Release()
	var before, after string
	witness := func() (string, error) {
		var value string
		err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
			var err error
			value, err = s.WithContext(ctx).browseExactWitness("a", "p", v.Restrictions)
			return err
		})
		return value, err
	}
	before, err = witness()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO saved_pins(owner_key,kind,resource_id,pinned,revision) VALUES('p','collection','saved',1,1)`); err != nil {
		t.Fatal(err)
	}
	after, err = witness()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("fixture unexpectedly moved common witness")
	}
	if _, err = pollSelectionPreparation(t, s, v, pending.QueryID, access, HomeActionAuthority{}); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("saved-input generation was omitted", err)
	}
	for _, drift := range []error{ErrSelectionBatchStale, ErrReadingProjectionChanged, ErrVisibilityBuilding, ErrStaleContinuation} {
		if !errors.Is(selectionPreparationError(drift), readprep.ErrStale) {
			t.Fatalf("drift became generic worker failure: %v", drift)
		}
	}
}

func TestSelectionPreparationFactoryReservesBeforeSourceConstruction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	access := SelectionPreparationAccess{Caller: "browse-selection/a/session", Check: func(*Service) error { return nil }}
	pending, _ := submitSelectionPreparation(t, s, v, access)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result, err := pollSelectionPreparation(t, s, v, pending.QueryID, access, HomeActionAuthority{})
		result.Release()
		if errors.Is(err, readprep.ErrCapacity) {
			if store.Stats().Bytes > store.MaxBytes() {
				t.Fatal("capacity exceeded")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("factory did not return typed capacity")
}

func TestSelectionPreparationSharesAcrossSessionFences(t *testing.T) {
	_, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	firstViewer := Viewer{Profile: "p", Fence: "session-one", Libraries: []string{"a", "b"}}
	secondViewer := Viewer{Profile: "p", Fence: "session-two", Libraries: []string{"a"}}
	firstAccess := SelectionPreparationAccess{Caller: "first", Check: func(*Service) error { return nil }}
	secondAccess := SelectionPreparationAccess{Caller: "second", Check: func(*Service) error { return nil }}
	first, firstRevision := submitSelectionPreparation(t, s, firstViewer, firstAccess)
	second, secondRevision := submitSelectionPreparation(t, s, secondViewer, secondAccess)
	if firstRevision == secondRevision || first.QueryID == second.QueryID || store.Stats().Entries != 1 {
		t.Fatalf("caller fences prevented content sharing: %s %s %+v", firstRevision, secondRevision, store.Stats())
	}
	for _, item := range []struct {
		viewer       Viewer
		access       SelectionPreparationAccess
		id, revision string
	}{
		{firstViewer, firstAccess, first.QueryID, firstRevision},
		{secondViewer, secondAccess, second.QueryID, secondRevision},
	} {
		ready := awaitSelectionPreparation(t, s, item.viewer, item.id, item.access, HomeActionAuthority{})
		if ready.Ready.CatalogRevision != item.revision || ready.Ready.Total != 5 {
			t.Fatalf("private revision was not rebound: %+v", ready.Ready)
		}
		ready.Release()
	}
	if _, err := pollSelectionPreparation(t, s, secondViewer, first.QueryID, secondAccess, HomeActionAuthority{}); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("shared content exposed another caller's handle", err)
	}
}

func TestSelectionPreparationLiveAvailabilityDriftBeforePublication(t *testing.T) {
	db, s, names := browseFixture(t)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	access := SelectionPreparationAccess{Caller: "availability-drift", Check: func(*Service) error { return nil }}
	pending, revision := submitSelectionPreparation(t, s, viewer, access)
	before := awaitSelectionPreparation(t, s, viewer, pending.QueryID, access, HomeActionAuthority{Playback: true})
	oldPlay := before.Ready.Types[0].CapabilityCounts.Play
	before.Release()
	// A no-op and a rolled-back change do not retire a valid held result.
	if _, err = db.Exec(`UPDATE catalog_item_availability SET available=available WHERE entity_id=?`, names["m1"].ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE catalog_item_availability SET available=0 WHERE entity_id=?`, names["m1"].ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	unchanged, err := pollSelectionPreparation(t, s, viewer, pending.QueryID, access, HomeActionAuthority{Playback: true})
	if err != nil {
		t.Fatal("no-op/rollback retired unchanged facts", err)
	}
	unchanged.Release()
	// The live availability relation changes before any asynchronous browse
	// publication. The held aggregate must not use its old play/queue counters.
	if _, err = db.Exec(`UPDATE catalog_item_availability SET available=0 WHERE entity_id=?`, names["m1"].ID); err != nil {
		t.Fatal(err)
	}
	ordinary, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 1})
	if err != nil || ordinary.PageInfo.Revision != revision {
		t.Fatal("ordinary publication token changed", ordinary.PageInfo.Revision, err)
	}
	if _, err = pollSelectionPreparation(t, s, viewer, pending.QueryID, access, HomeActionAuthority{Playback: true}); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("unpublished live availability reused aggregate", err)
	}
	replacement, _ := submitSelectionPreparation(t, s, viewer, access)
	after := awaitSelectionPreparation(t, s, viewer, replacement.QueryID, access, HomeActionAuthority{Playback: true})
	defer after.Release()
	if after.Ready.Types[0].CapabilityCounts.Play != oldPlay-1 {
		t.Fatal("new basis missed live availability", after.Ready.Types)
	}
}

func TestSelectionPreparationMissingInputEpochIsMiss(t *testing.T) {
	db, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	access := SelectionPreparationAccess{Caller: "missing-input", Check: func(*Service) error { return nil }}
	pending, _ := submitSelectionPreparation(t, s, viewer, access)
	ready := awaitSelectionPreparation(t, s, viewer, pending.QueryID, access, HomeActionAuthority{})
	ready.Release()
	if _, err = db.Exec(`DELETE FROM catalog_selection_predicate_epoch`); err != nil {
		t.Fatal(err)
	}
	if _, err = pollSelectionPreparation(t, s, viewer, pending.QueryID, access, HomeActionAuthority{}); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("missing input reused result", err)
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("miss leaked delivery", store.Stats())
	}
}

func TestSelectionPreparationLocalSearchSharesAcrossPrivateFences(t *testing.T) {
	_, s, _ := browseFixture(t)
	store := localOrderedStore(t, s)
	q := &BrowseNode{Field: "search", Operator: "contains", Value: "a"}
	first := Viewer{Profile: "p", Fence: "one", Libraries: []string{"a", "tv"}}
	second := Viewer{Profile: "p", Fence: "two", Libraries: []string{"a"}}
	a := SelectionPreparationAccess{Caller: "first-local", Check: func(*Service) error { return nil }}
	b := a
	b.Caller = "second-local"
	one, revisionOne := submitSelectionPreparation(t, s, first, a, q)
	two, revisionTwo := submitSelectionPreparation(t, s, second, b, q)
	if one.QueryID == two.QueryID || revisionOne == revisionTwo || store.Stats().Entries != 1 {
		t.Fatal("private identity prevented content coalescing", store.Stats())
	}
	for _, test := range []struct {
		v            Viewer
		a            SelectionPreparationAccess
		id, revision string
	}{{first, a, one.QueryID, revisionOne}, {second, b, two.QueryID, revisionTwo}} {
		ready := awaitSelectionPreparation(t, s, test.v, test.id, test.a, HomeActionAuthority{})
		defer ready.Release()
		if ready.Ready.Total != 1 || ready.Ready.CatalogRevision != test.revision {
			t.Fatalf("private ready %+v", ready.Ready)
		}
	}
}
