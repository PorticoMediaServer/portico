package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

// BEGIN_RB_NUMERIC_CONTEXT_JOURNAL
func newRBNumericContextSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET
 year=CASE WHEN id%4=0 THEN 0 ELSE 2000+id%3 END,
 release_date=CASE WHEN id%5=0 THEN '' WHEN id%5=1 THEN '1999-12-31' ELSE '2000-01-02' END,
 release_key=CASE WHEN id%5=0 THEN '' WHEN id%5=1 THEN '1999-12-31T00:00:00Z' ELSE '2000-01-02T00:00:00Z' END,
 added_at=CASE WHEN id%6=0 THEN NULL ELSE '2026-11-01T00:00:00.000000000Z' END,
 community_rating_key=CASE WHEN id%7=0 THEN 0 ELSE 6.5+id%3 END,
 critic_rating_key=CASE WHEN id%8=0 THEN 0 ELSE 5.0+id%3 END`)
	return f
}

// END_RB_NUMERIC_CONTEXT_JOURNAL
func rbNumericContextFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_numeric_context_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_NUMERIC_CONTEXT_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_NUMERIC_CONTEXT_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbNumericContextConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	// Separate PHASE2 logical test profile, exactly as the merged numeric facet
	// fixtures use. It is not a whole-server/2GB physical qualification.
	cfg.Foreground.AssignedBytes += 20 << 20
	cfg.Foreground.EffectiveBytes += 20 << 20
	cfg.Foreground.NonBrowseBytes += 16 << 20
	protected := cfg.Partitions[governor.Foreground][governor.MemoryBytes]
	cfg.Partitions[governor.Foreground][governor.MemoryBytes] += 16 << 20
	cfg.Partitions[governor.Background] = governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: protected}
	return cfg
}

func newRBNumericContextFixture(t *testing.T) (*browseFixture, *RBNumericContextReader) {
	t.Helper()
	f := composeReadFixture(t, newRBNumericContextSource(t), RBNumericContextDefinitions(), rbNumericContextConfig(), false, rbNumericContextFixtureHash(t))
	r, err := NewRBNumericContextReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rbNumericContextInput(field RBNumericContextField, filter, personal json.RawMessage) RBNumericContextInput {
	in := RBNumericContextInput{Field: field, Filter: filter, Personal: personal}
	return in
}

func rbNumericContextPredicate(field, operator string, value any) json.RawMessage {
	data, err := json.Marshal(map[string]any{"variant": "predicate", "field": field, "operator": operator, "value": value})
	if err != nil {
		panic(err)
	}
	return data
}

func rbNumericContextRelative(field, operator string, value any) json.RawMessage {
	data, err := json.Marshal(map[string]any{"variant": "relative", "field": field, "operator": operator, "value": value})
	if err != nil {
		panic(err)
	}
	return data
}

func rbNumericContextGroup(key string, children ...json.RawMessage) json.RawMessage {
	parts := make([]any, len(children))
	for i, child := range children {
		var value any
		if err := json.Unmarshal(child, &value); err != nil {
			panic(err)
		}
		parts[i] = value
	}
	data, err := json.Marshal(map[string]any{"variant": "group", key: parts})
	if err != nil {
		panic(err)
	}
	return data
}

func rbNumericContextNot(child json.RawMessage) json.RawMessage {
	var value any
	if err := json.Unmarshal(child, &value); err != nil {
		panic(err)
	}
	data, err := json.Marshal(map[string]any{"variant": "group", "not": value})
	if err != nil {
		panic(err)
	}
	return data
}

func rbNumericContextReady(t *testing.T, job *RBNumericContextPreparation) {
	t.Helper()
	for n := 0; n < 16; n++ {
		step, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if step.Ready {
			return
		}
	}
	t.Fatal("numeric context did not reach raw EOF")
}

func rbNumericContextReadCount(t *testing.T, f *browseFixture, job *RBNumericContextPreparation, in RBNumericContextInput) int64 {
	t.Helper()
	q, request, output := liveRequests("numeric-context-count")
	var count int64
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		count, _, e = job.Count(ctx, request, time.Second, out, in.View)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func rbNumericContextPage(t *testing.T, f *browseFixture, job *RBNumericContextPreparation, in RBNumericContextInput) RBNumericContextPage {
	t.Helper()
	q, request, output := liveRequests("numeric-context-page")
	var page RBNumericContextPage
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = job.Page(ctx, request, time.Second, out, in.View, nil, 200)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func rbNumericContextDefaultYear(id int64) int64 {
	if id%4 == 0 {
		return 0
	}
	return 2000 + id%3
}

func rbNumericContextEligible(id int64) bool {
	return id%9 != 0 && id != 1040
}

func TestRBNumericContextScalarVocabulary(t *testing.T) {
	accepted := []json.RawMessage{
		rbNumericContextPredicate("title", "contains", "title 1"),
		rbNumericContextPredicate("title", "starts-with", "the"),
		rbNumericContextPredicate("year", "between", []any{1990, 1999}),
		rbNumericContextPredicate("decade", "in", []any{1990, 2000}),
		rbNumericContextPredicate("releaseDate", "equals", "2020-01-02"),
		rbNumericContextRelative("dateAdded", "within-last-days", 7),
		rbNumericContextPredicate("dateAdded", "not-equals", "2020-01-02T12:00:00Z"),
		rbNumericContextPredicate("communityRating", "at-least", 6.5),
		rbNumericContextPredicate("criticRating", "is-missing", nil),
		rbNumericContextPredicate("library", "equals", "movies"),
		rbNumericContextPredicate("entityKind", "in", []any{"movie", "episode"}),
		rbNumericContextGroup("all",
			rbNumericContextPredicate("year", "at-least", 1990),
			rbNumericContextNot(rbNumericContextPredicate("communityRating", "less-than", 5))),
	}
	for _, raw := range accepted {
		if _, err := compileRBScalarCompleteFilter(raw); err != nil {
			t.Fatalf("accepted predicate refused: %s: %v", raw, err)
		}
	}
	bad := []json.RawMessage{
		rbNumericContextPredicate("match", "equals", "matched"),
		rbNumericContextPredicate("releaseDate", "less-than", "not-a-date"),
		rbNumericContextPredicate("year", "within-last-days", 7),
		rbNumericContextPredicate("year", "contains", "2001"),
		rbNumericContextPredicate("title", "between", []any{"a", "b"}),
		rbNumericContextPredicate("communityRating", "in", 7),
		rbNumericContextPredicate("communityRating", "in", []any{}),
		rbNumericContextPredicate("title", "is-present", true),
		rbNumericContextPredicate("entityKind", "equals", "unknown-kind"),
		rbNumericContextPredicate("library", "is-missing", nil),
		rbNumericContextPredicate("year", "between", []any{1990}),
		json.RawMessage(`{"variant":"group","all":[]}`),
		json.RawMessage(`{"variant":"predicate","field":"year","operator":"equals","value":2001,"extra":true}`),
		json.RawMessage(`{"variant":"relative","field":"year","operator":"equals","value":2001}`),
	}
	for _, raw := range bad {
		if _, err := compileRBScalarCompleteFilter(raw); err == nil {
			t.Fatalf("unsupported predicate accepted: %s", raw)
		}
	}
	if _, err := compileRBScalarCompleteFilter(nil); err != nil {
		t.Fatal(err)
	}
}

func TestRBNumericContextScalarTruth(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	base := rbScalarCompleteFacts{title: "The Movie", library: "movies", kind: 1, year: 2001, releaseDate: "2026-11-01", dateAdded: "2026-10-01T12:00:00Z", communityRating: rbScalarNumber{value: 7, present: true}, criticRating: rbScalarNumber{value: 6, present: true}, now: now}
	missingYear := base
	missingYear.year = 0
	outOfDomain := base
	outOfDomain.year = 1799
	missingDates := base
	missingDates.releaseDate, missingDates.dateAdded = "", ""
	missingRatings := base
	missingRatings.communityRating, missingRatings.criticRating = rbScalarNumber{}, rbScalarNumber{}
	cases := []struct {
		name  string
		raw   json.RawMessage
		facts rbScalarCompleteFacts
		want  rbTruth
	}{
		{"title-contains", rbNumericContextPredicate("title", "contains", "movie"), base, rbTrue},
		{"title-starts-with-folded", rbNumericContextPredicate("title", "starts-with", "the"), base, rbTrue},
		{"year-missing-equals-false", rbNumericContextPredicate("year", "equals", 2001), missingYear, rbFalse},
		{"year-missing-not-equals-true", rbNumericContextPredicate("year", "not-equals", 2001), missingYear, rbTrue},
		{"year-missing-not-equals-not-included", rbNumericContextNot(rbNumericContextPredicate("year", "equals", 2001)), missingYear, rbTrue},
		{"decade-out-of-domain-indexed-false", rbNumericContextPredicate("decade", "equals", 1790), outOfDomain, rbFalse},
		{"decade-out-of-domain-nullable-unknown", rbNumericContextNot(rbNumericContextPredicate("decade", "equals", 1000000000)), outOfDomain, rbUnknown},
		{"date-missing-equals-unknown", rbNumericContextPredicate("releaseDate", "equals", "2026-11-01"), missingDates, rbUnknown},
		{"date-missing-not-equals-true", rbNumericContextPredicate("dateAdded", "not-equals", "2026-11-01"), missingDates, rbTrue},
		{"date-missing-not-unknown", rbNumericContextNot(rbNumericContextPredicate("releaseDate", "equals", "2026-11-01")), missingDates, rbUnknown},
		{"date-relative-inside", rbNumericContextRelative("releaseDate", "within-last-days", 7), base, rbTrue},
		{"date-relative-outside", rbNumericContextRelative("dateAdded", "within-last-days", 7), base, rbFalse},
		{"rating-missing-positive-false", rbNumericContextPredicate("communityRating", "less-than", 5), missingRatings, rbFalse},
		{"rating-missing-not-true", rbNumericContextNot(rbNumericContextPredicate("communityRating", "less-than", 5)), missingRatings, rbTrue},
		{"rating-missing-not-equals-true", rbNumericContextPredicate("criticRating", "not-equals", 5), missingRatings, rbTrue},
		{"library-identity-case-sensitive", rbNumericContextPredicate("library", "equals", "Movies"), base, rbFalse},
		{"entity-kind-mapped", rbNumericContextPredicate("entityKind", "equals", "movie"), base, rbTrue},
		{"mixed-all", rbNumericContextGroup("all", rbNumericContextPredicate("year", "at-least", 2000), rbNumericContextNot(rbNumericContextPredicate("communityRating", "less-than", 5))), base, rbTrue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := compileRBScalarCompleteFilter(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := rbEvalCompleteScalar(filter.node, tc.facts); got != tc.want {
				t.Fatalf("truth %v want %v", got, tc.want)
			}
		})
	}
}

func TestRBNumericContextYearDecadeBounds(t *testing.T) {
	for _, field := range []RBNumericContextField{RBNumericContextYear, RBNumericContextDecade} {
		t.Run(fmt.Sprint(field), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, reader := newRBNumericContextFixture(t)
				f.exec(t, `DELETE FROM catalog_entities WHERE id>5`)
				f.exec(t, `UPDATE catalog_entities SET year=CASE id WHEN 1 THEN 1799 WHEN 2 THEN 1800 WHEN 3 THEN 2199 WHEN 4 THEN 2200 ELSE 0 END`)
				in := rbNumericContextInput(field, nil, nil)
				in.View = f.input
				job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
				if err != nil {
					t.Fatal(err)
				}
				defer job.Close()
				rbNumericContextReady(t, job)
				page := rbNumericContextPage(t, f, job, in)
				want := []RBNumericContextValue{{Value: 1800, Count: 1}, {Value: 2199, Count: 1}}
				if field == RBNumericContextDecade {
					want[1].Value = 2190
				}
				if !slices.Equal(page.Values, want) || page.ExactTotal != 5 || page.Total != 5 || page.TotalIsLowerBound || !page.Exact || !page.Exhausted || page.After != nil {
					t.Fatal("year/decade bounds inflated values or totals", field, page, want)
				}
				countIn := rbNumericContextInput(RBNumericContextEligible, nil, nil)
				countIn.View = f.input
				count, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, countIn)
				if err != nil {
					t.Fatal(err)
				}
				defer count.Close()
				rbNumericContextReady(t, count)
				if got := rbNumericContextReadCount(t, f, count, countIn); got != 5 {
					t.Fatal("eligible count inherited the year facet domain", got)
				}
			})
		})
	}
}

func TestRBNumericContextMissingYearTotals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, reader := newRBNumericContextFixture(t)
		in := rbNumericContextInput(RBNumericContextYear, nil, nil)
		in.View = f.input
		job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbNumericContextReady(t, job)
		page := rbNumericContextPage(t, f, job, in)
		var eligible, missing, memberships int64
		counts := map[int64]int64{}
		for id := int64(1); id <= 1100; id++ {
			if !rbNumericContextEligible(id) {
				continue
			}
			eligible++
			year := rbNumericContextDefaultYear(id)
			if year < 1800 || year > 2199 {
				missing++
				continue
			}
			memberships++
			counts[year]++
		}
		if eligible != 977 || missing != 244 || memberships != 733 {
			t.Fatal("fixture oracle drifted", eligible, missing, memberships)
		}
		if page.ExactTotal != eligible || page.Total != eligible {
			t.Fatalf("total counted year memberships instead of every eligible match: %+v want %d", page, eligible)
		}
		var sum int64
		for _, value := range page.Values {
			sum += value.Count
			if value.Count != counts[value.Value] {
				t.Fatalf("value %d count %d want %d", value.Value, value.Count, counts[value.Value])
			}
		}
		if sum != memberships {
			t.Fatalf("published memberships %d want %d", sum, memberships)
		}
	})
}

func TestRBNumericContextMixedNumericPredicates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, reader := newRBNumericContextFixture(t)
		filter := rbNumericContextGroup("all",
			rbNumericContextPredicate("year", "at-least", 2000),
			rbNumericContextGroup("any",
				rbNumericContextPredicate("communityRating", "at-least", 7),
				rbNumericContextPredicate("criticRating", "less-than", 6)),
			rbNumericContextNot(rbNumericContextPredicate("decade", "equals", 2000)))
		in := rbNumericContextInput(RBNumericContextYear, filter, nil)
		in.View = f.input
		job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbNumericContextReady(t, job)
		page := rbNumericContextPage(t, f, job, in)
		var want int64
		for id := int64(1); id <= 1100; id++ {
			if !rbNumericContextEligible(id) {
				continue
			}
			year := rbNumericContextDefaultYear(id)
			if year == 0 || year < 2000 {
				continue
			}
			community, critic := 0.0, 0.0
			if id%7 != 0 {
				community = 6.5 + float64(id%3)
			}
			if id%8 != 0 {
				critic = 5.0 + float64(id%3)
			}
			if (community >= 7 || critic < 6) && (year/10*10) != 2000 {
				want++
			}
		}
		if page.ExactTotal != want {
			t.Fatalf("mixed numeric total %d want %d", page.ExactTotal, want)
		}
	})
}

func TestRBNumericContextSparseBoundary(t *testing.T) {
	for _, mode := range []string{"exact-1024", "empty-window", "tail-match"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, reader := newRBNumericContextFixture(t)
				var filter json.RawMessage
				switch mode {
				case "exact-1024":
					f.exec(t, `DELETE FROM catalog_entities WHERE id>1024`)
				case "empty-window":
					filter = rbNumericContextPredicate("year", "equals", 9999)
				case "tail-match":
					filter = rbNumericContextGroup("all",
						rbNumericContextPredicate("title", "contains", "Title 1099"),
						rbNumericContextPredicate("year", "equals", 2001))
				}
				in := rbNumericContextInput(RBNumericContextEligible, filter, nil)
				in.View = f.input
				job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
				if err != nil {
					t.Fatal(err)
				}
				defer job.Close()
				first, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if first.Ready || first.RawRows != rbNumericContextRaw {
					t.Fatal("full 1,024-row window became EOF", first, err)
				}
				second, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
				if err != nil || !second.Ready {
					t.Fatal("short window did not prove EOF", second, err)
				}
				want := int64(911)
				if mode != "exact-1024" {
					want = 0
				}
				if mode == "tail-match" {
					want = 1
				}
				if got := rbNumericContextReadCount(t, f, job, in); got != want {
					t.Fatalf("sparse continuation count %d want %d", got, want)
				}
			})
		})
	}
}

func TestRBNumericContextLowerBoundExactFlags(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		prior := rbNumericContextThreshold
		rbNumericContextThreshold = 3
		defer func() { rbNumericContextThreshold = prior }()
		f, reader := newRBNumericContextFixture(t)
		in := rbNumericContextInput(RBNumericContextYear, nil, nil)
		in.View = f.input
		job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbNumericContextReady(t, job)
		page := rbNumericContextPage(t, f, job, in)
		if !page.Exact || page.ExactTotal != 977 || page.Total != 3 || !page.TotalIsLowerBound {
			t.Fatalf("lower-bound/exact flags: %+v", page)
		}
		for _, value := range page.Values {
			if value.Count > 3 || !value.CountIsLowerBound {
				t.Fatalf("value lower-bound flag: %+v", value)
			}
		}
		countInput := rbNumericContextInput(RBNumericContextEligible, nil, nil)
		countInput.View = f.input
		count, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, countInput)
		if err != nil {
			t.Fatal(err)
		}
		defer count.Close()
		rbNumericContextReady(t, count)
		if got := rbNumericContextReadCount(t, f, count, countInput); got != 977 {
			t.Fatal("private exact eligible count inherited the presentation cap", got)
		}
	})
}

func TestRBNumericContextPersonalFilter(t *testing.T) {
	cases := []struct {
		name  string
		raw   json.RawMessage
		count int64
	}{
		{"favorite", rbNumericContextPredicate("favorite", "equals", true), 1},
		{"rating-at-least", rbNumericContextPredicate("personalRating", "at-least", 8), 1},
		{"not-favorite-includes-absent", rbNumericContextNot(rbNumericContextPredicate("favorite", "equals", true)), 976},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, reader := newRBNumericContextFixture(t)
				f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,revision,favorite,watchlisted,rating) VALUES
 ('p',x'01000000000000000000000000000000',1,1,1,1,0,8),
 ('p',x'02000000000000000000000000000000',2,1,1,0,0,NULL),
 ('p',x'03000000000000000000000000000000',3,1,1,0,1,NULL)`)
				in := rbNumericContextInput(RBNumericContextEligible, nil, tc.raw)
				in.View = f.input
				job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
				if err != nil {
					t.Fatal(err)
				}
				defer job.Close()
				rbNumericContextReady(t, job)
				if got := rbNumericContextReadCount(t, f, job, in); got != tc.count {
					t.Fatalf("personal count %d want %d", got, tc.count)
				}
			})
		})
	}
	t.Run("container-personal-refused", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f, reader := newRBNumericContextFixture(t)
			in := rbNumericContextInput(RBNumericContextEligible, nil, rbNumericContextPredicate("favorite", "equals", true))
			in.View = f.input
			in.View.Kind = 2
			if _, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in); !errors.Is(err, ErrUnqualified) {
				t.Fatal("container personal filter was not refused", err)
			}
		})
	})
}

