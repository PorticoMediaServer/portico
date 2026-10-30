package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestSelectionProofReadyDriftRejectsEveryAdmission(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, scheduler, p, v, access, c, _ := bulkFixture(t, 3)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	prepareAccess := SelectionPreparationAccess{Caller: "browse-selection/lib/session", Check: func(*Service) error { return nil }}
	var pending BrowseSelectionPreparing
	var revision string
	if err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		request := BrowseRequest{Library: "lib", Pivot: "episodes", Limit: 1}
		page, e := read.BrowseEntities(v, request)
		if e != nil {
			return e
		}
		revision = page.PageInfo.Revision
		pending, e = read.SubmitBrowseSelectionPreparation(request, v, *page.Bound, prepareAccess)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var descriptor BrowseSelectionDescriptor
	// Poll on the actual library; the shared fixture poll helper is movie scoped.
	for i := 0; i < 1000; i++ {
		err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			result, e := s.WithContext(ctx).PollBrowseSelectionPreparation("lib", v, pending.QueryID, prepareAccess, HomeActionAuthority{})
			defer result.Release()
			if e != nil {
				return e
			}
			if result.Ready != nil {
				descriptor = *result.Ready
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.Selector.Query.SelectionBasis != "" {
			break
		}
		// Yield to the independent shared worker without delaying source work.
		time.Sleep(time.Millisecond)
	}
	basis := descriptor.Selector.Query.SelectionBasis
	if !selectionBasisPattern.MatchString(basis) || descriptor.Total != 3 {
		t.Fatalf("missing ready proof %+v", descriptor)
	}
	check := func(wantStale bool) {
		t.Helper()
		err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			read := s.WithContext(ctx)
			tx := dbwork.Snapshot(ctx)
			_, e := read.BrowseEntities(v, BrowseRequest{Library: "lib", Pivot: "episodes", Limit: 200, SelectionBasis: basis})
			if errors.Is(e, ErrStaleContinuation) != wantStale || (!wantStale && e != nil) {
				t.Fatalf("browse proof stale=%v: %v", wantStale, e)
			}
			_, e = read.BulkMetadataSelectionTx(ctx, tx, v, BulkMetadataPreviewRequest{Selector: &JobSelector{Query: &descriptor.Selector.Query}, Expected: &JobExpected{CatalogRevision: revision}})
			if errors.Is(e, ErrStaleContinuation) != wantStale || (!wantStale && e != nil) {
				t.Fatalf("preview proof stale=%v: %v", wantStale, e)
			}
			q := &QueueQuery{LibraryID: "lib", Pivot: "episodes", SelectionBasis: basis, SelectionRevision: revision}
			_, e = read.QueueKeysTx(ctx, tx, v, ResourceActor{}, q, "", nil)
			if errors.Is(e, ErrStaleContinuation) != wantStale || (!wantStale && e != nil) {
				t.Fatalf("queue proof stale=%v: %v", wantStale, e)
			}
			e = read.CheckQueueBrowseRevisionTx(ctx, tx, v, q)
			if errors.Is(e, ErrStaleContinuation) != wantStale || (!wantStale && e != nil) {
				t.Fatalf("queue revision stale=%v: %v", wantStale, e)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(false)
	// Predicate epoch changes with no ordinary order/publication revision change.
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('held-proof-drift',18)`)
	page, err := s.BrowseEntities(v, BrowseRequest{Library: "lib", Pivot: "episodes", Limit: 1})
	if err != nil || page.PageInfo.Revision != revision {
		t.Fatal("ordinary continuation changed", err)
	}
	check(true)
	yes := true
	_, err = s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "held-proof-stale", Command: "personal-state", Selector: JobSelector{Query: &descriptor.Selector.Query}, Expected: &JobExpected{CatalogRevision: revision}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access)
	if !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("job admitted stale ready proof", err)
	}
	// Legacy authored queries remain optional rather than acquiring held-proof semantics.
	descriptor.Selector.Query.SelectionBasis = ""
	_, err = s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "legacy-query", Command: "personal-state", Selector: JobSelector{Query: &descriptor.Selector.Query}, Expected: &JobExpected{CatalogRevision: revision}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access)
	if err != nil {
		t.Fatal("legacy authored query refused", err)
	}
}

func TestSelectionProofStrictOptionalWire(t *testing.T) {
	for _, proof := range []string{`null`, `""`, `"ABC"`, `"` + strings.Repeat("A", 64) + `"`} {
		raw := []byte(`{"libraryId":"a","pivot":"movies","selectionBasis":` + proof + `}`)
		var job JobQuery
		var queue QueueQuery
		if json.Unmarshal(raw, &job) == nil || json.Unmarshal(raw, &queue) == nil {
			t.Fatalf("accepted invalid proof %s", proof)
		}
	}
	basis := strings.Repeat("a", 64)
	raw := []byte(`{"libraryId":"a","pivot":"movies","selectionBasis":"` + basis + `"}`)
	var job JobQuery
	var queue QueueQuery
	if json.Unmarshal(raw, &job) != nil || json.Unmarshal(raw, &queue) != nil || job.SelectionBasis != basis || queue.SelectionBasis != basis {
		t.Fatal("proof lost")
	}
}
