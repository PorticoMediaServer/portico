package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// This factory exercises the real background count computation independently of
// the public admission switch, which stays off until the full cohort is checked.
func prepareLocalConversion(t *testing.T, s *Service, v Viewer, q *BrowseNode, sorts ...BrowseSortSelection) (BrowseSelectionPreparing, string) {
	t.Helper()
	var pending BrowseSelectionPreparing
	var revision string
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		page, err := read.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: q, Sort: sorts, Limit: 1})
		if err != nil {
			return err
		}
		revision = page.PageInfo.Revision
		basis, err := read.browseSelectionWitness("a", v.Profile, v.EffectiveRestrictions())
		if err != nil {
			return err
		}
		state, err := read.NewBrowseSelectionBatch(BrowseRequest{}, v, *page.Bound, basis)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(q)
		if err != nil {
			return err
		}
		spec, err := json.Marshal(selectionPreparationSpec{Library: "a", Pivot: "movies", Query: raw, Sorts: page.Applied.Sort, Viewer: selectionPreparationViewer(v, "a")})
		if err != nil {
			return err
		}
		comp := &selectionPreparationComputation{service: s, state: state, check: func(*Service) error { return nil }}
		key := readprep.Key{Namespace: browseSelectionPreparationDomain, Basis: basis, Canonical: string(spec)}
		h, err := s.ReadPreparations().Submit(ctx, "conversion-test", key, readprep.Plan{ColdSearch: true, InitialBytes: comp.AccountedBytes() + int64(unsafe.Sizeof(comp)), New: func(context.Context, *readprep.Budget) (readprep.Computation, error) { return comp, nil }})
		if err != nil {
			return err
		}
		pending = selectionPreparationPending(string(h))
		pending.basis = basis
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pending, revision
}

