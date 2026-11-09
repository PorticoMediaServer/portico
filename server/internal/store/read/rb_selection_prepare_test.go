package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_SELECTION_PREPARE_JOURNAL
// The fixture deliberately creates equal folded title keys (A/a), prefix keys
// (a/aa/ab), preserved article variants, rejected rows and a missing duration
// metric, so a range, rank, anchor or aggregate can only be exact by walking
// the complete source once. Two hundred extra admitted rows push a far start
// past the legacy exact-count threshold.
func newRBSelectionSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	for id := int64(2001); id <= 2200; id++ {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,1)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age) VALUES(?,?,1,1,?,'','','','',0,?,?,0,NULL)`, id, public, fmt.Sprintf("Title %d", id), public, public)
	}
	f.exec(t, `UPDATE catalog_entities SET
		sort_key=CASE id%6 WHEN 0 THEN 'A' WHEN 1 THEN 'a' WHEN 2 THEN 'aa' WHEN 3 THEN 'ab' WHEN 4 THEN 'A' ELSE '' END,
		sort_key_keep_articles=CASE id%6 WHEN 0 THEN 'The A' WHEN 1 THEN 'the a' WHEN 2 THEN 'The Aa' WHEN 3 THEN 'The Ab' WHEN 4 THEN 'the A' ELSE '' END,
		duration_key=CASE WHEN id%11=0 THEN 0 ELSE CAST(id%17 AS REAL)+0.5 END
		WHERE library_id=1`)
	return f
}

// END_RB_SELECTION_PREPARE_JOURNAL

func rbSelectionFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_selection_prepare_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_SELECTION_PREPARE_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_SELECTION_PREPARE_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbSelectionConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	// Separate PHASE2 logical test profile. Prep 16MiB is included in non-LIVE
	// demand; M grows to fund it plus its 20% contingency. This arithmetic is
	// not a whole-server/2GB physical qualification.
	cfg.Foreground.AssignedBytes += 20 << 20
	cfg.Foreground.EffectiveBytes += 20 << 20
	cfg.Foreground.NonBrowseBytes += 16 << 20
	protected := cfg.Partitions[governor.Foreground][governor.MemoryBytes]
	cfg.Partitions[governor.Foreground][governor.MemoryBytes] += 80 << 20
	cfg.Partitions[governor.Foreground][governor.WALBytes] += 1 << 20
	cfg.Partitions[governor.Background] = governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: protected}
	return cfg
}

func newRBSelectionFixture(t *testing.T) (*browseFixture, *RBSelectionPrepareReader) {
	t.Helper()
	f := composeReadFixture(t, newRBSelectionSource(t), RBSelectionPrepareDefinitions(), rbSelectionConfig(), false, rbSelectionFixtureHash(t))
	r, err := NewRBSelectionPrepareReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rbSelectionRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 397, ID: "rb-selection-prepare-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}

func rbSelectionAdvanceAll(t *testing.T, job *RBSelectionPreparation) RBSelectionPrepareAdvance {
	t.Helper()
	var last RBSelectionPrepareAdvance
	for n := 0; n < 64; n++ {
		advance, err := job.Advance(t.Context(), rbSelectionRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		last.Restarts += advance.Restarts
		last.RawRows += advance.RawRows
		last.Statements += advance.Statements
		if advance.Ready {
			last.Ready = true
			return last
		}
	}
	t.Fatal("selection preparation did not finish")
	return last
}

func rbSelectionLookup(t *testing.T, f *browseFixture, job *RBSelectionPreparation, view BrowseInput, q RBSelectionQuery) (RBSelectionResult, BrowseReadResult, error) {
	t.Helper()
	live, request, output := liveRequests(fmt.Sprintf("rb-selection-lookup-%p-%d-%d", job, q.Start, q.Limit+len(q.SelectedIDs)))
	var result RBSelectionResult
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var err error
		result, cost, err = job.Lookup(ctx, request, time.Second, out, view, q)
		return err
	})
	if err == nil {
		if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
			t.Fatal("selection lookup retained LIVE custody", u)
		}
	}
	return result, cost, err
}

// rbSelectionBrowseIDs is the independent oracle: the existing merged browse
// reader over the same fixture. It performs its own complete cursor walk and
// applies the real admission/policy fence; it never shares this task's reducer.
func rbSelectionBrowseIDs(t *testing.T, f *browseFixture, keep, desc bool) []int64 {
	t.Helper()
	in := f.input
	in.KeepArticles, in.Descending, in.Limit, in.After = keep, desc, 40, nil
	var ids []int64
	for n := 0; n < 64; n++ {
		live, request, output := liveRequests(fmt.Sprintf("rb-selection-browse-%d-%v-%v-%d", n, keep, desc, len(ids)))
		var page BrowsePage
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
			var e error
			page, _, e = f.reader.window(ctx, request, time.Second, out, in)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if page.Exhausted {
			return ids
		}
		if page.Next == nil {
			t.Fatal("browse oracle cursor did not continue")
		}
		in.After = page.Next
	}
	t.Fatal("browse oracle did not finish")
	return nil
}

// rbSelectionOracle stores the oracle's ascending result and rank so SQL can
// independently answer anchor ranks, range membership and duration aggregates.
func rbSelectionOracle(t *testing.T, f *browseFixture, ascending []int64) {
	t.Helper()
	f.exec(t, `DROP TABLE IF EXISTS rb_selection_oracle`)
	f.exec(t, `CREATE TABLE rb_selection_oracle(id INTEGER PRIMARY KEY, asc_rank INTEGER NOT NULL)`)
	for rank, id := range ascending {
		f.exec(t, `INSERT INTO rb_selection_oracle(id,asc_rank) VALUES(?,?)`, id, int64(rank))
	}
}

func rbSelectionSQLDuration(t *testing.T, f *browseFixture, where string, args ...any) (int64, int64) {
	t.Helper()
	row := f.conn.QueryRowContext(t.Context(), `SELECT COALESCE(SUM(CASE WHEN e.duration_key>0 THEN CAST(round(e.duration_key*1000) AS INTEGER) ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.duration_key IS NULL OR e.duration_key<=0 THEN 1 ELSE 0 END),0) FROM rb_selection_oracle o JOIN catalog_entities e ON e.id=o.id WHERE `+where, args...)
	var ms, missing int64
	if err := row.Scan(&ms, &missing); err != nil {
		t.Fatal(err)
	}
	return ms, missing
}

func rbSelectionSorted(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// ascWindowStart/End map a requested-order range to the oracle's ascending rank
// window. A descending page is the mirrored contiguous window.
func ascWindowStart(start, count, total int, descending bool) int {
	if descending {
		return total - (start + count)
	}
	return start
}

func ascWindowEnd(start, count, total int, descending bool) int {
	if descending {
		return total - start
	}
	return start + count
}

func TestRBSelectionPrepareCompleteResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		admitted := rbSelectionBrowseIDs(t, f, false, false)
		for _, key := range []RBOrderKey{RBOrderByID, RBOrderByTitle} {
			for _, desc := range []bool{false, true} {
				for _, keep := range []bool{false, true} {
					if key == RBOrderByID && keep {
						continue
					}
					// ascending is always the true ascending order; want is the
					// requested page order (the full reverse for a descending read).
					var ascending []int64
					if key == RBOrderByID {
						ascending = rbSelectionSorted(admitted)
					} else {
						ascending = rbSelectionBrowseIDs(t, f, keep, false)
					}
					want := slices.Clone(ascending)
					if desc {
						slices.Reverse(want)
					}
					rbSelectionOracle(t, f, ascending)
					in := RBSelectionInput{View: f.input, Key: key}
					in.View.Descending, in.View.KeepArticles = desc, keep
					job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, in)
					if err != nil {
						t.Fatal(err)
					}
					done := rbSelectionAdvanceAll(t, job)
					if !done.Ready || done.Restarts != 0 {
						t.Fatalf("preparation did not finish cleanly: %+v", done)
					}
					n := len(want)
					var got []int64
					for start := 0; start < n; start += 40 {
						limit := min(40, n-start)
						result, cost, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: start, Limit: limit})
						if err != nil {
							t.Fatalf("range %d/%d key=%d desc=%v keep=%v: %v", start, limit, key, desc, keep, err)
						}
						if result.Start != start || result.Total != int64(n) {
							t.Fatalf("range identity key=%d desc=%v start=%d: %+v", key, desc, start, result)
						}
						if cost.PublicationStatements < 5 || result.Statements != cost.PublicationStatements {
							t.Fatal("missing fresh publication evidence", result, cost)
						}
						if len(result.Entries) != limit {
							t.Fatalf("range length key=%d desc=%v start=%d: %+v", key, desc, start, result)
						}
						for i, entry := range result.Entries {
							if entry.Rank != int64(start+i) {
								t.Fatalf("wrong rank key=%d desc=%v at %d: %+v", key, desc, i, entry)
							}
							got = append(got, entry.ID)
						}
						ms, missing := rbSelectionSQLDuration(t, f, `o.asc_rank>=? AND o.asc_rank<?`, int64(ascWindowStart(start, len(result.Entries), n, desc)), int64(ascWindowEnd(start, len(result.Entries), n, desc)))
						if result.DurationMs != ms || result.DurationIsLowerBound != (missing > 0) || result.DurationMissing != missing {
							t.Fatalf("range duration key=%d desc=%v start=%d: %+v want ms=%d missing=%d", key, desc, start, result, ms, missing)
						}
					}
					if !slices.Equal(got, want) {
						t.Fatalf("complete result key=%d desc=%v keep=%v differs from oracle: %v vs %v", key, desc, keep, got, want)
					}
					// A range starting at the exact end is an exact empty answer.
					empty, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: n, Limit: 1})
					if err != nil || len(empty.Entries) != 0 || empty.Total != int64(n) || !empty.Exhausted || empty.DurationMs != 0 {
						t.Fatalf("end range key=%d desc=%v: %+v %v", key, desc, empty, err)
					}
					if err := job.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	})
}

func TestRBSelectionPrepareFarRangeBeyondCountThreshold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		admitted := rbSelectionBrowseIDs(t, f, false, false)
		ascending := rbSelectionSorted(admitted)
		rbSelectionOracle(t, f, ascending)
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbSelectionAdvanceAll(t, job)
		start := 1000
		if len(ascending) <= start+40 {
			t.Fatalf("fixture is too small for a far range beyond the count threshold: %d", len(ascending))
		}
		result, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: start, Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		if result.Total != int64(len(ascending)) || result.Start != start || len(result.Entries) != 40 {
			t.Fatalf("far range: %+v", result)
		}
		for i := range result.Entries {
			if result.Entries[i].ID != ascending[start+i] || result.Entries[i].Rank != int64(start+i) {
				t.Fatalf("far range row %d: %+v want id=%d", i, result.Entries[i], ascending[start+i])
			}
		}
		ms, missing := rbSelectionSQLDuration(t, f, `o.asc_rank>=? AND o.asc_rank<?`, int64(start), int64(start+40))
		if result.DurationMs != ms || result.DurationIsLowerBound != (missing > 0) || result.DurationMissing != missing {
			t.Fatalf("far range duration: %+v want ms=%d missing=%d", result, ms, missing)
		}
	})
}

// TestRBSelectionPrepareStartPastEnd pins the legacy OFFSET shape: a range
// whose start is beyond the finished order is an exact empty page with the
// unchanged total, not ErrInput.
func TestRBSelectionPrepareStartPastEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		ascending := rbSelectionSorted(rbSelectionBrowseIDs(t, f, false, false))
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbSelectionAdvanceAll(t, job)
		n := len(ascending)
		for _, start := range []int{n + 1, n + 40, n + 1000} {
			result, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: start, Limit: 40})
			if err != nil {
				t.Fatalf("start %d beyond end was not an empty page: %v", start, err)
			}
			if result.Start != start || result.Total != int64(n) || len(result.Entries) != 0 || !result.Exhausted || result.DurationMs != 0 || result.AnchorFound {
				t.Fatalf("start %d beyond end: %+v", start, result)
			}
		}
		// The last real position still publishes exactly one row.
		last, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: n - 1, Limit: 40})
		if err != nil || len(last.Entries) != 1 || last.Entries[0].ID != ascending[n-1] || !last.Exhausted {
			t.Fatalf("last position: %+v %v", last, err)
		}
	})
}

// TestRBSelectionPrepareAgeRestrictedAdmission checks the completed walk and
// the independent browse oracle agree when a restricted profile hides
// over-age and unrated rows from the raw source.
func TestRBSelectionPrepareAgeRestrictedAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
		f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE library_id=1 AND id BETWEEN 2001 AND 2030`)
		f.exec(t, `UPDATE catalog_entities SET admission_own_age=10 WHERE library_id=1 AND id BETWEEN 2031 AND 2060`)
		// 2061..2200 keep their literal NULL age, which allow_unrated=0 rejects.
		admitted := rbSelectionBrowseIDs(t, f, false, false)
		rbSelectionOracle(t, f, rbSelectionSorted(admitted))
		if slices.Contains(admitted, int64(2001)) || !slices.Contains(admitted, int64(2031)) || slices.Contains(admitted, int64(2061)) {
			t.Fatalf("fixture did not realize age/null admission: over=%v rated=%v null=%v",
				slices.Contains(admitted, int64(2001)), slices.Contains(admitted, int64(2031)), slices.Contains(admitted, int64(2061)))
		}
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbSelectionAdvanceAll(t, job)
		var got []int64
		for start := 0; start < len(admitted); start += 200 {
			result, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: start, Limit: min(200, len(admitted)-start)})
			if err != nil {
				t.Fatal(err)
			}
			if result.Total != int64(len(admitted)) {
				t.Fatalf("restricted total at %d: %d want %d", start, result.Total, len(admitted))
			}
			for i, entry := range result.Entries {
				got = append(got, entry.ID)
				if entry.Rank != int64(start+i) {
					t.Fatalf("restricted rank %d: %+v", start, entry)
				}
			}
		}
		if !slices.Equal(got, rbSelectionSorted(admitted)) {
			t.Fatalf("restricted order differs from oracle: got %d want %d", len(got), len(admitted))
		}
	})
}

