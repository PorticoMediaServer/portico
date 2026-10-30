package persistence

import (
	"context"
	"database/sql/driver"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// catalogueSized are tables whose rows grow with the library (or with a
// library's history). A statement that scans one costs the whole library.
var catalogueSized = []string{
	"catalog_entities", "catalog_identities", "catalog_item_details", "catalog_asset_links", "catalog_assets",
	"catalog_browse_rows", "catalog_browse_memberships", "catalog_browse_member_metrics", "catalog_browse_counted_rows",
	"catalog_episodes", "catalog_songs", "catalog_song_artists", "catalog_book_files", "catalog_albums", "catalog_books",
	"catalog_item_attribute_edges", "catalog_entity_terms", "catalog_term_sources", "catalog_credits",
	"catalog_item_availability", "catalog_search_documents", "catalog_related_facets", "metadata_details",
	"personal_items", "progress", "progress_activity", "personal_history", "inventory_objects",
}

var sqlKeywords = map[string]bool{"where": true, "join": true, "on": true, "indexed": true, "left": true, "cross": true, "inner": true,
	"order": true, "group": true, "limit": true, "using": true, "natural": true, "union": true, "set": true, "values": true, "as": true,
	"not": true, "and": true, "or": true, "having": true, "window": true, "except": true, "intersect": true, "returning": true, "outer": true}

// planScans are the statements whose plan reads a catalogue-sized table
// start to end, as the plan would be with a large library (sqlite_stat1 says
// every catalogue table holds millions of rows).
func planScans(t *testing.T, indexStats bool) map[string]string {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "plans.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	// Millions of rows, and every index selective: a scan that remains is one
	// no index can serve, not a planner guess about small tables.
	for _, table := range catalogueSized {
		if _, err = db.Exec(`INSERT INTO sqlite_stat1(tbl,idx,stat) VALUES(?,NULL,'5000000')`, table); err != nil {
			t.Fatal(err)
		}
		if !indexStats {
			continue
		}
		if _, err = db.Exec(`INSERT INTO sqlite_stat1(tbl,idx,stat)
 SELECT ?1,l.name,'5000000'||(SELECT group_concat(' 2','') FROM pragma_index_info(l.name)) FROM pragma_index_list(?1) l`, table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`ANALYZE sqlite_schema`); err != nil {
		t.Fatal(err)
	}
	statements, _ := productionStatements(t)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sized := map[string]bool{}
	for _, table := range catalogueSized {
		sized[table] = true
	}
	scan := regexp.MustCompile(`^SCAN (\w+)`)
	derived := regexp.MustCompile(`(?i)\)\s*(?:AS\s+)?(\w+)|\b(\w+)\s+AS\s*(?:NOT\s+)?(?:MATERIALIZED\s*)?\(`)
	source := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+(\w+)(?:\s+(?:AS\s+)?(\w+))?`)
	out := map[string]string{}
	for _, s := range statements {
		var plan []string
		err := conn.Raw(func(raw any) error {
			prepared, err := raw.(driver.Conn).Prepare("EXPLAIN QUERY PLAN " + s.text)
			if err != nil {
				return err
			}
			defer prepared.Close()
			// Every parameter NULL, by ordinal: positional ? and numbered ?N alike.
			args := make([]driver.NamedValue, 64)
			for i := range args {
				args[i] = driver.NamedValue{Ordinal: i + 1}
			}
			rows, err := prepared.(driver.StmtQueryContext).QueryContext(context.Background(), args)
			if err != nil {
				return err
			}
			defer rows.Close()
			values := make([]driver.Value, len(rows.Columns()))
			for {
				if err := rows.Next(values); errors.Is(err, io.EOF) {
					return nil
				} else if err != nil {
					return err
				}
				if detail, ok := values[len(values)-1].(string); ok {
					plan = append(plan, detail)
				}
			}
		})
		if err != nil {
			continue // does not prepare here (fragments, run-time tables): the prepare guard owns that
		}
		// The plan names a table by its alias when it has one.
		tables := map[string]string{}
		for _, m := range source.FindAllStringSubmatch(s.text, -1) {
			table := strings.ToLower(m[1])
			tables[table] = table
			if alias := strings.ToLower(m[2]); alias != "" && !sqlKeywords[alias] {
				tables[alias] = table
			}
		}
		// A name that also aliases a subquery or CTE may be the bounded
		// subquery's scan, not the table's.
		for _, m := range derived.FindAllStringSubmatch(s.text, -1) {
			delete(tables, strings.ToLower(m[1]))
		}
		for _, line := range plan {
			m := scan.FindStringSubmatch(line)
			if m == nil || !sized[tables[strings.ToLower(m[1])]] {
				continue
			}
			out[s.where] = line + "\n\t" + strings.Join(strings.Fields(s.text), " ")
			break
		}
	}
	return out
}

// catalogueScanAllowed are statements that may scan a catalogue-sized table,
// matched by a prefix of their text, each with the reason it is not a cost per
// request or per item.
var catalogueScanAllowed = map[string]string{
	"INSERT INTO libraries(":                                                       "creating a library (once) fires a trigger over the play history",
	"INSERT OR IGNORE INTO libraries(":                                             "creating a library (once) fires a trigger over the play history",
	"INSERT INTO catalog_credit_labels":                                            "a credit label is new once per label (Acting, Directing, …)",
	"SELECT library_id,kind,lower(sort_key),entity_id FROM catalog_browse_":        "CheckBrowseBlocks, a diagnostic that reads everything by design",
	"SELECT f.entity_id,f.facet FROM catalog_rec_facets f JOIN catalog_entities e": "CheckRecPostings, a diagnostic that reads everything by design",
}

// A statement that scans a catalogue-sized table costs the whole library on
// every run: Home's card hydration joined browse rows on an expression and
// read all of them for every row of every Home (fixed with this guard). This
// plans every production SQL literal as if the catalogue held millions of rows
// and fails on any such scan not listed above.
func TestProductionStatementsDoNotScanTheCatalogue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	// A fixture's convergence census is intentionally proportional to its tiny
	// seeded database. Keep that SQL out of the production cost model only
	// while no non-test Go source imports the fixture package.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	importers, err := catalogFixtureRuntimeImports(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(importers) != 0 {
		t.Fatalf("catalogue fixture entered production: %v", importers)
	}
	// Two statistics models: table sizes alone, and every index selective. A
	// plan can hide a scan under one and show it under the other (the artwork
	// resolver's did), so a scan under either fails.
	findings := planScans(t, false)
	for where, finding := range planScans(t, true) {
		findings[where] = finding
	}
	for where, finding := range findings {
		text := finding[strings.Index(finding, "\n\t")+2:]
		// The scale benchmark measures the catalogue; it is not a server path.
		allowed := strings.Contains(where, "/scalebench/") || strings.HasPrefix(filepath.ToSlash(where), "internal/catalogtest/")
		for prefix := range catalogueScanAllowed {
			if strings.HasPrefix(text, prefix) {
				allowed = true
			}
		}
		if !allowed {
			t.Errorf("%s scans a catalogue-sized table: %.400s", where, finding)
		}
	}
}

// Go imports are static. Check every non-test file, including all build tags,
// before excluding this one known test-support package's SQL from the model.
func catalogFixtureRuntimeImports(root string) (found []string, err error) {
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, e := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if e != nil {
			return e
		}
		for _, spec := range file.Imports {
			name, e := strconv.Unquote(spec.Path.Value)
			if e != nil {
				return e
			}
			if name == "github.com/PorticoMediaServer/portico/server/internal/catalogtest" {
				rel, e := filepath.Rel(root, path)
				if e != nil {
					return e
				}
				found = append(found, rel)
			}
		}
		return nil
	})
	return found, err
}

func TestCatalogFixtureImportGuardIncludesRuntimeAndBuildTags(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"consumer_test.go": "package consumer; import _ \"github.com/PorticoMediaServer/portico/server/internal/catalogtest\"",
		"runtime.go":       "//go:build future_platform\n\npackage consumer; import _ \"github.com/PorticoMediaServer/portico/server/internal/catalogtest\"",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	found, err := catalogFixtureRuntimeImports(root)
	if err != nil || len(found) != 1 || found[0] != "runtime.go" {
		t.Fatalf("fixture import boundary: %v %v", found, err)
	}
}
