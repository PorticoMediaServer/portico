package read

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Actual Reader/source qualification only. Stored-resource octet registration
// and first/final publication remain closed; these values mint no principal.
func TestRBSavedViewScalar(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET retired=0,year=0,admission_own_age=10`)
	f.exec(t, `UPDATE catalog_entities SET year=2020 WHERE id IN(100,1100)`)
	data, e := os.ReadFile("rb_saved_view_scalar_test.go")
	if e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	defs := rbSavedViewScalarDefinitions()
	f = composeBrowseFixture(t, f, append(browseDefinitions(), defs...))
	filter, e := compileRBScalarFilter(json.RawMessage(`{"variant":"predicate","field":"year","operator":"equals","value":2020}`))
	if e != nil {
		t.Fatal(e)
	}
	read := func(in BrowseInput, p rbScalarFilter) (BrowsePage, error) {
		var page BrowsePage
		_, e := f.snapshots.Read(t.Context(), savedViewRequest(), time.Second, func(scope *governor.ReadScope) error {
			q := func(name string, args ...any) ([][]any, error) { return scope.Query(readStatementID(name), args...) }
			fence, e := readBrowseFence(q, in)
			if e != nil {
				return e
			}
			if in.After != nil && in.After.fence != fence {
				return ErrStale
			}
			page, e = readSavedViewScalarPage(t.Context(), q, in, fence, p)
			return e
		})
		return page, e
	}
	for _, desc := range []bool{false, true} {
		in := f.input
		in.Limit = 1
		in.Descending = desc
		var ids []int64
		positions := 0
		exhausted := false
		for n := 0; n < 40; n++ {
			page, e := read(in, filter)
			if e != nil {
				t.Fatal(page, e)
			}
			if !desc && n == 0 && (len(page.Items) != 0 || page.RawRows != 64 || page.RawPositions != 64 || page.Exhausted || page.Next == nil || page.Next.id != 64) {
				t.Fatal("raw cap lost hidden work", page)
			}
			positions += page.RawPositions
			for _, item := range page.Items {
				ids = append(ids, item.ID)
			}
			if page.Exhausted {
				if page.Next != nil {
					t.Fatal(page)
				}
				exhausted = true
				break
			}
			if page.Next == nil {
				t.Fatal("lost continuation", page)
			}
			in.After = page.Next
		}
		want := []int64{100, 1100}
		if desc {
			want = []int64{1100, 100}
		}
		if !exhausted || positions != 1099 || !reflect.DeepEqual(ids, want) {
			t.Fatal(desc, exhausted, positions, ids)
		}
	}
	// Each supported field is the existing shared AST, never a new view dialect.
	for _, raw := range []string{
		`{"variant":"predicate","field":"decade","operator":"equals","value":2020}`,
		`{"variant":"predicate","field":"title","operator":"contains","value":"TITLE"}`,
		`{"variant":"predicate","field":"library","operator":"equals","value":"movies"}`,
		`{"variant":"predicate","field":"entityKind","operator":"equals","value":"movie"}`,
	} {
		p, e := compileRBScalarFilter(json.RawMessage(raw))
		if e != nil {
			t.Fatal(e)
		}
		if !p.matches(rbFilterFacts{title: "Title 100", library: "movies", kind: 1, year: 2020}) {
			t.Fatal(raw)
		}
	}
	// Missing numeric facts under NOT remain unknown, not permission.
	missing, e := compileRBScalarFilter(json.RawMessage(`{"variant":"group","not":{"variant":"predicate","field":"year","operator":"less-than","value":2000}}`))
	if e != nil || missing.matches(rbFilterFacts{year: 0}) {
		t.Fatal("unknown became true under NOT", e)
	}
	for _, raw := range []string{
		`{"variant":"group","not":{"variant":"predicate","field":"genre","operator":"equals","value":"drama"}}`,
		`{"variant":"relative","field":"year","operator":"within-last-days","value":7}`,
	} {
		if _, e := compileRBScalarFilter(json.RawMessage(raw)); !errors.Is(e, ErrUnqualified) {
			t.Fatal("unsupported broadened", raw, e)
		}
	}
	// Current age admission precedes scalar matching.
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=100`)
	in := f.input
	in.Limit = 40
	var got []int64
	for n := 0; n < 25; n++ {
		page, e := read(in, filter)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		if page.Exhausted {
			break
		}
		in.After = page.Next
	}
	if !reflect.DeepEqual(got, []int64{1100}) {
		t.Fatal("age admission bypass", got)
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, defs, stats, hash)
	}
}
