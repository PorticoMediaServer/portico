package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestCompatibilityPagesOffsetsTotalsAndExplicitZero(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		start, limit          int
		limited, total, array bool
		want                  []int
		calls                 int
	}{
		{"unlimited", 0, 0, false, true, false, []int{0, 1, 2, 3, 4, 5, 6}, 3},
		{"cross page slice", 2, 3, true, true, false, []int{2, 3, 4}, 2},
		{"explicit zero", 0, 0, true, true, false, []int{}, 1},
		{"count walk without native total", 3, 1, true, false, false, []int{3}, 3},
		{"past end", 20, 2, true, true, false, []int{}, 3},
		{"array slice without total", 1, 2, true, false, true, []int{1, 2}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			fetch := func(q url.Values) (map[string]any, error) {
				calls++
				offset, _ := strconv.Atoi(q.Get("cursor"))
				items := []any{}
				for i := offset; i < offset+3 && i < 7; i++ {
					items = append(items, map[string]any{"number": float64(i)})
				}
				page := map[string]any{"items": items}
				if offset+3 < 7 {
					page["nextCursor"] = strconv.Itoa(offset + 3)
				}
				if tc.total {
					page["total"] = float64(7)
				}
				return page, nil
			}
			w := httptest.NewRecorder()
			compatibilityStreamPages(w, httptest.NewRequest("GET", "/Items", nil), url.Values{}, tc.array, tc.start, tc.limit, tc.limited, fetch, func(row map[string]any) (any, error) { return int(compatNumber(row["number"])), nil })
			var got []int
			if tc.array {
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
			} else {
				var body struct {
					Items                        []int
					StartIndex, TotalRecordCount int
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				got = body.Items
				if body.StartIndex != tc.start || body.TotalRecordCount != 7 {
					t.Fatalf("incorrect paging envelope: %+v", body)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("rows %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("rows %v, want %v", got, tc.want)
				}
			}
			if calls != tc.calls {
				t.Fatalf("fetched %d pages, want %d", calls, tc.calls)
			}
		})
	}
}
func TestCompatibilityPagesAbortRatherThanPublishTruncation(t *testing.T) {
	for _, repeated := range []bool{false, true} {
		t.Run(strconv.FormatBool(repeated), func(t *testing.T) {
			calls := 0
			defer func() {
				if recover() != http.ErrAbortHandler {
					t.Error("incomplete native traversal must abort the HTTP response")
				}
			}()
			compatibilityStreamPages(httptest.NewRecorder(), httptest.NewRequest("GET", "/Items", nil), url.Values{}, false, 0, 0, false, func(q url.Values) (map[string]any, error) {
				calls++
				if calls > 1 && !repeated {
					return nil, errors.New("source unavailable")
				}
				return map[string]any{"items": []any{map[string]any{"id": "one"}}, "nextCursor": "again"}, nil
			}, func(row map[string]any) (any, error) { return row, nil })
		})
	}
}
