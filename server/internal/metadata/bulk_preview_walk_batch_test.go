package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// These tests pin the emitted-window batch: one snapshot and one complete basis
// fence pair per window slice, the existing 64-candidate/10ms bounds, one
// ephemeral page at a time, charges reserved before retention, and no output at
// all from a refused slice.

// bulkPreviewWalkWindowSource is the repeatable basis source the walking
// reducer fences against. It selects no membership: the walker's window already
// carries the exact matched ids.
func bulkPreviewWalkWindowSource() BulkPreviewSource {
	return func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		var fence string
		if err := tx.QueryRowContext(ctx, `SELECT printf('%d:%d',revision,metadata_revision) FROM library_revisions WHERE library_id='movies'`).Scan(&fence); err != nil {
			return catalog.BulkMetadataSelectionPage{}, err
		}
		return catalog.BulkMetadataSelectionPage{Fence: fence, Selection: catalog.BulkMetadataSelection{Revision: "bulk-window"}, Done: true}, nil
	}
}

// bulkPreviewWalkWindowMovies seeds real baseline catalog rows and returns their
// entity ids in walk order.
func bulkPreviewWalkWindowMovies(t *testing.T, count int) (*Service, []int64) {
	t.Helper()
	s, db := editorFixture(t)
	fixture := catalogtest.New(t, db)
	movies := fixture.Handle("movies")
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		item := fixture.Movie(movies, fmt.Sprintf("/movies/window-%d.mkv", i), fmt.Sprintf("Window %d", i), 2000+i)
		ids = append(ids, item.ID)
	}
	fixture.Drain()
	return s, ids
}