func TestRBNumericContextSourceWalks(t *testing.T) {
	cases := []struct {
		name  string
		raw   json.RawMessage
		match func(id int64) bool
	}{
		{"release", rbNumericContextPredicate("releaseDate", "at-least", "2000-01-01"), func(id int64) bool {
			return id%5 != 0 && id%5 != 1
		}},
		{"rating", rbNumericContextPredicate("communityRating", "at-least", 7), func(id int64) bool {
			return id%7 != 0 && 6.5+float64(id%3) >= 7
		}},
		{"critic", rbNumericContextPredicate("criticRating", "less-than", 6), func(id int64) bool {
			return id%8 != 0 && 5.0+float64(id%3) < 6
		}},
		{"library", rbNumericContextPredicate("title", "contains", "Title 1"), func(id int64) bool {
			return strings.Contains(fmt.Sprintf("Title %d", id), "Title 1")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, reader := newRBNumericContextFixture(t)
				in := rbNumericContextInput(RBNumericContextEligible, tc.raw, nil)
				in.View = f.input
				job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
				if err != nil {
					t.Fatal(err)
				}
				defer job.Close()
				rbNumericContextReady(t, job)
				var want int64
				for id := int64(1); id <= 1100; id++ {
					if rbNumericContextEligible(id) && tc.match(id) {
						want++
					}
				}
				if got := rbNumericContextReadCount(t, f, job, in); got != want {
					t.Fatalf("source walk count %d want %d", got, want)
				}
			})
		})
	}
}

