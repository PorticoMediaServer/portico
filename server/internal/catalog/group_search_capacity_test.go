package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestGroupSearchScopeRequiresPositiveConjunction(t *testing.T) {
	group := BrowseNode{Field: "genre", Operator: "contains", Value: "Rare"}
	search := BrowseNode{Field: "search", Operator: "contains", Value: "Title"}
	for _, tc := range []struct {
		name string
		node BrowseNode
		seek bool
	}{
		{"plain", search, false},
		{"group", BrowseNode{All: []BrowseNode{group, search}}, true},
		{"reversed", BrowseNode{All: []BrowseNode{search, group}}, true},
		{"nested", BrowseNode{All: []BrowseNode{{All: []BrowseNode{group}}, search}}, true},
		{"person", BrowseNode{All: []BrowseNode{{Field: "person", Operator: "equals", Value: "person"}, search}}, true},
		{"list", BrowseNode{All: []BrowseNode{{Field: "list", Operator: "equals", Value: "list"}, search}}, true},
		{"alternative", BrowseNode{Any: []BrowseNode{group, search}}, false},
		{"negative", BrowseNode{All: []BrowseNode{{Not: &group}, search}}, false},
		{"negated-conjunction", BrowseNode{Not: &BrowseNode{All: []BrowseNode{group, search}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiler := browseCompiler{}
			where, err := compiler.compile(&tc.node)
			if err != nil || strings.Contains(where, "p.entity_id=e.entity_id") != tc.seek {
				t.Fatalf("where=%s err=%v", where, err)
			}
			// Reusing the compiler must not change an ordinary search's plan.
			where, err = compiler.compile(&search)
			if err != nil || strings.Contains(where, "p.entity_id=e.entity_id") {
				t.Fatalf("scope escaped compilation: %s %v", where, err)
			}
		})
	}
}

func TestGroupedSearchDoesNotMaterializeUnrelatedMatches(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 1000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			c := catalogtest.Open(t)
			lib := c.Library("m", "Movies", "movie", "/m")
			item := c.Movie(lib, "/m/a.mkv", "Title Amélie", 2000)
			other := c.Movie(lib, "/m/b.mkv", "Title Elsewhere", 2000)
			c.Genres(item.ID, "fixture", "Rare")
			c.Genres(other.ID, "fixture", "Other")
			c.Drain()
			// Matching documents outside the entity fixture isolate the FTS match
			// set. They intentionally have no FK targets and must never be returned.
			ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), c.DB, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
			for off := 0; err == nil && off < n; off += 10000 {
				_, err = conn.ExecContext(ctx, `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_search_documents(entity_id,title,title_fold,entity_pid,search,normalized,kind) SELECT i+1000000000,'TitleNoise','titlenoise','fixture','TitleNoise','titlenoise','k7 lnone' FROM ids`, off+1, min(n, off+10000))
			}
			_, restore := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
			release()
			if err != nil || restore != nil {
				t.Fatalf("seed=%v restore=%v", err, restore)
			}
			c.Exec(`ANALYZE`)
			node := BrowseNode{All: []BrowseNode{{Field: "genre", Operator: "contains", Value: "Rare"}, {Field: "search", Operator: "contains", Value: "Title"}}}
			compiler := browseCompiler{}
			where, err := compiler.compile(&node)
			if err != nil {
				t.Fatal(err)
			}
			groupCompiler := browseCompiler{}
			groupWhere, err := groupCompiler.compile(&node.All[0])
			if err != nil {
				t.Fatal(err)
			}
			_, match, err := NormalizeSearch("Title")
			if err != nil {
				t.Fatal(err)
			}
			oldWhere := "(" + groupWhere + ` AND EXISTS(SELECT 1 FROM catalog_search_titles JOIN catalog_search_documents d ON d.entity_id=catalog_search_titles.rowid WHERE catalog_search_titles.rowid=e.entity_id AND catalog_search_titles MATCH ?))`
			oldArgs := append(append([]any{}, groupCompiler.args...), match)
			for _, version := range []struct {
				name, where string
				args        []any
			}{{"before", oldWhere, oldArgs}, {"after", where, compiler.args}} {
				query := `SELECT e.entity_id FROM catalog_browse_rows e WHERE e.library_id=? AND e.kind=1 AND ` + version.where
				args := append([]any{lib}, version.args...)
				plan := seekCapacityPlan(t, c.DB, query, args...)
				start := time.Now()
				rows, err := c.DB.Query(query, args...)
				if err != nil {
					t.Fatal(err)
				}
				ids := []int64{}
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					ids = append(ids, id)
				}
				err = rows.Err()
				rows.Close()
				elapsed := time.Since(start)
				if err != nil || len(ids) != 1 || ids[0] != item.ID {
					t.Fatalf("%s ids=%v err=%v", version.name, ids, err)
				}
				t.Logf("matching_documents=%d %s elapsed=%s plan=%s", n, version.name, elapsed, plan)
				if version.name == "after" && (!strings.Contains(plan, "catalog_search_prefix_rows_entity") || strings.Contains(plan, "catalog_search_titles VIRTUAL") || elapsed > 100*time.Millisecond) {
					t.Fatalf("grouped search did not stay bounded: %s %s", elapsed, plan)
				}
			}
		})
	}
}
