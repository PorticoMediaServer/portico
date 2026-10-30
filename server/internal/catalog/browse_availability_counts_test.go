package catalog

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
)

func TestAvailabilityCountersMatchRestrictedPages(t *testing.T) {
	db, s, _ := browseFixture(t)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	age := 13
	viewers := []Viewer{{Profile: "p", Fence: "open", Libraries: []string{"a"}}, {Profile: "p", Fence: "pg", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}}}
	if err := s.RebuildVisibilityClass(context.Background(), "a", viewers[1].Restrictions); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range viewers {
		for _, state := range []string{"available", "partial", "unavailable"} {
			node, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"availability","operator":"equals","value":"`+state+`"}`), "query")
			if err != nil {
				t.Fatal(err)
			}
			compiler := browseCompiler{}
			clause, err := compiler.compile(node)
			if err != nil {
				t.Fatal(err)
			}
			restriction, restrictionArgs := EntityRestrictionSQL("e.entity_id", viewer.Restrictions)
			args := append([]any{"a"}, compiler.args...)
			args = append(args, restrictionArgs...)
			var expected int
			if err = db.QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=1 AND `+clause+` AND `+restriction, args...).Scan(&expected); err != nil {
				t.Fatal(err)
			}
			out, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Query: node, Limit: 40})
			if err != nil || out.PageInfo.Total != expected || len(out.Entries) != expected {
				t.Fatalf("fence=%s state=%s expected=%d out=%+v err=%v", viewer.Fence, state, expected, out, err)
			}
			// A direct position lands where the first page put the title.
			for start := 1; start < expected; start++ {
				deep, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Query: node, Limit: 1, Range: &BrowseRange{Start: start}})
				if err != nil || len(deep.Entries) != 1 || deep.Entries[0].ID != out.Entries[start].ID {
					t.Fatalf("fence=%s state=%s start=%d: %+v %v", viewer.Fence, state, start, deep.Entries, err)
				}
			}
		}
	}
	var rollup, rows int
	if err := db.QueryRow(`SELECT sum(total) FROM catalog_browse_availability_counts WHERE container=-1`).Scan(&rollup); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT count(*) FROM catalog_browse_rows`).Scan(&rows)
	if rollup != rows {
		t.Fatalf("rollup=%d rows=%d", rollup, rows)
	}
}

func TestColdRestrictedBrowseDoesNotBlankLargeLibraries(t *testing.T) {
	db, s, _ := browseFixture(t)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A genuinely large cold library previously returned an empty page while
	// its class was queued. Seed the facts and membership used by permission SQL.
	for _, statement := range []string{
		`WITH RECURSIVE ids(i) AS(SELECT 1000001 UNION ALL SELECT i+1 FROM ids WHERE i<1001100) INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,year) SELECT i,randomblob(16),(SELECT id FROM catalog_libraries WHERE library_id='a'),1,'Bulk Title','Bulk Title','B',2000 FROM ids`,
		`INSERT INTO catalog_item_details(entity_id) SELECT id FROM catalog_entities WHERE id>1000000`,
		`INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) SELECT id,(SELECT id FROM catalog_attribute_terms WHERE field_id=1 AND value_key='pg'),'PG',id FROM catalog_entities WHERE id>1000000`,
		`INSERT INTO catalog_browse_rows(entity_id,library_id,kind,title,sort_key,head,year,item_id,rating_key,label_key,available,recent,backdrop) SELECT id,library_id,kind,title,sort_key,sort_head,year,id,'pg','',0,0,0 FROM catalog_entities WHERE id>1000000`,
		`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) SELECT id,id,0 FROM catalog_entities WHERE id>1000000`,
		`INSERT INTO catalog_browse_buckets(library_id,kind,container,axis,value,rating_key,label_key,total) SELECT id,1,0,0,'B','pg','',1100 FROM catalog_libraries WHERE library_id='a'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	age := 13
	viewer := Viewer{Profile: "cold", Fence: "cold", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}}
	out, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 40})
	if err != nil || len(out.Entries) != 40 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
