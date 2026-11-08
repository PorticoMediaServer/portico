package read

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func newRBAcrossFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, newRBDirectorySource(t), RBAcrossDefinitions(), rbDirectoryConfig(), true, rbDirectoryFixtureHash(t))
}

func (f *browseFixture) across(t *testing.T, in RBAcrossInput) (RBAcrossPage, BrowseReadResult, error) {
	t.Helper()
	request := governor.Request{Key: governor.JobKey{Domain: 96, ID: "across"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	return f.reader.acrossWindow(t.Context(), request, time.Second, f.output, in)
}

// rbAcrossOracle is the independent plain-SQL order over the same fixture: it
// never calls the reader's merge or comparator.
func rbAcrossOracle(t *testing.T, f *browseFixture, sort RBAcrossSort, desc, keep bool) []int64 {
	t.Helper()
	column, collate := "sort_key", " COLLATE NOCASE"
	switch sort {
	case RBAcrossTitle:
		if keep {
			column = "sort_key_keep_articles"
		}
	case RBAcrossAdded:
		column, collate = "added_at", ""
	case RBAcrossContentAdded:
		column, collate = "last_content_added_at", ""
	case RBAcrossSort(SortYear):
		column, collate = "year", ""
	case RBAcrossSort(SortDuration):
		column, collate = "duration_key", ""
	default:
		t.Fatalf("oracle has no source for %s", sort)
	}
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id FROM catalog_entities WHERE library_id IN(1,2) AND kind=2 AND retired=0 ORDER BY `+column+collate+` `+direction+`,id `+direction)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func rbAcrossCollect(t *testing.T, f *browseFixture, in RBAcrossInput) ([]int64, int) {
	t.Helper()
	var got []int64
	maxRaw := 0
	for n := 0; n < 200; n++ {
		page, cost, err := f.across(t, in)
		if err != nil {
			t.Fatal(err)
		}
		if page.RawRows > MaxRaw || page.RawPositions > MaxRaw || len(page.Items) > in.View.Limit {
			t.Fatalf("unbounded window: %+v", page)
		}
		if cost.LookupStatements > 16 || cost.PublicationStatements > 5 || page.Statements > 24 {
			t.Fatalf("window cost: %+v %+v", page, cost)
		}
		if page.RawRows > maxRaw {
			maxRaw = page.RawRows
		}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		if page.Exhausted {
			if page.Next != nil {
				t.Fatal("exhausted cursor")
			}
			return got, maxRaw
		}
		if page.Next == nil {
			t.Fatal("lost continuation")
		}
		in.After = page.Next
	}
	t.Fatal("did not terminate")
	return nil, 0
}

func TestRBAcrossExactSameSortMerge(t *testing.T) {
	f := newRBAcrossFixture(t)
	for _, sort := range []RBAcrossSort{RBAcrossTitle, RBAcrossAdded, RBAcrossContentAdded, RBAcrossSort(SortYear), RBAcrossSort(SortDuration)} {
		for _, desc := range []bool{false, true} {
			for _, keep := range []bool{false, true} {
				if keep && sort != RBAcrossTitle {
					continue
				}
				want := rbAcrossOracle(t, f, sort, desc, keep)
				in := RBAcrossInput{View: f.input, Sort: sort}
				in.View.Kind, in.View.Descending, in.View.KeepArticles, in.View.Limit = 2, desc, keep, 3
				got, _ := rbAcrossCollect(t, f, in)
				if !slices.Equal(got, want) {
					t.Fatalf("merge sort=%s desc=%v keep=%v got=%v want=%v", sort, desc, keep, got, want)
				}
			}
		}
	}
}

func TestRBAcrossEqualKeysAcrossLibraries(t *testing.T) {
	f := newRBAcrossFixture(t)
	in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
	in.View.Kind, in.View.Limit = 2, 3
	got, _ := rbAcrossCollect(t, f, in)
	if want := []int64{2001, 2002, 2003, 2004, 2005, 2007, 2008}; !slices.Equal(got, want) {
		t.Fatalf("folded title ties: got %v want %v", got, want)
	}
	in.View.KeepArticles = true
	in.After = nil
	got, _ = rbAcrossCollect(t, f, in)
	if want := []int64{2002, 2003, 2004, 2005, 2001, 2007, 2008}; !slices.Equal(got, want) {
		t.Fatalf("article-preserving ties: got %v want %v", got, want)
	}
}

func TestRBAcrossNoLibraryViewer(t *testing.T) {
	f := newRBAcrossFixture(t)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='a'`)
	in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
	in.View.Kind, in.View.Limit = 2, 3
	page, _, err := f.across(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || !page.Exhausted || page.Pending || page.Next != nil {
		t.Fatalf("no-library cross page: %+v", page)
	}
}

func TestRBAcrossCursorScopeDrift(t *testing.T) {
	f := newRBAcrossFixture(t)
	in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
	in.View.Kind, in.View.Limit = 2, 2
	page, _, err := f.across(t, in)
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	// Revoking one library changes the principal share fence; denial wins and
	// the old cursor yields no bytes.
	revoked := in
	revoked.After = page.Next
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id='a'`)
	if got, _, err := f.across(t, revoked); !errors.Is(err, ErrStale) || len(got.Items) != 0 {
		t.Fatal("revoked share continuation", got, err)
	}
	// Sort and page-size drift are continuation errors, not new pages.
	f2 := newRBAcrossFixture(t)
	firstIn := RBAcrossInput{View: f2.input, Sort: RBAcrossTitle}
	firstIn.View.Kind, firstIn.View.Limit = 2, 2
	first, _, err := f2.across(t, firstIn)
	if err != nil || first.Next == nil {
		t.Fatal(first, err)
	}
	changed := firstIn
	changed.After = first.Next
	changed.Sort = RBAcrossAdded
	if got, _, err := f2.across(t, changed); !errors.Is(err, ErrStale) || len(got.Items) != 0 {
		t.Fatal("sort drift", got, err)
	}
	resized := firstIn
	resized.After = first.Next
	resized.View.Limit = 3
	if got, _, err := f2.across(t, resized); !errors.Is(err, ErrStale) || len(got.Items) != 0 {
		t.Fatal("limit drift", got, err)
	}
	foreign := firstIn
	foreign.After = first.Next
	foreign.View.TokenHash = "tokenb"
	if got, _, err := f2.across(t, foreign); !errors.Is(err, ErrStale) || len(got.Items) != 0 {
		t.Fatal("cursor crossed principals", got, err)
	}
}

func TestRBAcrossRetiredLibraryDrift(t *testing.T) {
	f := newRBAcrossFixture(t)
	in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
	in.View.Kind, in.View.Limit = 2, 2
	page, _, err := f.across(t, in)
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	f.exec(t, `UPDATE catalog_libraries SET retired=1 WHERE id=2`)
	in.After = page.Next
	if got, _, err := f.across(t, in); !errors.Is(err, ErrStale) || len(got.Items) != 0 {
		t.Fatal("retired mapping continuation", got, err)
	}
}

func TestRBAcrossRefusesAfterCancellation(t *testing.T) {
	f := newRBAcrossFixture(t)
	request := governor.Request{Key: governor.JobKey{Domain: 96, ID: "across"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
	in.View.Kind, in.View.Limit = 2, 3
	page, _, err := f.reader.acrossWindow(ctx, request, time.Second, f.output, in)
	if err == nil || len(page.Items) != 0 || page.Statements != 0 {
		t.Fatal("cancelled merge returned bytes", page, err)
	}
}

func TestRBAcrossPublicLIVE(t *testing.T) {
	f := composeReadFixture(t, newRBDirectorySource(t), RBAcrossDefinitions(), rbDirectoryConfig(), false, rbDirectoryFixtureHash(t))
	live, request, output := liveRequests("across")
	in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
	in.View.Kind, in.View.Limit = 2, 3
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := f.reader.AcrossWindow(ctx, request, time.Second, out, in)
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			t.Fatal(page)
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}
