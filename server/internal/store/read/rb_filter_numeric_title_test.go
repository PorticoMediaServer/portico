package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func rbNumericTitleSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, "DELETE FROM catalog_entities WHERE id>9")
	f.exec(t, "UPDATE catalog_entities SET retired=0,year=CASE id WHEN 1 THEN 0 WHEN 2 THEN 1999 WHEN 3 THEN 1999 WHEN 4 THEN 2000 WHEN 5 THEN 0 WHEN 6 THEN 1799 WHEN 7 THEN 2200 WHEN 8 THEN 2000 ELSE 2020 END")
	f.exec(t, "UPDATE catalog_entities SET retired=1 WHERE id=8")
	f.exec(t, "UPDATE catalog_entities SET sort_key=CASE id WHEN 1 THEN 'a' WHEN 2 THEN 'same' WHEN 3 THEN 'same' WHEN 4 THEN 'same' ELSE 'z' END,sort_key_keep_articles=CASE id WHEN 1 THEN 'z' WHEN 2 THEN 'same' WHEN 3 THEN 'same' WHEN 4 THEN 'same' ELSE 'a' END")
	return f
}
func rbNumericTitleHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_filter_numeric_title_test.go")
	if e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data...)))
}
func rbNumericTitleFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, rbNumericTitleSource(t), append(browseDefinitions(), RBNumericTitleFilterDefinitions()...), readfixture.LiveConfig(time.Now), false, rbNumericTitleHash(t))
}
func rbNumericTitleRead(t *testing.T, f *browseFixture, in NumericTitleFilterInput) (NumericTitleFilterPage, BrowseReadResult, error) {
	t.Helper()
	var page NumericTitleFilterPage
	var cost BrowseReadResult
	live, request, output := liveRequests("numeric-title-filter")
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, cost, e = f.reader.NumericTitleFilterWindow(ctx, request, time.Second, out, in)
		return e
	})
	return page, cost, err
}

func TestRBNumericTitleFilterTraversal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbNumericTitleFixture(t)
		cases := []struct{ query, where string }{
			{`{"variant":"predicate","field":"year","operator":"equals","value":1999}`, "year=1999"},
			{`{"variant":"group","not":{"variant":"predicate","field":"year","operator":"greater-than","value":1999}}`, "NOT (NULLIF(year,0)>1999)"},
			{`{"variant":"predicate","field":"year","operator":"not-equals","value":1999}`, "year=0 OR year<>1999"},
			{`{"variant":"predicate","field":"decade","operator":"equals","value":1990}`, "year BETWEEN 1990 AND 1999"},
			{`{"variant":"predicate","field":"year","operator":"is-missing","value":null}`, "year=0"},
			{`{"variant":"predicate","field":"library","operator":"equals","value":"movies"}`, "1"},
			{`{"variant":"predicate","field":"entityKind","operator":"equals","value":"movie"}`, "1"},
		}
		for _, tc := range cases {
			for _, keep := range []bool{false, true} {
				for _, desc := range []bool{false, true} {
					in := NumericTitleFilterInput{View: f.input, Query: json.RawMessage(tc.query)}
					in.View.Limit = 1
					in.View.KeepArticles, in.View.Descending = keep, desc
					var got []int64
					for window := 0; ; window++ {
						if window > 12 {
							t.Fatal("cursor stalled", tc.query)
						}
						page, _, err := rbNumericTitleRead(t, f, in)
						if err != nil || page.RawRows > MaxRaw || page.RawPositions > page.RawRows {
							t.Fatal(tc.query, page, err)
						}
						for _, item := range page.Items {
							got = append(got, item.ID)
						}
						if page.Exhausted {
							if page.After != nil {
								t.Fatal("EOF retained cursor")
							}
							break
						}
						if page.After == nil || page.RawPositions == 0 {
							t.Fatal("lost raw progress", page)
						}
						in.After = page.After
					}
					key, direction := "sort_key", "ASC"
					if keep {
						key = "sort_key_keep_articles"
					}
					if desc {
						direction = "DESC"
					}
					rows, err := f.conn.QueryContext(t.Context(), "SELECT id FROM catalog_entities WHERE library_id=1 AND kind=1 AND retired=0 AND ("+tc.where+") ORDER BY "+key+" COLLATE NOCASE "+direction+",id "+direction)
					if err != nil {
						t.Fatal(err)
					}
					var want []int64
					for rows.Next() {
						var id int64
						if err = rows.Scan(&id); err != nil {
							t.Fatal(err)
						}
						want = append(want, id)
					}
					err = rows.Err()
					rows.Close()
					if err != nil || !slices.Equal(got, want) {
						t.Fatal("native public expression differs", tc.query, keep, desc, got, want, err)
					}
				}
			}
		}
	})
}

func TestRBNumericTitleFilterRawProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbNumericTitleFixture(t)
		f.exec(t, "INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,year,retired) SELECT id,public_id,library_id,kind,'Hidden','hidden','h','hidden','h',0,public_id,public_id,2001,1 FROM catalog_subjects WHERE id BETWEEN 10 AND 150")
		in := NumericTitleFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"year","operator":"equals","value":1901}`)}
		raw := 0
		for window := 0; ; window++ {
			if window > 3 {
				t.Fatal("filtered tail stalled")
			}
			page, _, err := rbNumericTitleRead(t, f, in)
			if err != nil || len(page.Items) != 0 || page.RawPositions == 0 {
				t.Fatal(page, err)
			}
			raw += page.RawPositions
			if page.Exhausted {
				if page.After != nil {
					t.Fatal("EOF cursor")
				}
				break
			}
			if page.After == nil {
				t.Fatal("hidden prefix became EOF")
			}
			in.After = page.After
		}
		if raw != 150 {
			t.Fatal("raw positions omitted", raw)
		}
	})
}

func TestRBNumericTitleFilterFencesAndRefusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbNumericTitleFixture(t)
		in := NumericTitleFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"year","operator":"equals","value":1999}`)}
		in.View.Limit = 1
		first, _, err := rbNumericTitleRead(t, f, in)
		if err != nil || len(first.Items) != 1 || first.After == nil {
			t.Fatal(first, err)
		}
		assertLiveUsage(t, f, 0, 0)
		in.After = first.After
		in.Query = json.RawMessage(`{"variant":"predicate","field":"year","operator":"equals","value":2000}`)
		page, _, err := rbNumericTitleRead(t, f, in)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, NumericTitleFilterPage{}) {
			t.Fatal("query cursor reused", page, err)
		}
		in.After = nil
		in.Query = json.RawMessage(`{"variant":"group","not":{"variant":"predicate","field":"title","operator":"equals","value":"Hidden"}}`)
		page, _, err = rbNumericTitleRead(t, f, in)
		if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, NumericTitleFilterPage{}) {
			t.Fatal("unsupported NOT became permission", page, err)
		}
		in.Query = nil
		f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, "UPDATE authorization_session_families SET revoked=1") }}
		page, _, err = rbNumericTitleRead(t, f, in)
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(page, NumericTitleFilterPage{}) {
			t.Fatal("revoked page escaped", page, err)
		}
		_, request, _ := liveRequests("numeric-bypass")
		page, _, err = f.reader.NumericTitleFilterWindow(t.Context(), request, time.Second, BrowseOutput{}, in)
		if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, NumericTitleFilterPage{}) {
			t.Fatal("LIVE bypass", page, err)
		}
	})
}

func TestRBNumericTitleFilterCapture(t *testing.T) {
	f := rbNumericTitleSource(t)
	defs := RBNumericTitleFilterDefinitions()
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, defs, stats, rbNumericTitleHash(t))
	}
}
