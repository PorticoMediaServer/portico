package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
)

// Research-only read adapter. The evaluator never imports any server code.
func TestP0VExport(t *testing.T) {
	f, call := buildGoldenFixture(t)
	if os.Getenv("P0V_PLAN_DROP_INDEX") == "1" {
		if _, err := f.db.Exec(`DROP INDEX catalog_term_sources_term`); err != nil {
			t.Fatal(err)
		}
	}
	plans := map[string]any{}
	var read func(q string) []map[string]any
	raw := func(q string) []map[string]any {
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(q)))
		plans[id] = map[string]any{"sql": q, "bindings": []any{}, "eqp": read("EXPLAIN QUERY PLAN " + q), "explain": read("EXPLAIN " + q)}
		return read(q)
	}
	read = func(q string) []map[string]any {
		rows, err := f.db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		out := []map[string]any{}
		for rows.Next() {
			vals := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range vals {
				dest[i] = &vals[i]
			}
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			m := map[string]any{}
			for i, c := range cols {
				if b, ok := vals[i].([]byte); ok {
					m[c] = string(b)
				} else {
					m[c] = vals[i]
				}
			}
			out = append(out, m)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := map[string]any{
		"entities":   raw(`SELECT e.id,pid(e.public_id) AS public,e.kind,l.library_id AS library,e.parent_id,e.title,e.sort_title,e.year,e.added_ms,e.retired,l.retired AS library_retired FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id`),
		"assets":     raw(`SELECT l.entity_id,a.duration,a.size,a.height,a.available AS asset_available,l.available AS link_available FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id`),
		"attributes": raw(`SELECT e.item_id,f.field,t.value_key,e.source_value FROM catalog_item_attribute_edges e JOIN catalog_attribute_terms t ON t.id=e.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id`),
		"ages":       raw(`SELECT * FROM content_rating_ages`),
		"genres":     raw(`SELECT s.entity_id,t.key,COALESCE(s.label_override,t.label) AS label FROM catalog_term_sources s JOIN catalog_terms t ON t.id=s.term_id WHERE t.vocab=1`),
	}
	cases := []map[string]any{}
	add := func(name, method, path, body, viewer string) {
		status, response := call(method, path, body, viewer)
		var doc any
		if err := json.Unmarshal([]byte(response), &doc); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, map[string]any{"name": name, "method": method, "path": path, "body": body, "viewer": viewer, "status": status, "response": doc})
	}
	for _, v := range []string{"owner", "open", "restricted"} {
		for _, p := range []struct{ lib, pivot string }{{f.library.Movies, "movies"}, {f.library.Shows, "episodes"}, {f.library.Music, "songs"}} {
			for _, field := range []string{"title", "year", "duration", "added"} {
				for _, dir := range []string{"asc", "desc"} {
					cursor := ""
					for page := 0; page < 1000; page++ {
						b := fmt.Sprintf(`{"pivot":%q,"sort":[{"field":%q,"direction":%q}],"limit":7,"cursor":%q}`, p.pivot, field, dir, cursor)
						add("browse", "POST", "/v1/libraries/"+p.lib+"/browse", b, v)
						c := cases[len(cases)-1]
						if c["status"] != 200 {
							break
						}
						doc := c["response"].(map[string]any)
						info := doc["pageInfo"].(map[string]any)
						next, _ := info["nextCursor"].(string)
						if next == "" {
							break
						}
						if next == cursor {
							t.Fatal("repeated cursor")
						}
						cursor = next
						if page == 999 {
							t.Fatal("incomplete traversal")
						}
					}
				}
			}
		}
		for _, field := range []string{"genre", "label", "contentRating"} {
			add("facet", "GET", "/v1/libraries/"+f.library.Movies+"/facets?field="+field, "", v)
		}
		for _, term := range []string{"Word001", "Word002", "zznever"} {
			cursor := ""
			for page := 0; page < 1000; page++ {
				add("search", "GET", "/v1/search?q="+term+"&group=movies&limit=40&cursor="+url.QueryEscape(cursor), "", v)
				c := cases[len(cases)-1]
				if c["status"] != 200 {
					t.Fatal(c)
				}
				groups := c["response"].(map[string]any)["groups"].([]any)
				if len(groups) != 1 {
					t.Fatal(groups)
				}
				g := groups[0].(map[string]any)
				next, _ := g["nextCursor"].(string)
				if next == "" {
					break
				}
				if next == cursor {
					t.Fatal("search repeated cursor")
				}
				cursor = next
				if page == 999 {
					t.Fatal("incomplete search")
				}
			}
		}
	}
	out["cases"] = cases
	out["planRegistry"] = plans
	out["engine"] = read(`SELECT sqlite_version() AS version,sqlite_source_id() AS source_id`)
	out["compileOptions"] = read(`PRAGMA compile_options`)
	schema, _ := json.Marshal(read(`SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`))
	out["schemaSHA256"] = fmt.Sprintf("%x", sha256.Sum256(schema))
	out["plannerPragmas"] = map[string]any{"automatic_index": read(`PRAGMA automatic_index`), "temp_store": read(`PRAGMA temp_store`), "page_size": read(`PRAGMA page_size`)}
	out["statistics"] = []map[string]any{}
	if len(read(`SELECT name FROM sqlite_schema WHERE name='sqlite_stat1'`)) > 0 {
		out["statistics"] = read(`SELECT * FROM sqlite_stat1 ORDER BY tbl,idx`)
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("P0V_EXPORT"), b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("exported %d entities and %d HTTP pages", len(out["entities"].([]map[string]any)), len(cases))
}