func TestPreparedLocalConversionUsesCompleteIDsAndLiveBasis(t *testing.T) {
	_, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	v := Viewer{Profile: "p", Fence: "browse-route", Libraries: []string{"a"}}
	q := &BrowseNode{Field: "search", Operator: "contains", Value: "a"}
	pending, revision := prepareLocalConversion(t, s, v, q)
	access := SelectionPreparationAccess{Caller: "conversion-test", Check: func(*Service) error { return nil }}
	ready := awaitSelectionPreparation(t, s, v, pending.QueryID, access, HomeActionAuthority{})
	defer ready.Release()
	if ready.Ready.Total != 1 {
		t.Fatalf("actual prefix result %+v", ready.Ready)
	}
	original := *ready.Ready
	// Another route may carry a different private fence and a wider authorized
	// library list. This does not change the held single-library content basis.
	metadataViewer := v
	metadataViewer.Fence = "metadata-route"
	metadataViewer.Libraries = []string{"tv", "a"}
	err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		converted, err := read.ResolvePreparedBrowseSelection(metadataViewer, original.Selector.Query, revision)
		if err != nil {
			return err
		}
		defer converted.Release()
		if len(converted.IDs) != 1 {
			t.Fatal(converted.IDs)
		}
		request := BrowseRequest{Library: "a", Pivot: "movies", Query: q, Sort: original.Selector.Query.Sort, SelectionBasis: pending.basis, Limit: 200, Range: &BrowseRange{Start: 0, Revision: revision}}
		page, err := read.BrowseEntities(v, request)
		if err != nil {
			return err
		}
		defer page.Release()
		if len(page.Entries) != 1 || page.PageInfo.Total != 1 || page.PageInfo.HasMore || page.PageInfo.TotalIsLowerBound || page.PageInfo.Revision != revision {
			t.Fatalf("incomplete explicit conversion %+v", page.PageInfo)
		}
		changed := original.Selector.Query
		changed.Filter = json.RawMessage(`{"field":"search","operator":"contains","value":"b"}`)
		if bad, e := read.ResolvePreparedBrowseSelection(metadataViewer, changed, revision); e == nil {
			bad.Release()
			t.Fatal("changed query borrowed the count proof")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, *ready.Ready) {
		t.Fatal("consumer mutated shared descriptor")
	}
	if _, err = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassInteractive, `UPDATE catalog_browse_predicate_epoch SET revision=revision+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		r, e := s.WithContext(ctx).ResolvePreparedBrowseSelection(v, original.Selector.Query, revision)
		r.Release()
		return e
	})
	if !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("late drift %v", err)
	}
}

func TestPreparedLocalConversionMaintainedSortParity(t *testing.T) {
	_, s, _ := browseFixture(t)
	localOrderedStore(t, s)
	v := Viewer{Profile: "p", Fence: "browse", Libraries: []string{"a"}}
	q := &BrowseNode{Not: &BrowseNode{Field: "search", Operator: "contains", Value: "unmatched"}}
	for _, d := range browseSorts {
		if !containsString(d.ApplicableKinds, "movie") {
			continue
		}
		for _, direction := range d.Directions {
			selected := BrowseSortSelection{Field: d.ID, Direction: direction}
			if !LocalSearchQueueSupported(q, []BrowseSortSelection{selected}) {
				continue
			}
			t.Run(d.ID+"/"+direction, func(t *testing.T) {
				oracle, e := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: q, Sort: []BrowseSortSelection{selected}, Limit: 200})
				if e != nil {
					t.Fatal(e)
				}
				pending, revision := prepareLocalConversion(t, s, v, q, selected)
				ready := awaitSelectionPreparation(t, s, v, pending.QueryID, SelectionPreparationAccess{Caller: "conversion-test", Check: func(*Service) error { return nil }}, HomeActionAuthority{})
				defer ready.Release()
				page, e := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: q, Sort: []BrowseSortSelection{selected}, SelectionBasis: pending.basis, Limit: 200, Range: &BrowseRange{Start: 0, Revision: revision}})
				if e != nil {
					t.Fatal(e)
				}
				defer page.Release()
				if len(page.Entries) != len(oracle.Entries) {
					t.Fatal("cardinality")
				}
				for i, entry := range page.Entries {
					if entry.ID != oracle.Entries[i].ID || entry.Title != oracle.Entries[i].Title {
						t.Fatalf("order %v != %v", page.Entries, oracle.Entries)
					}
				}
			})
		}
	}
}

func TestPreparedLocalConversionByteAdmissionAndJobsFence(t *testing.T) {
	db, s, names := browseFixture(t)
	localOrderedStore(t, s)
	v := Viewer{Profile: "p", Fence: "browse-route", Libraries: []string{"a"}}
	q := &BrowseNode{Field: "search", Operator: "contains", Value: "a"}
	// A large legal serving title exercises conversion byte admission. Capture
	// the complete witness after this base-fact change.
	if _, err := db.Exec(`UPDATE catalog_browse_rows SET title=? WHERE entity_id=?`, strings.Repeat("x", 3<<20), names["m1"].ID); err != nil {
		t.Fatal(err)
	}
	pending, revision := prepareLocalConversion(t, s, v, q)
	ready := awaitSelectionPreparation(t, s, v, pending.QueryID, SelectionPreparationAccess{Caller: "conversion-test", Check: func(*Service) error { return nil }}, HomeActionAuthority{})
	defer ready.Release()
	jobs := v
	jobs.Fence = "jobs-route"
	err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		// The real local admission branch is gated until final qualification. Its
		// revision/provenance seam is tested here with a different route fence.
		proof, e := read.resolvePreparedBrowseSelection(jobs, ready.Ready.Selector.Query, revision, false)
		if e != nil {
			return e
		}
		proof.Release()
		old, e := read.bulkReplayBound(jobs, &ready.Ready.Selector.Query)
		if e != nil {
			return e
		}
		if old.Revision == revision {
			t.Fatal("fixture does not distinguish route fences")
		}
		if selectionBatchLocalSearchEnabled {
			got, _, e := read.bulkSelection(identity.Principal{}, jobs, JobRequest{Command: "personal-state", Selector: JobSelector{Query: &ready.Ready.Selector.Query}, Expected: &JobExpected{CatalogRevision: revision}})
			if e != nil {
				return e
			}
			if got != bulkReplaySelection {
				t.Fatalf("wrong replay %q", got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: q, SelectionBasis: pending.basis, Limit: 200, Range: &BrowseRange{Start: 0, Revision: revision}})
	if !errors.Is(err, readprep.ErrCapacity) {
		t.Fatalf("large source accepted: %v", err)
	}
}