func TestRBNumericContextDriftAndDenial(t *testing.T) {
	for _, mode := range []string{"stale", "denied", "empty"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, reader := newRBNumericContextFixture(t)
				if mode == "empty" {
					f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE library_id=1`)
				}
				in := rbNumericContextInput(RBNumericContextEligible, nil, nil)
				in.View = f.input
				job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
				if err != nil {
					t.Fatal(err)
				}
				defer job.Close()
				rbNumericContextReady(t, job)
				want := error(nil)
				wantCount := int64(977)
				switch mode {
				case "stale":
					f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
					want = ErrStale
				case "denied":
					f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
					want = ErrDenied
				case "empty":
					wantCount = 0
				}
				q, request, output := liveRequests("numeric-context-" + mode)
				_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
					got, _, e := job.Count(ctx, request, time.Second, out, in.View)
					if want != nil && got != 0 {
						t.Fatal("invalid preparation exposed a count", got)
					}
					if want == nil && got != wantCount {
						t.Fatal("empty preparation count", got, wantCount)
					}
					return e
				})
				if want != nil && !errors.Is(err, want) {
					t.Fatal(err, want)
				}
				if want != nil && !job.entry.closed {
					t.Fatal("invalid prepared job retained its lease")
				}
				if want == nil && err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestRBNumericContextPreflightRefusal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, reader := newRBNumericContextFixture(t)
		in := rbNumericContextInput(RBNumericContextEligible, nil, nil)
		in.View = f.input
		job, err := reader.BeginNumericContext(t.Context(), 1, 4<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		request := rbFacetReadRequest()
		// Leave no copy budget beyond the fixed native row reservation: the
		// same-snapshot copy preflight must refuse before any row is retained.
		request.Need[governor.MemoryBytes] = f.snapshots.NativeRowReservation()
		if _, err = job.Advance(t.Context(), request, time.Second); !errors.Is(err, governor.ErrSnapshotBudget) {
			t.Fatal("copy preflight did not refuse", err)
		}
		if !job.entry.closed {
			t.Fatal("refused preparation retained its lease")
		}
	})
}
