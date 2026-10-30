package compactcatalog_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestReadingPathValues(t *testing.T) {
	input := []compactcatalog.ReadingRuleInput{
		{1, "books", "A & B - Mixed_CASE.Title [Group] (Digital).epub", compactcatalog.Ebook},
		{2, "books", "Frank Herbert/Dune/Dune 01 - Dune.epub", compactcatalog.Ebook},
		{3, "books", "Frank Herbert/Dune/Dune 02.5 - Dune_Messiah.pdf", compactcatalog.Ebook},
		{4, "comics", "Nemo/Nemo v01 - Little_Nemo.cbz", compactcatalog.Comic},
		{5, "comics", "Nemo/Nemo Vol. 2 - Dreamland.cbz", compactcatalog.Comic},
		{6, "books", "Jane Doe/Jane Doe - First.epub", compactcatalog.Ebook},
		{7, "books", "Jane Doe/Second.epub", compactcatalog.Ebook},
		{8, "books", "Root_one.epub", compactcatalog.Ebook},
		{9, "books", "Root_two.epub", compactcatalog.Ebook},
		{10, "other-source", "Nemo/Single.pdf", compactcatalog.Ebook},
	}
	got, err := compactcatalog.ReadingPathValues(input)
	if err != nil {
		t.Fatal(err)
	}
	check := func(id int64, title, author, series string, number *float64, authorSource compactcatalog.ReadingValueSource) {
		t.Helper()
		v := got[id]
		if v.Title != title {
			t.Errorf("item %d title=%q want %q", id, v.Title, title)
		}
		if author == "" {
			if v.Author != nil {
				t.Errorf("item %d invented author: %+v", id, v.Author)
			}
		} else if v.Author == nil || v.Author.Value != author || v.Author.Source != authorSource {
			t.Errorf("item %d author=%+v", id, v.Author)
		}
		if series == "" {
			if v.Series != nil {
				t.Errorf("item %d invented series: %+v", id, v.Series)
			}
		} else if v.Series == nil || v.Series.Value != series || v.Series.Source != compactcatalog.ReadingFolder {
			t.Errorf("item %d series=%+v", id, v.Series)
		}
		if number == nil {
			if v.SeriesNumber != nil {
				t.Errorf("item %d invented number=%+v", id, v.SeriesNumber)
			}
		} else if v.SeriesNumber == nil || v.SeriesNumber.Value != *number || v.SeriesNumber.Source != compactcatalog.ReadingFileName {
			t.Errorf("item %d number=%+v", id, v.SeriesNumber)
		}
	}
	n := func(v float64) *float64 { return &v }
	check(1, "Mixed CASE Title", "A & B", "", nil, compactcatalog.ReadingFileName)
	check(2, "Dune", "Frank Herbert", "Dune", n(1), compactcatalog.ReadingFolder)
	check(3, "Dune Messiah", "Frank Herbert", "Dune", n(2.5), compactcatalog.ReadingFolder)
	check(4, "Little Nemo", "", "Nemo", n(1), "")
	check(5, "Dreamland", "", "Nemo", n(2), "")
	check(6, "First", "Jane Doe", "", nil, compactcatalog.ReadingFileName)
	check(7, "Second", "Jane Doe", "", nil, compactcatalog.ReadingFolder)
	check(8, "Root one", "", "", nil, "")
	check(9, "Root two", "", "", nil, "")
	check(10, "Single", "Nemo", "", nil, compactcatalog.ReadingFolder)
}

