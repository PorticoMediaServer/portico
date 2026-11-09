package read

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	return rbAcrossOracleLibraries(t, f, []int64{1, 2}, sort, desc, keep)
}

func rbAcrossOracleLibraries(t *testing.T, f *browseFixture, libraries []int64, sort RBAcrossSort, desc, keep bool) []int64 {
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
	args := make([]any, len(libraries))
	for i, library := range libraries {
		args[i] = library
	}
	in := "IN(" + strings.TrimSuffix(strings.Repeat("?,", len(libraries)), ",") + ")"
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id FROM catalog_entities WHERE library_id `+in+` AND kind=2 AND retired=0 ORDER BY `+column+collate+` `+direction+`,id `+direction, args...)
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

// rbAcrossSeedTitles inserts count kind-2 entities into one library, keyed
// a00, a01, ... (or prefix numeric) so the global title order is predictable.
func rbAcrossSeedTitles(t *testing.T, f *browseFixture, library, firstID int64, prefix string, count int) {
	t.Helper()
	for n := 0; n < count; n++ {
		id := firstID + int64(n)
		key := fmt.Sprintf("%s%02d", prefix, n)
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,2)`, id, public, library)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,year,added_at,last_content_added_at,retired,duration_key,admission_own_age,metadata_incarnation,artwork_incarnation) VALUES(?,?,?,2,?,?,?,?,?,?,?,?,?,?,?,NULL,?,?)`,
			id, public, library, fmt.Sprintf("Show %d", id), key, key, key, key, id, 2000+n, "2026-01-01T00:00:00.000000000Z", "2026-01-01T00:00:00.000000000Z", 0, 10.0, public, public)
	}
}

// rbAcrossWalkPrefix walks every page and asserts each page is an exact prefix
// of the independent plain-SQL global order. It also holds the bounded-window,
// statement and forward-progress invariants across the whole walk.
func rbAcrossWalkPrefix(t *testing.T, f *browseFixture, in RBAcrossInput, want []int64) []int64 {
	t.Helper()
	got := []int64{}
	for n := 0; n < 100; n++ {
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
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		if len(got) > len(want) || !slices.Equal(got, want[:len(got)]) {
			t.Fatalf("page %d is not an exact prefix: got %v want-prefix %v", n, got, want[:min(len(got), len(want))])
		}
		if page.Exhausted {
			if page.Next != nil {
				t.Fatal("exhausted cursor")
			}
			if !slices.Equal(got, want) {
				t.Fatalf("merge lost or duplicated rows: got %v want %v", got, want)
			}
			return got
		}
		if page.Next == nil {
			t.Fatal("lost continuation")
		}
		// Forward progress: a non-exhausted page consumed at least one position.
		if page.RawPositions == 0 {
			t.Fatalf("page %d emitted no position while data remains: %+v", n, page)
		}
		in.After = page.Next
	}
	t.Fatal("did not terminate")
	return nil
}

type rbAcrossSeed struct {
	library, firstID int64
	prefix           string
	count            int
}

// TestRBAcrossMergeOrder walks every page across a full limit-40 boundary and
// holds each page to the independent plain-SQL global order. Two cases cover
// the reviewer's drain repro (two libraries, 45 rows each, more than one
// per-library fetch) and four-library heavy ties (per=15), where a buffered
// head must never be published while another library's buffer is drained.
func TestRBAcrossMergeOrder(t *testing.T) {
	cases := []struct {
		name      string
		share     string
		libraries []int64
		seeds     []rbAcrossSeed
		minimum   int
	}{
		{
			name:      "two libraries drain",
			share:     `["movies","other"]`,
			libraries: []int64{1, 2},
			seeds:     []rbAcrossSeed{{1, 4001, "a", 45}, {2, 5001, "b", 45}},
			minimum:   90,
		},
		{
			name:      "four library ties",
			share:     `["movies","other","books","reading"]`,
			libraries: []int64{1, 2, 5, 6},
			seeds:     []rbAcrossSeed{{1, 6001, "tie", 20}, {2, 7001, "tie", 20}, {5, 8001, "tie", 20}, {6, 9001, "tie", 20}},
			minimum:   80,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRBAcrossFixture(t)
			f.exec(t, `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='a'`, tc.share)
			for _, seed := range tc.seeds {
				rbAcrossSeedTitles(t, f, seed.library, seed.firstID, seed.prefix, seed.count)
			}
			want := rbAcrossOracleLibraries(t, f, tc.libraries, RBAcrossTitle, false, false)
			if len(want) < tc.minimum {
				t.Fatalf("fixture too small: %d", len(want))
			}
			in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
			in.View.Kind, in.View.Limit = 2, MaxPage
			got := rbAcrossWalkPrefix(t, f, in, want)
			if !slices.Equal(got, want) {
				t.Fatalf("merge order: got %v want %v", got, want)
			}
		})
	}
}

// TestRBAcrossCursorRefusedAcrossAgePolicyPrincipal replays a cursor under a
// second session for the same account/profile and the identical library share,
// after an age-policy change. The directory witness (profile + scope digest) is
// unchanged, so only the full principal fence can tell this principal apart.
func TestRBAcrossCursorRefusedAcrossAgePolicyPrincipal(t *testing.T) {
	f := newRBAcrossFixture(t)
	in := RBAcrossInput{View: f.input, Sort: RBAcrossTitle}
	in.View.Kind, in.View.Limit = 2, 2
	first, _, err := f.across(t, in)
	if err != nil || first.Next == nil {
		t.Fatal(first, err)
	}
	f.exec(t, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('e2','a','i2','Second','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`)
	f.exec(t, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) SELECT 'fc','s','a','p','local','member',1,'2026-11-07T00:00:00Z',1,'e2',m.incarnation,p.lifetime,x'00800000000000000000000000000003',0 FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='a' AND p.id='p'`)
	f.exec(t, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('tokenc','fc',1,'2026-11-07T00:00:00Z')`)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	replay := in
	replay.After = first.Next
	replay.View.TokenHash = "tokenc"
	if got, _, err := f.across(t, replay); !errors.Is(err, ErrStale) || len(got.Items) != 0 {
		t.Fatal("age-policy principal replay", got, err)
	}
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
