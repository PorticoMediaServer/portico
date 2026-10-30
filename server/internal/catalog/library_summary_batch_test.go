package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLibrarySummaryBatchStatementCountIsFlatFromTwoToEleven(t *testing.T) {
	c := catalogtest.Open(t)
	var libraries []Library
	viewer := Viewer{}
	for i := 0; i < 11; i++ {
		id := fmt.Sprintf("batch-%d", i)
		handle := c.Library(id, id, "movie", "/"+id)
		c.Entity(compactcatalog.Entity{Library: handle, Kind: compactcatalog.Movie, Key: id, Title: id}, nil)
		libraries = append(libraries, Library{ID: id, Kind: "movie"})
		viewer.Libraries = append(viewer.Libraries, id)
	}
	read := func(n int) dbwork.Cost {
		t.Helper()
		s := New(c.DB)
		ctx, cost := dbwork.Measure(context.Background())
		got, err := s.SummarizeLibraryList(ctx, viewer, libraries[:n])
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n {
			t.Fatalf("libraries=%d want%d", len(got), n)
		}
		for _, lib := range libraries[:n] {
			if got[lib.ID].Counts[0].Count != 1 || got[lib.ID].Empty {
				t.Fatal("library crossed", lib, got)
			}
		}
		return cost()
	}
	two, eleven := read(2), read(11)
	if two.Statements != eleven.Statements || eleven.Statements > 12 || two.Transactions != 1 || eleven.Transactions != 1 || two.Acquisitions != 1 || eleven.Acquisitions != 1 {
		t.Fatalf("composition is not flat/single snapshot: two=%+v eleven=%+v", two, eleven)
	}
	t.Logf("two=%+v eleven=%+v", two, eleven)
}

func TestLibrarySummaryBatchMatchesReadingPrivacyAndLiveArtwork(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	libs := []Library{{ID: "reading", Kind: "book"}, {ID: "other", Kind: "book"}}
	viewer.Libraries = []string{"reading", "other"}
	compare := func() {
		t.Helper()
		before, err := s.SummarizeLibraries(context.Background(), viewer, libs)
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.SummarizeLibraryList(context.Background(), viewer, libs)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if string(a) != string(b) {
			t.Fatalf("summary drift: old=%s new=%s", a, b)
		}
	}
	compare()
	viewer.Restrictions = identity.ContentRestrictions{BlockUnrated: true}
	compare()
	viewer.Restrictions = identity.ContentRestrictions{}
	c.Drain()
	compare()
}

func TestLibrarySummaryBatchKeepsPublicationExistenceAndMemberPointGuard(t *testing.T) {
	_, s, viewer, _ := readingBrowseFixture(t, "book")
	ctx, statements := dbwork.TraceQueryPlans(context.Background())
	_, err := s.SummarizeLibraryList(ctx, viewer, []Library{{ID: "reading", Kind: "book"}})
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	for _, query := range statements() {
		if !strings.Contains(query, "kind_requests AS MATERIALIZED") {
			continue
		}
		checked = true
		if strings.Contains(query, "inventory_item_availability") || strings.Contains(query, "ORDER BY EXISTS") {
			t.Fatal("summary invoked canonical availability ranking", query)
		}
		if strings.Count(query, "LEFT JOIN reading_attribution_members summary_member") != 1 || !strings.Contains(query, "summary_member.item_id=e.id") {
			t.Fatal("summary lost its single member point lookup", query)
		}
	}
	if !checked {
		t.Fatal("batched candidate query not observed")
	}
}