// TestRBSelectionPrepareDescendingSelectionRanks checks selection ranks, a
// range and an entity anchor in the descending direction against the oracle's
// ascending ranks.
func TestRBSelectionPrepareDescendingSelectionRanks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		for _, key := range []RBOrderKey{RBOrderByID, RBOrderByTitle} {
			// The browse oracle returns title order; the ID recipe's ascending
			// order is the same membership sorted by ID.
			ascending := rbSelectionBrowseIDs(t, f, false, false)
			if key == RBOrderByID {
				ascending = rbSelectionSorted(ascending)
			}
			total := len(ascending)
			rbSelectionOracle(t, f, ascending)
			in := RBSelectionInput{View: f.input, Key: key}
			in.View.Descending = true
			job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, in)
			if err != nil {
				t.Fatal(err)
			}
			rbSelectionAdvanceAll(t, job)
			at := []int{0, total / 3, total - 1}
			selected := []int64{ascending[at[0]], ascending[at[1]], ascending[at[2]], ascending[at[1]]}
			result, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: 0, Limit: 1, SelectedIDs: selected})
			if err != nil {
				t.Fatalf("descending selection key=%d: %v", key, err)
			}
			if len(result.Selection) != 3 {
				t.Fatalf("descending selection dedup key=%d: %+v", key, result.Selection)
			}
			for i, a := range at {
				if result.Selection[i].ID != ascending[a] || result.Selection[i].Rank != int64(total-1-a) {
					t.Fatalf("descending selection key=%d row %d: %+v want id=%d rank=%d", key, i, result.Selection[i], ascending[a], total-1-a)
				}
			}
			ms, missing := rbSelectionSQLDuration(t, f, `o.id IN (?,?,?)`, ascending[at[0]], ascending[at[1]], ascending[at[2]])
			if result.SelectionDurationMs != ms || result.SelectionDurationIsLowerBound != (missing > 0) {
				t.Fatalf("descending selection duration key=%d: %+v want ms=%d missing=%d", key, result, ms, missing)
			}
			// A descending range and entity anchor mirror the ascending ranks.
			start := total / 2
			page, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: start, Limit: 3})
			if err != nil || len(page.Entries) != 3 {
				t.Fatalf("descending range key=%d: %+v %v", key, page, err)
			}
			for i, entry := range page.Entries {
				if entry.ID != ascending[total-1-(start+i)] || entry.Rank != int64(start+i) {
					t.Fatalf("descending range key=%d row %d: %+v", key, i, entry)
				}
			}
			anchorAt := total / 4
			anchor, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: 0, Limit: 1, AnchorID: ascending[anchorAt]})
			if err != nil || !anchor.AnchorFound || anchor.AnchorRank != int64(total-1-anchorAt) {
				t.Fatalf("descending anchor key=%d: %+v %v want %d", key, anchor, err, total-1-anchorAt)
			}
			if err := job.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestRBSelectionPrepareAnchorsBothDirectionsAndEqualKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		ascending := rbSelectionBrowseIDs(t, f, false, false)
		total := len(ascending)
		rbSelectionOracle(t, f, ascending)
		for _, desc := range []bool{false, true} {
			in := RBSelectionInput{View: f.input, Key: RBOrderByTitle}
			in.View.Descending = desc
			job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, in)
			if err != nil {
				t.Fatal(err)
			}
			rbSelectionAdvanceAll(t, job)
			for _, at := range []int{0, total / 2, total - 1} {
				id := ascending[at]
				want := int64(at)
				if desc {
					want = int64(total - 1 - at)
				}
				result, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: 0, Limit: 1, AnchorID: id})
				if err != nil || !result.AnchorFound || result.AnchorRank != want {
					t.Fatalf("entity anchor desc=%v id=%d: %+v %v want %d", desc, id, result, err, want)
				}
			}
			for _, anchor := range []string{"A", "a", "aa", "ab", "zz", "0"} {
				var count int64
				comparison := "<"
				if desc {
					comparison = "<="
				}
				if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM rb_selection_oracle o JOIN catalog_entities e ON e.id=o.id WHERE e.sort_key COLLATE NOCASE `+comparison+` ?`, anchor).Scan(&count); err != nil {
					t.Fatal(err)
				}
				var want int64
				found := true
				if desc {
					want = int64(total) - count
					found = count > 0
				} else {
					want = count
					found = count < int64(total)
				}
				result, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: 0, Limit: 1, AnchorKey: anchor, AnchorKeySet: true})
				if err != nil {
					t.Fatalf("key anchor desc=%v %q: %v", desc, anchor, err)
				}
				if result.AnchorFound != found || found && result.AnchorRank != want {
					t.Fatalf("key anchor desc=%v %q: %+v want found=%v rank=%d", desc, anchor, result, found, want)
				}
			}
			// Equal folded keys (A vs a) resolve to the same run beginning.
			first, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: 0, Limit: 1, AnchorKey: "A", AnchorKeySet: true})
			if err != nil {
				t.Fatal(err)
			}
			second, _, err := rbSelectionLookup(t, f, job, in.View, RBSelectionQuery{Start: 0, Limit: 1, AnchorKey: "a", AnchorKeySet: true})
			if err != nil || !first.AnchorFound || !second.AnchorFound || first.AnchorRank != second.AnchorRank {
				t.Fatalf("equal folded anchors desc=%v: %+v %+v %v", desc, first, second, err)
			}
			if err := job.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestRBSelectionPrepareSelectionAndOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		ascending := rbSelectionBrowseIDs(t, f, false, false)
		rbSelectionOracle(t, f, ascending)
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByTitle})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbSelectionAdvanceAll(t, job)
		total := len(ascending)
		selected := []int64{ascending[0], ascending[total/2], ascending[total-1], ascending[total/2], 999999}
		result, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 1, SelectedIDs: selected})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Selection) != 3 || !slices.Equal(result.SelectionMissing, []int64{999999}) {
			t.Fatalf("selection membership: %+v", result)
		}
		for i, at := range []int{0, total / 2, total - 1} {
			if result.Selection[i].ID != ascending[at] || result.Selection[i].Rank != int64(at) {
				t.Fatalf("selection row %d: %+v want id=%d rank=%d", i, result.Selection[i], ascending[at], at)
			}
		}
		ms, missing := rbSelectionSQLDuration(t, f, `o.id IN (?,?,?)`, ascending[0], ascending[total/2], ascending[total-1])
		if result.SelectionDurationMs != ms || result.SelectionDurationIsLowerBound != (missing > 0) {
			t.Fatalf("selection duration: %+v want ms=%d missing=%d", result, ms, missing)
		}
		// An over-large set is refused before any copy or delivery; the finished
		// preparation is not consumed by the refusal.
		overflow := make([]int64, rbSelectionMaxIDs+1)
		for i := range overflow {
			overflow[i] = int64(i + 1)
		}
		bad, band, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 1, SelectedIDs: overflow})
		if !errors.Is(err, ErrInput) || len(bad.Entries) != 0 || len(bad.Selection) != 0 || band.PublicationStatements != 0 {
			t.Fatalf("selection overflow was not refused before copy: %+v %+v %v", bad, band, err)
		}
		again, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 1})
		if err != nil || len(again.Entries) != 1 {
			t.Fatalf("preparation did not survive selection overflow: %+v %v", again, err)
		}
	})
}

func TestRBSelectionPrepareSparseRestrictionsAndRawContinuation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE library_id=1 AND id BETWEEN 200 AND 260`)
		admitted := rbSelectionBrowseIDs(t, f, false, false)
		rbSelectionOracle(t, f, rbSelectionSorted(admitted))
		var rawRows int64
		if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_entities WHERE library_id=1 AND kind=1`).Scan(&rawRows); err != nil {
			t.Fatal(err)
		}
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		done := rbSelectionAdvanceAll(t, job)
		if !done.Ready || int64(done.RawRows) < rawRows {
			t.Fatalf("rejected rows did not remain in the raw stream: %+v want >=%d", done, rawRows)
		}
		result, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 40})
		if err != nil || result.Total != int64(len(admitted)) {
			t.Fatalf("sparse restriction total: %+v %v want %d", result, err, len(admitted))
		}
		sorted := rbSelectionSorted(admitted)
		for i := range result.Entries {
			if result.Entries[i].ID != sorted[i] {
				t.Fatalf("sparse restriction row %d: %+v", i, result.Entries[i])
			}
		}
	})
}

func TestRBSelectionPrepareSourceContentRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		if _, err := job.Advance(t.Context(), rbSelectionRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		original := job.entry.lease.Deadline()
		bytes := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		done := rbSelectionAdvanceAll(t, job)
		if !done.Ready || done.Restarts != 1 {
			t.Fatalf("content invalidation did not restart exactly once: %+v", done)
		}
		if !job.entry.lease.Deadline().Equal(original) || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != bytes {
			t.Fatal("restart renewed the deadline or re-admitted physical bytes")
		}
		want := rbSelectionSorted(rbSelectionBrowseIDs(t, f, false, false))
		result, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 40})
		if err != nil || result.Total != int64(len(want)) {
			t.Fatalf("restart result: %+v %v", result, err)
		}
		for i := range result.Entries {
			if result.Entries[i].ID != want[i] {
				t.Fatalf("restart published mixed content at %d: %+v", i, result.Entries[i])
			}
		}
	})
}

func TestRBSelectionPrepareDriftTimeAnchorAndDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		// Source/content drift after the complete walk is a stale token, never a
		// silently re-walked range.
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		rbSelectionAdvanceAll(t, job)
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
		if _, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 40}); !errors.Is(err, ErrStale) {
			t.Fatal("range accepted a drifted source fence", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("drifted lookup retained values or bytes")
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		// A time anchor past the token's horizon denies before any copy.
		job, err = r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		rbSelectionAdvanceAll(t, job)
		late := f.input
		late.Now = late.Now.Add(48 * time.Hour)
		result, _, err := rbSelectionLookup(t, f, job, late, RBSelectionQuery{Start: 0, Limit: 40})
		if !errors.Is(err, ErrDenied) || len(result.Entries) != 0 {
			t.Fatalf("expired time anchor: %+v %v", result, err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied lookup retained values or bytes")
		}
	})
}

func rbSelectionWriter(class governor.Class, now time.Time) governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 395, ID: "rb-selection-publication"}, Owner: 2, Class: class, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: now.Add(time.Minute), Budget: governor.UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Need: governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}, Footprint: []governor.Scope{{Library: 1}}}
}

func TestRBSelectionPrepareFirstHandoffAndForegroundDenial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbSelectionAdvanceAll(t, job)
		if d := f.governor.Try(rbSelectionWriter(governor.Background, time.Now())); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.Conflict {
			t.Fatal("ready preparation released the parked writer before first handoff", d)
		}
		first, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 40})
		if err != nil || len(first.Entries) != 40 || first.Exhausted {
			t.Fatal("first handoff", first, err)
		}
		d := f.governor.Try(rbSelectionWriter(governor.Background, time.Now()))
		if d.Grant == nil {
			t.Fatal("publication did not resume after first handoff", d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		second, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 40, Limit: 40})
		if err != nil || len(second.Entries) != 40 || second.Entries[0].Rank != 40 || second.Entries[0].ID <= first.Entries[len(first.Entries)-1].ID {
			t.Fatal("later handoff replayed or skipped values", second, err)
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}

		// A foreground edit after a computing poll is a denial, not a restart.
		job2, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job2.Advance(t.Context(), rbSelectionRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		edit := f.governor.Try(rbSelectionWriter(governor.Foreground, time.Now()))
		if edit.Grant == nil {
			t.Fatal(edit.Refusal)
		}
		if err := f.authority.Retired(edit.Grant); err != nil {
			t.Fatal(err)
		}
		if _, err := job2.Advance(t.Context(), rbSelectionRequest(), time.Second); !errors.Is(err, governor.ErrPreparationStale) {
			t.Fatal("security change restarted instead of denying", err)
		}
		if !job2.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied preparation retained values or bytes")
		}
	})
}

func TestRBSelectionPrepareOriginalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 1<<20, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbSelectionRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(governor.PreparationLifetime + time.Second)
		if _, err := job.Advance(t.Context(), rbSelectionRequest(), time.Second); !errors.Is(err, governor.ErrPreparationExpired) {
			t.Fatal("preparation outlived the original deadline", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("expired preparation retained values or bytes")
		}
	})
}

func TestRBSelectionPrepareCapacityBeforeAppend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBSelectionFixture(t)
		job, err := r.BeginRBSelectionPrepare(t.Context(), 1, 8192, RBSelectionInput{View: f.input, Key: RBOrderByID})
		if err != nil {
			t.Fatal(err)
		}
		_, err = job.Advance(t.Context(), rbSelectionRequest(), time.Second)
		if !errors.Is(err, errRBSelectionCapacity) {
			t.Fatalf("undersized admission did not refuse: %v", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("capacity refusal retained a partial set or bytes")
		}
		if _, _, err := rbSelectionLookup(t, f, job, f.input, RBSelectionQuery{Start: 0, Limit: 1}); err == nil {
			t.Fatal("closed preparation still answered a lookup")
		}
	})
}

func TestRBSelectionPrepareSameSnapshotPreflightRefusal(t *testing.T) {
	p := &RBSelectionPreparation{view: BrowseInput{Library: 1, Kind: 1}, key: RBOrderByTitle, copyLimit: 1 << 20, nativeLimit: 8 << 20, entry: &rbSelectionEntry{fence: browseFence{profile: "p"}}}
	if p.after != nil {
		t.Fatal("unexpected cursor")
	}
	var calls []string
	query := func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		if !strings.HasSuffix(name, ".bytes") {
			t.Fatal("payload requested before the byte preflight")
		}
		return [][]any{{int64(1), int64(1), int64(3 << 20)}}, nil
	}
	rows, _, err := p.readTitleRows(query, p.view)
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("octet preflight did not refuse before payload", rows, err, calls)
	}
	calls = nil
	p.copied = 0
	query = func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		return [][]any{{int64(1), int64(1), int64(9 << 20)}}, nil
	}
	rows, _, err = p.readTitleRows(query, p.view)
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("native row bound was not enforced", rows, err, calls)
	}
}

func TestRBSelectionPrepareDurationConversion(t *testing.T) {
	for _, test := range []struct {
		value  any
		ms     int64
		known  bool
		failed bool
	}{
		{nil, 0, false, false},
		{float64(0), 0, false, false},
		{float64(-1), 0, false, false},
		{float64(12.5), 12500, true, false},
		{int64(3), 3000, true, false},
		{"bad", 0, false, true},
	} {
		ms, known, err := rbSelectionDurationMs(test.value)
		if ms != test.ms || known != test.known || (err != nil) != test.failed {
			t.Fatalf("duration %v: ms=%d known=%v err=%v", test.value, ms, known, err)
		}
	}
}
