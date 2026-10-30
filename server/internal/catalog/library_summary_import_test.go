package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestReadingSummaryDoesNotRankPublicationAvailability(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	// Several linked versions, including unavailable ones, still count once.
	asset, _ := c.File(items[0].ID, "/extra-version.pdf", 0)
	c.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, items[0].ID, asset)
	ctx, statements := dbwork.TraceQueryPlans(context.Background())
	got, err := s.SummarizeLibraries(ctx, viewer, []Library{{ID: "reading", Kind: "book"}})
	if err != nil {
		t.Fatal(err)
	}
	assertReadingSummaryPreparing(t, got["reading"], 4)
	checked := false
	for _, q := range statements() {
		if strings.Contains(q, "WITH candidates AS") {
			checked = true
			if strings.Contains(q, "inventory_item_availability") || strings.Contains(q, "ORDER BY EXISTS") {
				t.Fatal("title summary invoked the canonical availability ranking", q)
			}
		}
	}
	if !checked {
		t.Fatal("summary candidate query was not observed")
	}
	// The FK, rather than an assumption about ingestion order, is what makes
	// the reduction valid: removing every link also removes its publications.
	c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=?`, items[0].ID)
	got, err = s.SummarizeLibraries(context.Background(), viewer, []Library{{ID: "reading", Kind: "book"}})
	if err != nil {
		t.Fatal(err)
	}
	assertReadingSummaryPreparing(t, got["reading"], 3)
	viewer.Restrictions = identity.ContentRestrictions{BlockUnrated: true}
	got, err = s.SummarizeLibraries(context.Background(), viewer, []Library{{ID: "reading", Kind: "book"}})
	if err != nil {
		t.Fatal(err)
	}
	assertReadingSummaryPreparing(t, got["reading"], 0)
}

func TestReadingSummariesReuseMemberLookupAcrossLibraries(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	viewer.Libraries = []string{"reading", "other"}
	ctx, statements := dbwork.TraceQueryPlans(context.Background())
	got, err := s.SummarizeLibraries(ctx, viewer, []Library{{ID: "reading", Kind: "book"}, {ID: "other", Kind: "book"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["reading"].Counts[0].Count != 4 || got["other"].Counts[0].Count != 0 || !got["other"].EmptyKnown {
		t.Fatal("bound library/member scopes crossed", got)
	}
	var queries []string
	for _, q := range statements() {
		if strings.Contains(q, "WITH candidates AS") {
			queries = append(queries, q)
		}
	}
	if len(queries) != 2 || queries[0] != queries[1] {
		t.Fatalf("library-scoped values forced separate prepared bytecode: %v", queries)
	}
}

// The real eleven-library global composition, with a cold 6,000-title import
// and no ready image. This is a read-cost receipt (the existing dbwork Measure
// surface), not a scale/load campaign or a machine-dependent CI timeout gate.
// The Contabo target is well below 500ms for this entire composition.
func TestElevenLibraryImportSummaryReadCost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	kinds := []string{"movie", "tv", "anime", "music", "audiobook", "book", "comic", "book", "book", "book", "book"}
	var libraries []Library
	var viewer Viewer
	var large int64
	for i, kind := range kinds {
		id := fmt.Sprintf("summary-%d", i)
		handle := c.Library(id, id, kind, "/"+id)
		libraries = append(libraries, Library{ID: id, Kind: kind})
		viewer.Libraries = append(viewer.Libraries, id)
		if i == len(kinds)-1 {
			large = handle
			continue
		}
		// A non-Reading library must remain usable beside the importing one.
		if kind == "movie" {
			c.Entity(compactcatalog.Entity{Library: handle, Kind: compactcatalog.Movie, Key: "movie", Title: "Visible movie"}, nil)
		}
	}
	// Synchronous fixture facts only: do not run the worker, since this is the
	// real first-import shape the summary must serve without waiting for it.
	c.Exec(`WITH RECURSIVE seq(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<6000)
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,added_ms)
 SELECT 100000+n,randomblob(16),?,20,'Book '||n,'book '||n,'B',n FROM seq`, large)
	c.Exec(`WITH RECURSIVE seq(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<6000)
 INSERT INTO catalog_assets(id,token,path,size,modified_ns,available,container,video_codec,audio_codec,width,height,duration)
 SELECT 100000+n,'summary-asset-'||n,'/summary-import/'||n||'.pdf',10,1,0,'pdf','','',0,0,0 FROM seq`)
	c.Exec(`INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT id,id FROM catalog_entities WHERE library_id=?`, large)
	c.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) SELECT id,id,'pdf' FROM catalog_entities WHERE library_id=?`, large)
	ctx, cost := dbwork.Measure(context.Background())
	started := time.Now()
	got, err := New(c.DB).SummarizeLibraries(ctx, viewer, libraries)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 11 {
		t.Fatalf("global list lost libraries: %d", len(got))
	}
	assertReadingSummaryPreparing(t, got["summary-10"], 128)
	if got["summary-0"].Counts[0].Count != 1 || got["summary-0"].Empty {
		t.Fatal("import blocked another library", got["summary-0"])
	}
	t.Logf("eleven-library global summary: elapsed=%s cost=%+v (Contabo target <500ms)", elapsed, cost())
}

