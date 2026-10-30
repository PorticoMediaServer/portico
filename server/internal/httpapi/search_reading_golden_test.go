package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// The accepted Reading group is distinct from the fixture's audiobook Books.
// Its migration is installed, but Tiny seeds no ebook/comic/publication rows,
// so both fixture viewers get an available, exactly empty group for every
// recorded query. These literals never call Search or its projection helpers.
func goldenSearchReadingGroup(t *testing.T, f *goldenFixture, c goldenCase, recorded string) string {
	t.Helper()
	if !strings.HasPrefix(c.Name, "search:") || c.Status != 200 {
		return recorded
	}
	if c.Viewer != "open" && c.Viewer != "restricted" {
		t.Fatalf("%s: specify the Reading expectation for this viewer", c.Name)
	}
	var installed, publications, entities int
	if err := f.db.QueryRow(`SELECT
 (SELECT count(*) FROM sqlite_master WHERE type='table' AND name='reading_publications'),
 (SELECT count(*) FROM reading_publications),
 (SELECT count(*) FROM catalog_entities WHERE kind IN(20,21))`).Scan(&installed, &publications, &entities); err != nil {
		t.Fatal(err)
	}
	if installed != 1 || publications != 0 || entities != 0 {
		t.Fatalf("%s: specify Reading expectations for the changed fixture", c.Name)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(recorded), &doc); err != nil {
		t.Fatal(err)
	}
	insert := func(raw any, addition any) []any {
		entries, ok := raw.([]any)
		if !ok {
			t.Fatalf("%s: recorded Search group list missing", c.Name)
		}
		out := make([]any, 0, len(entries)+1)
		inserted := false
		for _, entry := range entries {
			id, stringID := entry.(string)
			if !stringID {
				object, ok := entry.(map[string]any)
				if !ok {
					t.Fatalf("%s: invalid recorded Search group", c.Name)
				}
				id, ok = object["id"].(string)
				if !ok {
					t.Fatalf("%s: recorded Search group id missing", c.Name)
				}
			}
			if id == "reading" || (id == "books" && inserted) {
				t.Fatalf("%s: Reading extension already recorded or audiobook group duplicated", c.Name)
			}
			out = append(out, entry)
			if id == "books" {
				out = append(out, addition)
				inserted = true
			}
		}
		if !inserted {
			t.Fatalf("%s: recorded audiobook group missing", c.Name)
		}
		return out
	}
	capabilities, ok := doc["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("%s: recorded Search capabilities missing", c.Name)
	}
	query, ok := doc["query"].(map[string]any)
	if !ok {
		t.Fatalf("%s: recorded Search query missing", c.Name)
	}
	capabilities["groups"] = insert(capabilities["groups"], map[string]any{
		"id": "reading", "entityKind": "reading", "labelKey": "search.chip.reading", "title": "Books",
		"available": true, "sorts": []any{"relevance", "title", "releaseYear", "dateAdded"},
	})
	doc["groups"] = insert(doc["groups"], map[string]any{
		"id": "reading", "entityKind": "reading", "labelKey": "search.chip.reading", "title": "Books",
		"status": "success", "errorCode": "", "items": []any{}, "totalCount": 0,
		"totalCountExact": true, "hasMore": false, "nextCursor": "",
	})
	query["groups"] = insert(query["groups"], "reading")
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}
