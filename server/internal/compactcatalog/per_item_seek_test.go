package compactcatalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// CaptureFacetProducerFence probes readiness through indexed primary-key
// ranges, and reads the accepted visibility frontier through first_pending's
// index with LIMIT 1. The >= min-int ranges cover every legal integer key and
// stop at the first match; the source target is a captured scalar. None sorts
// or enumerates source, repair or facet work. Pin the complete statement so
// changing any barrier requires reviewing its bounds again. Every base-table
// access remains subject to the ordinary no-SCAN check below.
const facetProducerFenceStatement = `SELECT s.source_epoch,s.projection_epoch,g.generation,CASE WHEN g.retaining=1 THEN g.visibility_sequence ELSE COALESCE((SELECT first_pending-1 FROM compact_visibility_source_jobs WHERE first_pending>=0 ORDER BY first_pending LIMIT 1),(SELECT sequence FROM compact_visibility_source_state WHERE id=1)) END,COALESCE((SELECT version FROM catalog_derivations WHERE domain=33),0),
 g.retaining=1
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=33 AND version=7)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=36 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=37 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=38 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=39 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=40 AND version=1)
 OR EXISTS(SELECT 1 FROM catalog_derivations WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44) AND rebuilding=1)
 OR EXISTS(SELECT 1 FROM catalog_dirty WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44))
 OR EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44))
 OR EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_source_fanouts WHERE domain>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_work WHERE entity_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_work_places WHERE entity_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_stage WHERE entity_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM compact_visibility_source_jobs WHERE first_pending<=(CASE WHEN g.retaining=1 THEN g.visibility_sequence ELSE COALESCE((SELECT first_pending-1 FROM compact_visibility_source_jobs WHERE first_pending>=0 ORDER BY first_pending LIMIT 1),(SELECT sequence FROM compact_visibility_source_state WHERE id=1)) END))
 OR EXISTS(SELECT 1 FROM catalog_browse_row_work WHERE domain IN(18,35))
 OR EXISTS(SELECT 1 FROM catalog_browse_row_keys WHERE domain IN(18,35))
 OR EXISTS(SELECT 1 FROM catalog_item_metric_jobs WHERE item_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_browse_counter_repairs WHERE entity_id>=-9223372036854775808),
 EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44))
 FROM catalog_facet_source_state s CROSS JOIN catalog_generations g WHERE s.singleton=1 AND g.singleton=1`

// collapse folds a statement's whitespace, so a re-indented or re-wrapped
// literal still matches while any change of text does not.
func collapse(query string) string { return strings.Join(strings.Fields(query), " ") }

// Writing and deriving must cost the same per entity in a library of any size.
// Every SQL statement in this package's production code runs per entity, per
// file or per batch; a statement that scans a table makes the whole catalogue
// quadratic. This reads every string literal that is a statement, asks SQLite
// for its plan, and fails on any full scan other than a batch's own json_each
// list, a constant VALUES list or a scan the allow-list below explains.
var drivesFromBatch = regexp.MustCompile(`(?is)\bFROM\s+json_each\(\?\d*\)\s+(\w+)\s+(?:CROSS\s+)?JOIN\b`)