func TestReadingPathNumbersAndDates(t *testing.T) {
	for _, tc := range []struct {
		stem, title string
		number      *float64
	}{
		{"Story v01 - Title", "Story - Title", number(1)},
		{"Story Vol.1 - Title", "Story - Title", number(1)},
		{"Story Volume 1 - Title", "Story - Title", number(1)},
		{"Story #12 - Title", "Story - Title", number(12)},
		{"Story - 03 - Title", "Story Title", number(3)},
		{"Story Book 2 - Title", "Story - Title", number(2)},
		{"Story v1.25 - Title", "Story - Title", number(1.25)},
		{"2026-03-04", "2026-03-04", nil},
		{"20260304", "20260304", nil},
		{"03-04-2026", "03-04-2026", nil},
	} {
		t.Run(tc.stem, func(t *testing.T) {
			got, err := compactcatalog.ReadingPathValues([]compactcatalog.ReadingRuleInput{{1, "source", tc.stem + ".pdf", compactcatalog.Ebook}})
			if err != nil {
				t.Fatal(err)
			}
			v := got[1]
			if v.Title != tc.title {
				t.Errorf("title=%q want %q", v.Title, tc.title)
			}
			if tc.number == nil {
				if v.SeriesNumber != nil {
					t.Fatalf("date used as series number: %+v", v)
				}
			} else if v.SeriesNumber == nil || v.SeriesNumber.Value != *tc.number {
				t.Errorf("number=%+v want %v", v.SeriesNumber, *tc.number)
			}
		})
	}
}

func number(n float64) *float64 { return &n }

func TestReadingPathValuesRejectInvalidIdentityAndPaths(t *testing.T) {
	for _, name := range []string{"../outside.pdf", "a/../../outside.pdf", "/absolute.pdf", "a//b.pdf", "a/./b.pdf", "a\\b.pdf", "a\x00.pdf", ".", ""} {
		if _, err := compactcatalog.ReadingPathValues([]compactcatalog.ReadingRuleInput{{1, "source", name, compactcatalog.Ebook}}); !errors.Is(err, compactcatalog.ErrReadingValue) {
			t.Errorf("path %q accepted: %v", name, err)
		}
	}
	for _, input := range [][]compactcatalog.ReadingRuleInput{
		{{0, "source", "A.pdf", compactcatalog.Ebook}},
		{{1, "", "A.pdf", compactcatalog.Ebook}},
		{{1, "source", "A.pdf", compactcatalog.Book}},
		{{1, "source", "A.pdf", compactcatalog.Ebook}, {1, "other", "B.pdf", compactcatalog.Comic}},
	} {
		if _, err := compactcatalog.ReadingPathValues(input); !errors.Is(err, compactcatalog.ErrReadingValue) {
			t.Errorf("invalid identity accepted: %v", err)
		}
	}
}