func bulkPreviewWalkWindowComputation(t *testing.T, s *Service, ids []int64, authorize func(context.Context, *sql.Tx) error) *bulkPreviewComputation {
	t.Helper()
	state, err := newBulkPreviewReduction(s, bulkPreviewWalkWindowSource(), authorize, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	w := &bulkPreviewComputation{state: state, base: 4096}
	if len(ids) > len(w.matches) {
		t.Fatalf("fixture window %d exceeds the fixed match array", len(ids))
	}
	copy(w.matches[:], ids)
	w.matchCount = len(ids)
	return w
}

func TestBulkPreviewWalkReducesMatchWindowInOneSnapshot(t *testing.T) {
	for _, count := range []int{1, 4} {
		s, ids := bulkPreviewWalkWindowMovies(t, count)
		w := bulkPreviewWalkWindowComputation(t, s, ids, previewAuthorize)

		ctx, cost := dbwork.Measure(context.Background())
		if err := w.drainMatchWindowUntil(ctx, time.Now().Add(time.Minute)); err != nil {
			t.Fatalf("%d targets: %v", count, err)
		}
		// Snapshot count is independent of the match count: one window, one
		// short snapshot, instead of one snapshot per target.
		if got := cost(); got.Transactions != 1 {
			t.Fatalf("%d targets opened %d snapshots, want 1: %+v", count, got.Transactions, got)
		}
		if w.matchNext != len(ids) || w.matchCount != len(ids) {
			t.Fatalf("%d targets: window progress %d/%d", count, w.matchNext, w.matchCount)
		}
		for i := range ids {
			if w.matches[i] != 0 {
				t.Fatalf("%d targets: released window slot %d leaked %d", count, i, w.matches[i])
			}
		}
		if w.state.out.Total != int64(len(ids)) || w.state.done {
			t.Fatalf("%d targets: staged total=%d done=%v", count, w.state.out.Total, w.state.done)
		}
		// Each target was reduced exactly once: distinct years, one each.
		year := w.state.aggregate.fields["year"]
		if year == nil || len(year.values) != len(ids) {
			t.Fatalf("%d targets: aggregate year values %+v", count, year)
		}
		for i, value := range year.values {
			if value.count != 1 || value.value != fmt.Sprint(2000+i) {
				t.Fatalf("%d targets: window value %d = %q x%d", count, i, value.value, value.count)
			}
		}
	}
}

func TestBulkPreviewWalkStopsAfterEachIndivisibleTarget(t *testing.T) {
	s, ids := bulkPreviewWalkWindowMovies(t, 3)
	w := bulkPreviewWalkWindowComputation(t, s, ids, previewAuthorize)
	expired := time.Now().Add(-time.Hour)
	for processed := 1; processed <= len(ids); processed++ {
		ctx, cost := dbwork.Measure(context.Background())
		if err := w.drainMatchWindowUntil(ctx, expired); err != nil {
			t.Fatalf("slice %d: %v", processed, err)
		}
		// One whole target per expired slice: never zero (no starvation) and
		// never two (the 10ms slice is observed between targets only).
		if w.matchNext != processed {
			t.Fatalf("slice %d advanced to %d", processed, w.matchNext)
		}
		if got := cost(); got.Transactions != 1 {
			t.Fatalf("slice %d opened %d snapshots, want 1", processed, got.Transactions)
		}
		if processed < len(ids) && w.matches[processed] != ids[processed] {
			t.Fatalf("pending match released early at %d", processed)
		}
	}
	if w.state.out.Total != int64(len(ids)) {
		t.Fatalf("sliced window total %d", w.state.out.Total)
	}
	year := w.state.aggregate.fields["year"]
	if year == nil || len(year.values) != len(ids) {
		t.Fatalf("sliced window double counted: %+v", year)
	}
}

func TestBulkPreviewWalkIgnoresCandidatesOutsideEmittedWindow(t *testing.T) {
	s, ids := bulkPreviewWalkWindowMovies(t, 2)
	w := bulkPreviewWalkWindowComputation(t, s, ids, previewAuthorize)
	// The reducer is bounded by the walker's fixed array, not by a fresh scan.
	if len(w.matches) != 128 {
		t.Fatalf("match window size changed: %d", len(w.matches))
	}
	// A sentinel past the emitted count cannot be a real entity, so reading it
	// would fail the single-target page guard.
	w.matches[2] = -1
	if err := w.drainMatchWindowUntil(context.Background(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w.matchNext != 2 || w.state.out.Total != 2 {
		t.Fatalf("drained past the emitted window: next=%d total=%d", w.matchNext, w.state.out.Total)
	}
	if w.matches[2] != -1 {
		t.Fatalf("candidate outside the emitted window was touched: %d", w.matches[2])
	}
}

func TestBulkPreviewWalkRefusesNonSingleTargetPage(t *testing.T) {
	s, ids := bulkPreviewWalkWindowMovies(t, 1)
	w := bulkPreviewWalkWindowComputation(t, s, ids, previewAuthorize)
	w.matches[0] = 1 << 62
	err := w.drainMatchWindowUntil(context.Background(), time.Now().Add(time.Minute))
	if !errors.Is(err, catalog.ErrStaleContinuation) {
		t.Fatalf("non-single page was accepted: %v", err)
	}
	if w.matchNext != 0 || w.state.out.Total != 0 || w.state.done || w.matches[0] != 1<<62 {
		t.Fatalf("refused page moved reducer state: next=%d total=%d done=%v match=%d", w.matchNext, w.state.out.Total, w.state.done, w.matches[0])
	}
}

func TestBulkPreviewWalkPostBatchRevocationDeliversNothing(t *testing.T) {
	s, ids := bulkPreviewWalkWindowMovies(t, 2)
	calls := 0
	authorize := func(context.Context, *sql.Tx) error {
		calls++
		// Two authorize calls per complete basis read, so the fourth call is the
		// post-batch fence: every page below has already been staged.
		if calls == 4 {
			return identity.ErrForbidden
		}
		return nil
	}
	w := bulkPreviewWalkWindowComputation(t, s, ids, authorize)
	err := w.drainMatchWindowUntil(context.Background(), time.Now().Add(time.Minute))
	if err == nil || !errors.Is(bulkPreviewPreparationError(err), readprep.ErrStale) {
		t.Fatalf("late revocation was not a stale refusal: %v", err)
	}
	if calls != 4 {
		t.Fatalf("revocation landed at call %d, not the post-batch fence", calls)
	}
	if w.state.out.Total != int64(len(ids)) {
		t.Fatalf("refusal was not post-batch: staged total=%d", w.state.out.Total)
	}
	// Nothing is published: no fields, no done state, no window progress. The
	// staged pages die with this computation because readprep terminalises any
	// step error instead of delivering a payload.
	if w.state.done || len(w.state.out.Fields) != 0 || w.matchNext != 0 || w.matches[0] != ids[0] {
		t.Fatalf("refused batch published or advanced: done=%v fields=%d next=%d", w.state.done, len(w.state.out.Fields), w.matchNext)
	}
}

func TestBulkPreviewWalkSingleTargetResultMatchesStreamingPath(t *testing.T) {
	s, ids := bulkPreviewWalkWindowMovies(t, 1)
	w := bulkPreviewWalkWindowComputation(t, s, ids, previewAuthorize)
	if err := w.drainMatchWindowUntil(context.Background(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The batched walk finishes through the same exhausted reducer step as the
	// streaming path does.
	w.state.exhausted = true
	if err := w.state.step(dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia)); err != nil {
		t.Fatal(err)
	}
	if !w.state.done {
		t.Fatal("window path did not finish")
	}

	source := bulkPreviewWalkWindowSource()
	streamed, err := s.BulkPreview(context.Background(), func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		page, err := source(ctx, tx, after)
		if err != nil {
			return page, err
		}
		page.Selection = catalog.BulkMetadataSelection{SQL: "SELECT ? AS id", Args: []any{ids[0]}, Revision: "bulk-window"}
		page.Done = true
		return page, nil
	}, previewAuthorize)
	if err != nil {
		t.Fatal(err)
	}

	batch, err := json.Marshal(w.state.out)
	if err != nil {
		t.Fatal(err)
	}
	single, err := json.Marshal(streamed)
	if err != nil {
		t.Fatal(err)
	}
	if string(batch) != string(single) {
		t.Fatalf("single-target result changed:\nwindow:    %s\nstreaming: %s", batch, single)
	}
	if w.state.out.Total != 1 || w.state.out.SelectionRevision != "bulk-window" {
		t.Fatalf("single-target window result %+v", w.state.out)
	}
}

func TestBulkPreviewWalkReservesRetainedChargeBeforeRetention(t *testing.T) {
	s, ids := bulkPreviewWalkWindowMovies(t, 3)
	w := bulkPreviewWalkWindowComputation(t, s, ids, previewAuthorize)
	var chargedAt []int64
	var reserved int64
	w.state.aggregate.reserve = func(n int64) error {
		chargedAt = append(chargedAt, w.state.out.Total)
		reserved = max(reserved, n)
		return nil
	}
	if err := w.drainMatchWindowUntil(context.Background(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(chargedAt) == 0 || w.state.aggregate.used <= 0 {
		t.Fatalf("window retained values without a charge: used=%d calls=%d", w.state.aggregate.used, len(chargedAt))
	}
	seen := map[int64]bool{}
	for _, pages := range chargedAt {
		if pages >= int64(len(ids)) {
			t.Fatalf("charge observed after the final retention: pages=%d", pages)
		}
		seen[pages] = true
	}
	for page := int64(0); page < int64(len(ids)); page++ {
		if !seen[page] {
			t.Fatalf("page %d was retained without a prior charge: %v", page, chargedAt)
		}
	}
	if reserved < w.state.aggregate.used {
		t.Fatalf("reserved %d below retained %d", reserved, w.state.aggregate.used)
	}
}

func TestBulkPreviewWalkCancellationCommitsNothing(t *testing.T) {
	s, ids := bulkPreviewWalkWindowMovies(t, 2)
	w := bulkPreviewWalkWindowComputation(t, s, ids, previewAuthorize)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.state.aggregate.reserve = func(int64) error {
		cancel()
		return nil
	}
	err := w.drainMatchWindowUntil(ctx, time.Now().Add(time.Minute))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation did not surface: %v", err)
	}
	if w.matchNext != 0 || w.state.done || w.matches[0] != ids[0] {
		t.Fatalf("canceled slice moved reducer state: next=%d done=%v", w.matchNext, w.state.done)
	}
}