func TestPerItemDerivationStatementsSeek(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Scans that are bounded by something other than an index, with why.
	allowed := map[string]string{
		// catalog_derivations has one row per registered domain.
		"FROM catalog_derivations st":                 "the scheduler reads a table of one row per derived domain",
		"FROM catalog_derivations WHERE rebuilding=1": "one row per derived domain",
		"FROM audio_metadata_policies p":              "one row per audio library",
		// The rating classifier's queue: spellings not yet classified, drained
		// in batches (LIMIT) and probed for emptiness (EXISTS stops at a row).
		"FROM content_rating_pending LIMIT 5000":                    "a bounded batch of a draining queue",
		"entity_id,total FROM catalog_browse_blocks":                "CheckBrowseBlocks: a whole-table diagnostic for tests",
		"FROM catalog_browse_rows ORDER BY library_id,kind":         "CheckBrowseBlocks: a whole-table diagnostic for tests",
		"EXCEPT SELECT library_id,field,value,rating_key,label_key": "CheckFacetCounts: a whole-table diagnostic for tests",
		"FROM catalog_rec_facets f JOIN catalog_entities e":         "CheckRecPostings: a whole-table diagnostic for tests",
		"FROM catalog_rec_postings EXCEPT SELECT":                   "CheckRecPostings: a whole-table diagnostic for tests",
		"EXCEPT SELECT entity_id,facet FROM catalog_rec_postings":   "CheckRecPostings: a whole-table diagnostic for tests",
		"FROM catalog_rec_df EXCEPT SELECT":                         "CheckRecPostings: a whole-table diagnostic for tests",
		"FROM catalog_rec_postings GROUP BY facet EXCEPT":           "CheckRecPostings: a whole-table diagnostic for tests",
		"FROM rec_profile_signals s JOIN catalog_rec_facets f":      "CheckRecTaste: a whole-table diagnostic for tests",
		"SELECT profile_id,facet,long,short FROM rec_profile_taste": "CheckRecTaste: a whole-table diagnostic for tests",
		"SELECT work,facet FROM (":                                  "a work's member facets: the batch's works (json_each), each work's members by index, grouped",
		"SELECT EXISTS(SELECT 1 FROM content_rating_pending)":       "stops at the first queued row",
		// Reading path publication (P2-08): its queues are drained by head reads.
		"FROM reading_path_semantic_pending ORDER BY library_id LIMIT 128":                            "a draining queue's head read in primary-key order, at most 128 rows",
		"SELECT EXISTS(SELECT 1 FROM reading_path_dirty_work)":                                        "stops at the first queued row",
		"INDEXED BY reading_path_build_retired WHERE phase IN('cleanup','abort') ORDER BY id LIMIT 1": "the first row of the retired-build partial index",
		// Inserting a parent row plans a scan of its children, which SQLite runs
		// only while a deferred foreign-key violation is outstanding (FkIfZero);
		// both are also first-use fallbacks, not per-item writes.
		"INSERT INTO catalog_libraries(":     "parent insert; child scan gated by FkIfZero",
		"INSERT INTO catalog_credit_labels(": "parent insert; child scan gated by FkIfZero",
	}
	// A job queue's head read (ORDER BY its key LIMIT n, in index order, no
	// sort) stops after n rows: SQLite calls it a scan, but it is bounded.
	headRead := regexp.MustCompile(`(?is)ORDER BY [\w,\s]+ LIMIT (1|\?)\s*$`)
	statement := regexp.MustCompile(`(?is)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH)\b`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	pinnedSeen := false
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			query, err := strconv.Unquote(lit.Value)
			if err != nil || !statement.MatchString(query) || strings.Contains(query, "%s") {
				return true
			}
			for marker := range allowed {
				if strings.Contains(query, marker) {
					return true
				}
			}
			rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, placeholders(query)...)
			if err != nil {
				// A fragment concatenated with other literals at run time.
				if testing.Verbose() {
					t.Logf("%s: not checked (%v): %.80s", file, err, strings.TrimSpace(query))
				}
				return true
			}
			plan := ""
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan += detail + "\n"
			}
			rows.Close()
			checked++
			// A statement that joins from its batch (FROM json_each(?) j JOIN …)
			// must start from the batch. Every line can be an index search and
			// the statement still grow with the library when the planner starts
			// from a low-selectivity key (every term of a vocabulary) and walks
			// out to the batch; CROSS JOIN fixes the order.
			if m := drivesFromBatch.FindStringSubmatch(query); m != nil {
				lines := strings.Split(strings.TrimSpace(plan), "\n")
				// An UPDATE first seeks its target; the batch drives the IN subquery.
				if strings.HasPrefix(strings.TrimSpace(query), "UPDATE ") {
					for i, line := range lines {
						if strings.HasPrefix(line, "LIST SUBQUERY ") {
							lines = lines[i+1:]
							break
						}
					}
				}
				for _, line := range lines {
					if strings.HasPrefix(line, "SCAN "+m[1]+" ") || line == "SCAN "+m[1] {
						break
					}
					if strings.HasPrefix(line, "SEARCH ") {
						t.Errorf("%s: statement joins from its batch but the plan starts elsewhere (use CROSS JOIN):\n%s\nplan:\n%s", file, strings.TrimSpace(query), plan)
						break
					}
				}
			}
			pinned := collapse(query) == collapse(facetProducerFenceStatement)
			if pinned {
				pinnedSeen = true
			}
			bounded := headRead.MatchString(query) && !strings.Contains(plan, "TEMP B-TREE")
			for _, line := range strings.Split(strings.TrimSpace(plan), "\n") {
				if !strings.HasPrefix(line, "SCAN ") {
					continue
				}
				scanned := strings.Fields(line)[1]
				// A CTE the statement defines is a bounded stream built by its
				// own (checked) lines.
				cte := regexp.MustCompile(`(?i)(WITH|,)\s*` + regexp.QuoteMeta(scanned) + `\s+AS\s*(?:MATERIALIZED\s+)?\(`).MatchString(query)
				switch {
				case cte:
				case strings.HasPrefix(scanned, "(subquery-") && drivesFromBatch.MatchString(query):
					// A bounded batch-derived UNION or DISTINCT result; every base-table
					// access is still independently checked below.
				case scanned == "k" && strings.Contains(query, "FROM (SELECT ? AS profile,? AS item) k"):
					// Exactly one parameter-only row, with no catalogue scan.
				case scanned == "j", scanned == "json_each", strings.HasPrefix(scanned, "CONSTANT"), strings.HasPrefix(line, "SCAN d ") && strings.Contains(query, "(VALUES"):
				case strings.Contains(line, "VIRTUAL TABLE INDEX"), bounded:
				default:
					t.Errorf("%s: statement scans %s:\n%s\nplan:\n%s", file, scanned, strings.TrimSpace(query), plan)
				}
			}
			return true
		})
	}
	if !pinnedSeen {
		t.Fatalf("the recorded producer-fence statement is not in this package: re-review the barrier's bounded EXISTS probes and re-pin it")
	}
	if checked < 50 {
		t.Fatalf("only %d statements were checked; the literal scan is broken", checked)
	}
}

// placeholders is one NULL per parameter of query (plans don't depend on the
// values): the count of bare ? marks, or the highest ?N.
func placeholders(query string) []any {
	bare, numbered := 0, 0
	for i := 0; i < len(query); i++ {
		if query[i] != '?' {
			continue
		}
		j := i + 1
		for j < len(query) && query[j] >= '0' && query[j] <= '9' {
			j++
		}
		if j == i+1 {
			bare++
			continue
		}
		if n, err := strconv.Atoi(query[i+1 : j]); err == nil && n > numbered {
			numbered = n
		}
	}
	return make([]any, max(bare, numbered))
}