func TestReadingEffectiveValuesOwnerClearAndAbsence(t *testing.T) {
	auto := compactcatalog.ReadingValues{Title: "Dune", Author: &compactcatalog.ReadingValue{"Frank Herbert", compactcatalog.ReadingFolder}, Series: &compactcatalog.ReadingValue{"dune", compactcatalog.ReadingFolder}, SeriesNumber: &compactcatalog.ReadingNumber{1, compactcatalog.ReadingFileName}}
	owner := map[string]compactcatalog.ReadingOwnerValue{"author": {"A & B", true}, "series": {"Dune", true}, "seriesNumber": {"2.5", true}}
	v, err := compactcatalog.ReadingEffectiveValues(auto, owner)
	if err != nil {
		t.Fatal(err)
	}
	if v.Author.Value != "A & B" || v.Author.Source != compactcatalog.ReadingOwner || v.Series.Value != "Dune" || v.Series.Source != compactcatalog.ReadingOwner || v.SeriesNumber.Value != 2.5 || v.SeriesNumber.Source != compactcatalog.ReadingOwner {
		t.Fatalf("owner lost: %+v", v)
	}
	if auto.Author.Value != "Frank Herbert" || auto.Series.Value != "dune" || auto.SeriesNumber.Value != 1 {
		t.Fatal("automatic facts mutated")
	}
	for _, clear := range []compactcatalog.ReadingOwnerValue{{"", true}, {"   ", true}, {"old owner", false}} {
		got, err := compactcatalog.ReadingEffectiveValues(auto, map[string]compactcatalog.ReadingOwnerValue{"author": clear, "series": clear, "seriesNumber": clear})
		if err != nil || !reflect.DeepEqual(got, auto) {
			t.Fatalf("clear must restore actual producer: %+v %v", got, err)
		}
		got, err = compactcatalog.ReadingEffectiveValues(compactcatalog.ReadingValues{}, map[string]compactcatalog.ReadingOwnerValue{"author": clear, "series": clear, "seriesNumber": clear})
		if err != nil || got.Author != nil || got.Series != nil || got.SeriesNumber != nil {
			t.Fatalf("absent fallback invented: %+v %v", got, err)
		}
	}
	for _, invalid := range []string{"-1", "NaN", "Inf", "1e999", "one", "1/2"} {
		if _, err := compactcatalog.ReadingEffectiveValues(auto, map[string]compactcatalog.ReadingOwnerValue{"seriesNumber": {invalid, true}}); !errors.Is(err, compactcatalog.ErrReadingValue) {
			t.Errorf("accepted %q", invalid)
		}
	}
	for _, valid := range []string{"0", "0.5", "12.25"} {
		if _, err := compactcatalog.ReadingEffectiveValues(auto, map[string]compactcatalog.ReadingOwnerValue{"seriesNumber": {valid, true}}); err != nil {
			t.Errorf("rejected %q", valid)
		}
	}
	if _, err := compactcatalog.ReadingEffectiveValues(auto, map[string]compactcatalog.ReadingOwnerValue{"author": {"A\nB", true}}); !errors.Is(err, compactcatalog.ErrReadingValue) {
		t.Fatal("control characters accepted")
	}
}

func TestReadingFacetsAcceptedValuesAndSeriesOrder(t *testing.T) {
	item := func(id int64, title, name string, source compactcatalog.ReadingValueSource, n *float64) compactcatalog.ReadingFacetItem {
		v := compactcatalog.ReadingValues{Series: &compactcatalog.ReadingValue{name, source}}
		if n != nil {
			v.SeriesNumber = &compactcatalog.ReadingNumber{*n, source}
		}
		return compactcatalog.ReadingFacetItem{Item: id, TitleKey: title, Values: v}
	}
	rows := []compactcatalog.ReadingFacetItem{
		item(1, "z", "dune", compactcatalog.ReadingFolder, number(1)),
		item(2, "a", "Dune", compactcatalog.ReadingFolder, number(1)),
		item(3, "unnumbered", "dune", compactcatalog.ReadingFolder, nil),
		item(4, "second", "DUNE", compactcatalog.ReadingOwner, number(2)),
	}
	rows[0].Values.Author = &compactcatalog.ReadingValue{"A & B", compactcatalog.ReadingFileName}
	rows[1].Values.Author = &compactcatalog.ReadingValue{"a & b", compactcatalog.ReadingFileName}
	got := compactcatalog.ReadingFacets("library", rows)
	if len(got.Series) != 1 || got.Series[0].Name != "DUNE" || got.Series[0].Count != 4 || len(got.Authors) != 1 || got.Authors[0].Count != 2 {
		t.Fatalf("facet grouping/owner label: %+v", got)
	}
	if compactcatalog.ReadingValueKey("  Dune\tSaga  ") != "dune saga" {
		t.Fatal("normalization differs from genre whitespace folding")
	}
	if got.Series[0].ID != compactcatalog.ReadingSeriesID("library", "dune") || got.Series[0].ID == compactcatalog.ReadingSeriesID("other-library", "dune") {
		t.Fatal("identity not library+normalized value")
	}
	list, ok := compactcatalog.ReadingSeriesItems("library", got.Series[0].ID, rows)
	if !ok || len(list) != 4 || list[0].Item != 2 || list[1].Item != 1 || list[2].Item != 4 || list[3].Item != 3 {
		t.Fatalf("number/title/unnumbered ordering: %+v", list)
	}
	old := got.Series[0].ID
	for i := range rows {
		rows[i].Values.Series = &compactcatalog.ReadingValue{"Dune renamed", compactcatalog.ReadingOwner}
	}
	if _, ok := compactcatalog.ReadingSeriesItems("library", old, rows); ok {
		t.Fatal("renamed series kept old link")
	}
	if _, ok := compactcatalog.ReadingSeriesItems("other-library", compactcatalog.ReadingSeriesID("library", "Dune renamed"), rows); ok {
		t.Fatal("series crossed library identity")
	}
	empty := compactcatalog.ReadingFacets("library", []compactcatalog.ReadingFacetItem{{Item: 1}})
	if len(empty.Authors) != 0 || len(empty.Series) != 0 {
		t.Fatal("absent facts invented pivots")
	}
	first := compactcatalog.ReadingFacets("library", rows[:1])
	if len(first.Series) != 1 {
		t.Fatal("first accepted value did not create facet")
	}
	frequency := compactcatalog.ReadingFacets("library", []compactcatalog.ReadingFacetItem{item(1, "a", "Dune", compactcatalog.ReadingFolder, nil), item(2, "b", "dune", compactcatalog.ReadingFolder, nil), item(3, "c", "dune", compactcatalog.ReadingFolder, nil)})
	if frequency.Series[0].Name != "dune" {
		t.Fatal("label is not most frequent accepted spelling")
	}
}