func TestLibrarySummaryBatchMatchesAllKindsAndLiveDecoration(t *testing.T) {
	c := catalogtest.Open(t)
	s := New(c.DB)
	var libraries []Library
	viewer := Viewer{}
	for _, kind := range []string{"movie", "tv", "anime", "music", "audiobook"} {
		handle := c.Library(kind, kind, kind, "/"+kind)
		libraries = append(libraries, Library{ID: kind, Kind: kind})
		viewer.Libraries = append(viewer.Libraries, kind)
		for i, k := range summaryKinds(kind) {
			code, err := compactcatalog.ParseKind(k)
			if err != nil {
				t.Fatal(err)
			}
			item := c.Entity(compactcatalog.Entity{Library: handle, Kind: code, Key: fmt.Sprint(i), Title: k}, nil)
			if k == "movie" {
				c.Exec(`INSERT INTO catalog_item_details(entity_id,poster_url,backdrop_url) VALUES(?,'poster','backdrop') ON CONFLICT(entity_id) DO UPDATE SET poster_url=excluded.poster_url,backdrop_url=excluded.backdrop_url`, item.ID)
			}
		}
	}
	for _, restricted := range []bool{false, true} {
		viewer.Restrictions.BlockUnrated = restricted
		before, err := s.SummarizeLibraries(context.Background(), viewer, libraries)
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.SummarizeLibraryList(context.Background(), viewer, libraries)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if string(a) != string(b) {
			t.Fatalf("restricted=%v old=%s new=%s", restricted, a, b)
		}
	}
	// A detail-only artwork change must remain live even when the core is reused.
	c.Exec(`UPDATE catalog_item_details SET poster_url='',backdrop_url=''`)
	viewer.Restrictions = identity.ContentRestrictions{}
	before, err := s.SummarizeLibraries(context.Background(), viewer, libraries)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.SummarizeLibraryList(context.Background(), viewer, libraries)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatalf("decoration old=%s new=%s", a, b)
	}
}

func TestLibrarySummaryBatchRawHeadsUseTheOrderedPrefix(t *testing.T) {
	c := catalogtest.Open(t)
	handle := c.Library("raw", "Raw", "movie", "/raw")
	raw, _ := json.Marshal([]summaryBatchInput{{Library: "raw", Kind: "movie", Handle: handle}})
	rows, err := c.DB.Query(`EXPLAIN QUERY PLAN `+summaryBatchCandidates+`SELECT id FROM candidates`, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	heads := 0
	entityPoints := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "picked") {
			if !strings.Contains(detail, "SEARCH picked USING COVERING INDEX catalog_entities_summary_added (library_id=? AND kind=? AND retired=?)") {
				t.Fatal("raw head lost ordered prefix", detail)
			}
			heads++
		}
		if strings.Contains(detail, "SEARCH e USING INTEGER PRIMARY KEY") {
			entityPoints++
		}
		if strings.Contains(detail, "SCAN e") || strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal("unbounded raw candidate plan", detail)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if heads != 2 || entityPoints != 2 {
		t.Fatalf("heads=%d point-expansions=%d", heads, entityPoints)
	}
}

func TestLibrarySummaryBatchPendingWindowStaysBounded(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	handle := c.Handle("reading")
	// No publication exists for these extra raw candidates; the supported rows
	// below the bounded prefix must not turn the pending summary into exact zero.
	appendRows := func(n int) {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.Exec(`WITH RECURSIVE seq(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?) INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head,added_ms) SELECT randomblob(16),?,(SELECT id FROM catalog_kinds WHERE name='ebook'),'Raw '||n,'raw '||n,'R',100000+n FROM seq`, n, handle)
			return err
		})
	}
	read := func() dbwork.Cost {
		ctx, cost := dbwork.Measure(context.Background())
		got, err := s.SummarizeLibraryList(ctx, viewer, []Library{{ID: "reading", Kind: "book"}})
		if err != nil {
			t.Fatal(err)
		}
		summary := got["reading"]
		if summary.EmptyKnown || len(summary.Counts) != 1 || !summary.Counts[0].AtLeast {
			t.Fatal("pending window claimed exact emptiness", summary)
		}
		return cost()
	}
	appendRows(256)
	small := read()
	appendRows(512)
	large := read()
	if large.VMSteps > small.VMSteps+100 {
		t.Fatalf("pending work grew with the library: small=%+v large=%+v", small, large)
	}
	t.Logf("256=%+v 768=%+v", small, large)
}
