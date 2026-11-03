package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// OW-6: "What will change" names the titles a change touches (select.html s8,
// s9, s11), so each scalar value carries a bounded list of its titles.

func TestBulkPreviewAggregateNamesUpToFiveTitlesPerValue(t *testing.T) {
	m := bulkPreviewAggregate{}
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		if err := m.add(ctx, []bulkPreviewObservation{{field: "year", value: "2020", count: 1, title: fmt.Sprint("Episode ", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.add(ctx, []bulkPreviewObservation{{field: "year", value: "2021", count: 1, title: "Please Hold to My Hand"}}); err != nil {
		t.Fatal(err)
	}
	// A grouped count has no single title to name.
	if err := m.add(ctx, []bulkPreviewObservation{{field: "year", value: "2022", count: 3, title: "grouped"}}); err != nil {
		t.Fatal(err)
	}
	out, err := m.result([]RepairFieldSpec{{Field: "year"}})
	if err != nil {
		t.Fatal(err)
	}
	values := out["year"].Values
	if len(values) != 3 {
		t.Fatal(values)
	}
	if v := values[0]; v.Value != "2020" || v.Count != 7 || strings.Join(v.Titles, "|") != "Episode 0|Episode 1|Episode 2|Episode 3|Episode 4" {
		t.Fatalf("a crowded value names five titles and keeps its exact count: %+v", v)
	}
	if v := values[1]; v.Count != 1 || len(v.Titles) != 1 || v.Titles[0] != "Please Hold to My Hand" {
		t.Fatalf("a one-title value is named in full: %+v", v)
	}
	if v := values[2]; v.Count != 3 || len(v.Titles) != 0 {
		t.Fatalf("a grouped count names nobody: %+v", v)
	}
}

func TestBulkPreviewAggregateTitleBudgetDropsTitlesInsteadOfFailing(t *testing.T) {
	m := bulkPreviewAggregate{}
	ctx := context.Background()
	long := strings.Repeat("x", 4000)
	for i := 0; i < 64; i++ {
		if err := m.add(ctx, []bulkPreviewObservation{{field: "year", value: fmt.Sprint(i), count: 1, title: long}}); err != nil {
			t.Fatal(err)
		}
	}
	if m.titleBytes > bulkPreviewTitleBytes {
		t.Fatalf("titles exceeded their budget: %d", m.titleBytes)
	}
	out, err := m.result([]RepairFieldSpec{{Field: "year"}})
	if err != nil {
		t.Fatal(err)
	}
	named := 0
	for _, v := range out["year"].Values {
		named += len(v.Titles)
		if v.Count != 1 {
			t.Fatal(v)
		}
	}
	if named == 0 || named == 64 {
		t.Fatalf("the budget should name some, not all, of 64 long titles: %d", named)
	}
	// Saturating the field releases its titles with its values.
	before := m.titleBytes
	if err = m.add(ctx, []bulkPreviewObservation{{field: "year", value: "overflow", count: 1, title: "x"}}); err != nil {
		t.Fatal(err)
	}
	if m.titleBytes >= before || m.titleBytes != 0 {
		t.Fatalf("a saturated field kept its title budget: %d (was %d)", m.titleBytes, before)
	}
}

func TestBulkMetadataPreviewNamesEachValuesTitles(t *testing.T) {
	service, targets, _ := bulkFixture(t)
	request := catalog.BulkMetadataPreviewRequest{}
	for _, target := range targets {
		request.Targets = append(request.Targets, catalog.BulkMetadataTarget{Kind: target.Kind, ContentKind: "movie", ID: target.ID, LibraryID: "movies"})
	}
	preview, err := service.BulkPreview(context.Background(), previewRequestSource(service, request), previewAuthorize)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, v := range preview.Fields["year"].Values {
		if int64(len(v.Titles)) != v.Count {
			t.Fatalf("a small selection names every title: %+v", v)
		}
		names = append(names, v.Titles...)
	}
	sort.Strings(names)
	if strings.Join(names, "|") != "Before|Beta|Gamma" {
		t.Fatalf("titles came from the selected titles: %v", names)
	}
	for _, entry := range preview.Fields["tags"].Entries {
		if len(entry.Titles) != 0 {
			t.Fatalf("list entries carry no titles: %+v", entry)
		}
	}
}

func TestBulkMetadataPreviewQueryNamesTitlesOfSmallValues(t *testing.T) {
	s, db := editorFixture(t)
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, e := tx.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<12) INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head,year) SELECT randomblob(16),(SELECT id FROM catalog_libraries WHERE library_id='movies'),1,'Named '||x,'named '||x,'N',CASE WHEN x=12 THEN 1999 ELSE 2000 END FROM n`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := catalog.BulkMetadataSelection{SQL: `SELECT id FROM catalog_entities WHERE title LIKE 'Named %'`, Revision: "named"}
	p, err := s.BulkPreview(context.Background(), previewSQLSource(s, selection), previewAuthorize)
	if err != nil {
		t.Fatal(err)
	}
	year := p.Fields["year"]
	if len(year.Values) != 2 {
		t.Fatal(year)
	}
	if v := year.Values[0]; v.Value != "1999" || v.Count != 1 || len(v.Titles) != 1 || v.Titles[0] != "Named 12" {
		t.Fatalf("the one different title is named: %+v", v)
	}
	if v := year.Values[1]; v.Value != "2000" || v.Count != 11 || len(v.Titles) != bulkPreviewTitles {
		t.Fatalf("the shared value names only the first five: %+v", v)
	}
	assertPreviewScratchClean(t, s)
}