func TestReadingPathRuling45Vectors(t *testing.T) {
	tests := []struct {
		name           string
		paths          []string
		ids            []int64
		author, series string
		nums           []float64
	}{
		{"hierarchy", []string{"Frank Herbert/Dune Saga/Dune.epub", "Frank Herbert/Dune Saga/Children of Dune.epub"}, []int64{1, 2}, "Frank Herbert", "Dune Saga", nil},
		{"calibre", []string{"Frank Herbert/Dune (12)/Dune.epub", "Frank Herbert/Dune (12)/Dune.pdf"}, []int64{1, 1}, "Frank Herbert", "", nil},
		{"ambiguous", []string{"Isaac Asimov/Foundation.epub"}, []int64{1}, "Isaac Asimov", "", nil},
		{"filename-author", []string{"Isaac Asimov/Isaac Asimov - Foundation.epub"}, []int64{1}, "Isaac Asimov", "", nil},
		{"nemo", []string{"Little Nemo/Little Nemo 01.cbz", "Little Nemo/02.cbz"}, []int64{1, 2}, "", "Little Nemo", []float64{1, 2}},
		{"not-every-title", []string{"Dune/Dune.epub", "Dune/Children of Dune.epub"}, []int64{1, 2}, "Dune", "", nil},
		{"deep", []string{"A/B/C/One.epub", "A/B/C/Two.epub"}, []int64{1, 2}, "B", "C", nil},
		{"formats-once", []string{"Dune/Dune.epub", "Dune/Dune.pdf"}, []int64{1, 1}, "", "", nil},
		{"generic-deep", []string{"Books/Sci-fi/Frank Herbert/Dune.epub"}, []int64{1}, "Frank Herbert", "", nil},
		{"generic-only", []string{"Ebooks/Foundation.epub"}, []int64{1}, "", "", nil},
		{"root-tags", []string{"Some Book [Group] (Digital).epub"}, []int64{1}, "", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var inputs []compactcatalog.ReadingRuleInput
			for i, p := range tc.paths {
				inputs = append(inputs, compactcatalog.ReadingRuleInput{tc.ids[i], "library-root", p, compactcatalog.Ebook})
			}
			got, err := compactcatalog.ReadingPathValues(inputs)
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range tc.ids {
				v := got[id]
				if tc.author == "" {
					if v.Author != nil {
						t.Errorf("invented author: %+v", v.Author)
					}
				} else if v.Author == nil || v.Author.Value != tc.author {
					t.Errorf("author=%+v want %q", v.Author, tc.author)
				}
				if tc.series == "" {
					if v.Series != nil {
						t.Errorf("invented series: %+v", v.Series)
					}
				} else if v.Series == nil || v.Series.Value != tc.series {
					t.Errorf("series=%+v want %q", v.Series, tc.series)
				}
				if len(tc.nums) > 0 && (v.SeriesNumber == nil || v.SeriesNumber.Value != tc.nums[i]) {
					t.Errorf("number=%+v want %v", v.SeriesNumber, tc.nums[i])
				}
			}
		})
	}
}