// Import summaries promise a lower bound, so neither a long eligible suffix nor
// an empty raw prefix may force a whole-library eligibility search.
func TestPendingReadingSummaryBoundsRawWindowAndNeverClaimsAnEmptyTail(t *testing.T) {
	c := catalogtest.Open(t)
	handle := c.Library("pending", "Pending", "book", "/pending")
	viewer := Viewer{Libraries: []string{"pending"}}
	s := New(c.DB)
	seed := func(start, n int, eligible bool) {
		t.Helper()
		c.Exec(`WITH RECURSIVE seq(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?)
   INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,added_ms)
   SELECT ?+n,randomblob(16),?,20,'Book '||n,'book '||n,'B',?+n FROM seq`, n, start, handle, start)
		if eligible {
			c.Exec(`WITH RECURSIVE seq(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?)
    INSERT INTO catalog_assets(id,token,path,size,modified_ns,available,container,video_codec,audio_codec,width,height,duration)
    SELECT ?+n,'pending-'||(?+n),'/pending/'||(?+n)||'.pdf',10,1,0,'pdf','','',0,0,0 FROM seq`, n, start, start, start)
			c.Exec(`INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT id,id FROM catalog_entities WHERE library_id=? AND id>? AND id<=?`, handle, start, start+n)
			c.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) SELECT id,id,'pdf' FROM catalog_entities WHERE library_id=? AND id>? AND id<=?`, handle, start, start+n)
		}
	}
	read := func() (LibrarySummary, dbwork.Cost) {
		t.Helper()
		ctx, cost := dbwork.Measure(context.Background())
		out, err := s.SummarizeLibraries(ctx, viewer, []Library{{ID: "pending", Kind: "book"}})
		if err != nil {
			t.Fatal(err)
		}
		return out["pending"], cost()
	}
	seed(100000, 256, true)
	first, cost := read()
	assertReadingSummaryPreparing(t, first, 128)
	if first.Empty {
		t.Fatal("eligible raw prefix was called empty")
	}
	// More catalogue rows must not increase the raw candidate work.
	seed(101000, 512, true)
	larger, moreCost := read()
	assertReadingSummaryPreparing(t, larger, 128)
	if moreCost.VMSteps > cost.VMSteps+5000 {
		t.Fatalf("walk grew with the tail: %+v -> %+v", cost, moreCost)
	}
	// Put more than a full raw window of publication-less titles in front of
	// every eligible title. Its empty prefix is UNKNOWN, never an exact zero.
	seed(102000, 256, false)
	hidden, _ := read()
	if hidden.EmptyKnown || !hidden.Counts[0].AtLeast || hidden.Counts[0].Count != 0 {
		t.Fatalf("ineligible prefix claimed the eligible tail empty: %+v", hidden)
	}
	t.Logf("bounded pending raw windows: initial=%+v larger=%+v", cost, moreCost)
}
