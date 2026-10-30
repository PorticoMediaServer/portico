package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestBulkPreviewPreparationSharesMemoryOnlyWorkAndChecksLiveBasis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, targets, _ := bulkFixture(t)
	catalogtest.New(t, s.db).Drain()
	var request catalog.BulkMetadataPreviewRequest
	for _, target := range targets {
		request.Targets = append(request.Targets, catalog.BulkMetadataTarget{Kind: target.Kind, ContentKind: "movie", ID: target.ID, LibraryID: "movies"})
	}
	var rows, revoked atomic.Int64
	cat := catalog.New(s.db)
	access := BulkPreviewPreparationAccess{CapturedBytes: 4096, Authorize: func(context.Context, *sql.Tx) error {
		if revoked.Load() != 0 {
			return errors.New("revoked")
		}
		return nil
	}, Source: func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		page, err := cat.BulkMetadataSelectionUnitTx(ctx, tx, catalog.Viewer{Profile: "profile", Libraries: []string{"movies"}}, request, after)
		if err == nil && len(page.Selection.Targets) > 1 {
			t.Error("more than one candidate in quantum")
		}
		rows.Add(int64(len(page.Selection.Targets)))
		return page, err
	}}
	key, plan, err := s.PlanBulkPreview(context.Background(), "canonical-explicit", access)
	if err != nil {
		t.Fatal(err)
	}
	factory := plan.New
	plan.New = func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		work, err := factory(ctx, b)
		if err != nil {
			return nil, err
		}
		return &previewTestComputation{work, t}, nil
	}
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, err := store.Submit(context.Background(), "one", key, plan)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Submit(context.Background(), "two", key, plan)
	if err != nil {
		t.Fatal(err)
	}
	if store.Stats().Entries != 1 {
		t.Fatal(store.Stats())
	}
	deadline := time.Now().Add(5 * time.Second)
	var result readprep.Result
	for {
		result, err = store.Poll(context.Background(), "one", a)
		if err != nil {
			t.Fatal(err)
		}
		if result.State == readprep.Ready {
			break
		}
		result.Release()
		if time.Now().After(deadline) {
			t.Fatal("preparation failed to finish")
		}
		time.Sleep(time.Millisecond)
	}
	defer result.Release()
	var out BulkMetadataPreview
	if err = json.Unmarshal(result.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 || out.Fields["year"].DistinctCount != 3 || len(out.Targets) != 3 {
		t.Fatal(out)
	}
	if err = s.CheckBulkPreviewBasis(context.Background(), result.Basis, access); err != nil {
		t.Fatal(err)
	}
	if store.Stats().Bytes > store.MaxBytes() || rows.Load() > 30 {
		t.Fatal(store.Stats(), rows.Load())
	}
	if _, err = store.Poll(context.Background(), "other", a); !errors.Is(err, readprep.ErrStale) {
		t.Fatal(err)
	}
	if err = store.Cancel(context.Background(), "one", a); err != nil {
		t.Fatal(err)
	}
	other, err := store.Poll(context.Background(), "two", b)
	if err != nil {
		t.Fatal(err)
	}
	other.Release()
	revoked.Store(1)
	if err = s.CheckBulkPreviewBasis(context.Background(), result.Basis, access); err == nil {
		t.Fatal("cached preview bypassed revocation")
	}
}

func TestBulkPreviewPreparationHonestSharedCapacity(t *testing.T) {
	s, targets, _ := bulkFixture(t)
	catalogtest.New(t, s.db).Drain()
	request := catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: targets[0].ID, LibraryID: "movies"}}}
	access := BulkPreviewPreparationAccess{CapturedBytes: 4096, Source: previewRequestSource(s, request), Authorize: previewAuthorize}
	key, plan, err := s.PlanBulkPreview(context.Background(), "one", access)
	if err != nil {
		t.Fatal(err)
	}
	store, err := readprep.New(readprep.Config{MaxBytes: 32 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Submit(context.Background(), "one", key, plan); !errors.Is(err, readprep.ErrCapacity) {
		t.Fatal(err)
	}
	if store.Stats().Entries != 0 {
		t.Fatal(store.Stats())
	}
}

type previewTestComputation struct {
	readprep.Computation
	t *testing.T
}

func (w *previewTestComputation) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	result, err := w.Computation.Step(ctx, b)
	if err != nil {
		w.t.Logf("preparation quantum error: %T %v", err, err)
	}
	return result, err
}