func TestReadingPathFilenameAuthorRequiresExactEvidence(t *testing.T) {
	for _, tc := range []struct{ path, author, title string }{
		{"George Orwell - 1984.epub", "George Orwell", "1984"},
		{"A & B - Work.epub", "A & B", "Work"},
		{"A - Work - Subtitle.epub", "", "A - Work - Subtitle"},
		{"01 - Work.epub", "", "01 - Work"},
		{"Series Issue 4 - Work.epub", "", "Series - Work"},
		{"Series 01 - Work.epub", "", "Series 01 - Work"},
		{"Folder/Series/Other Author - Work.epub", "Other Author", "Work"},
		{"A/B/C/Other Author - Work.epub", "Other Author", "Work"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := compactcatalog.ReadingPathValues([]compactcatalog.ReadingRuleInput{{1, "source", tc.path, compactcatalog.Ebook}})
			if err != nil {
				t.Fatal(err)
			}
			v := got[1]
			if tc.author == "" {
				if v.Author != nil {
					t.Fatalf("invented author %+v", v.Author)
				}
			} else if v.Author == nil || v.Author.Value != tc.author || v.Author.Source != compactcatalog.ReadingFileName {
				t.Fatalf("author=%+v want %q", v.Author, tc.author)
			}
			if v.Title != tc.title {
				t.Errorf("title=%q want %q", v.Title, tc.title)
			}
		})
	}
}

func TestReadingAcceptedSourcePrecedence(t *testing.T) {
	path := compactcatalog.ReadingValues{Author: &compactcatalog.ReadingValue{"Path author", compactcatalog.ReadingFolder}, Series: &compactcatalog.ReadingValue{"Path series", compactcatalog.ReadingFolder}, SeriesNumber: &compactcatalog.ReadingNumber{1, compactcatalog.ReadingFileName}}
	file := compactcatalog.ReadingValues{Author: &compactcatalog.ReadingValue{"File author", compactcatalog.ReadingFileMetadata}, Series: &compactcatalog.ReadingValue{"File series", compactcatalog.ReadingFileMetadata}, SeriesNumber: &compactcatalog.ReadingNumber{2, compactcatalog.ReadingFileMetadata}}
	auto, err := compactcatalog.ReadingAutomaticValues(path, file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(auto, file) {
		t.Fatalf("file must beat path: %+v", auto)
	}
	owner := map[string]compactcatalog.ReadingOwnerValue{"author": {"Owner author", true}, "series": {"Owner series", true}, "seriesNumber": {"3", true}}
	got, err := compactcatalog.ReadingEffectiveValues(auto, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got.Author.Value != "Owner author" || got.Series.Value != "Owner series" || got.SeriesNumber.Value != 3 {
		t.Fatal("owner did not beat file metadata")
	}
	for k := range owner {
		owner[k] = compactcatalog.ReadingOwnerValue{Value: "", Locked: false}
	}
	got, err = compactcatalog.ReadingEffectiveValues(auto, owner)
	if err != nil || !reflect.DeepEqual(got, file) {
		t.Fatal("owner clear must recover actual file provenance", err)
	}
	got, err = compactcatalog.ReadingAutomaticValues(path, compactcatalog.ReadingValues{})
	if err != nil || !reflect.DeepEqual(got, path) {
		t.Fatal("missing file metadata must keep actual path")
	}
}
