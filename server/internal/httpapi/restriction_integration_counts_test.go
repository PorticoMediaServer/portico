package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// Enumerate maintained identities and apply the public single-item/container
// admission oracle, independently of QueryIntegrationItems and its count SQL.
type integrationCountEntity struct{ Library, Kind string }

func (m *restrictionEnv) visibleIntegrationEntities(t *testing.T) map[string]integrationCountEntity {
	t.Helper()
	rows, err := m.db.Query(`SELECT pid(e.public_id),cl.library_id,k.name,k.container FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_browse_rows b ON b.entity_id=e.id WHERE e.retired=0 AND cl.retired=0`)
	if err != nil {
		t.Fatal(err)
	}
	type candidate struct {
		id, library, kind string
		container         bool
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.library, &c.kind, &c.container); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]integrationCountEntity{}
	kinds := map[string]int{}
	for _, c := range candidates {
		if c.container {
			err = m.catalog.VisibleEntity(context.Background(), m.visibilityOracle, c.kind, c.id)
		} else {
			err = m.catalog.VisibleItem(context.Background(), m.visibilityOracle, c.id)
		}
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out[c.id] = integrationCountEntity{Library: c.library, Kind: c.kind}
		kinds[c.kind]++
	}
	t.Logf("integration count oracle: %d visible entities by kind %v", len(out), kinds)
	return out
}
func integrationCountMatches(visible map[string]integrationCountEntity, path string) map[string]integrationCountEntity {
	u, err := url.Parse(path)
	if err != nil {
		panic(err)
	}
	q := u.Query()
	ids, kinds := map[string]bool{}, map[string]bool{}
	if q.Get("ids") != "" {
		for _, id := range strings.Split(q.Get("ids"), ",") {
			ids[id] = true
		}
	}
	if q.Get("kind") != "" {
		for _, kind := range strings.Split(q.Get("kind"), ",") {
			kinds[kind] = true
		}
	}
	out := map[string]integrationCountEntity{}
	for id, entity := range visible {
		if len(ids) > 0 && !ids[id] || len(kinds) > 0 && !kinds[entity.Kind] || q.Get("libraryId") != "" && q.Get("libraryId") != entity.Library {
			continue
		}
		out[id] = entity
	}
	return out
}
func integrationEntityCountFindings(raw string, expected map[string]integrationCountEntity) []string {
	var page struct {
		Items []struct {
			ID string `json:"id"`
		}
		Total      *int64 `json:"total"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		return []string{"invalid integration page"}
	}
	out := []string{}
	if page.Total == nil || *page.Total != int64(len(expected)) {
		out = append(out, fmt.Sprintf("total does not equal %d independently visible matching entities", len(expected)))
	}
	seen := map[string]bool{}
	for _, item := range page.Items {
		if _, ok := expected[item.ID]; !ok {
			out = append(out, "page contains a hidden or non-matching entity")
		}
		if seen[item.ID] {
			out = append(out, "page contains a duplicate entity")
		}
		seen[item.ID] = true
	}
	if page.NextCursor == "" && len(seen) != len(expected) {
		out = append(out, "complete page omits visible matching entities")
	}
	if page.NextCursor != "" && len(seen) >= len(expected) {
		out = append(out, "continuation has no remaining visible matching entities")
	}
	return out
}
func TestIntegrationCountOracleKeepsEntityUnitsAndExactVisibility(t *testing.T) {
	visible := map[string]integrationCountEntity{"movie": {Library: "a", Kind: "movie"}, "show": {Library: "a", Kind: "show"}, "season": {Library: "a", Kind: "season"}, "other": {Library: "b", Kind: "movie"}}
	expected := integrationCountMatches(visible, "/v1/integrations/items?libraryId=a&kind=movie,show")
	if len(expected) != 2 {
		t.Fatal("wrong filtered entity oracle")
	}
	for _, raw := range []string{`{"total":2,"items":[{"id":"movie"},{"id":"show"}]}`, `{"total":2,"items":[{"id":"movie"}],"nextCursor":"more"}`} {
		if findings := integrationEntityCountFindings(raw, expected); len(findings) != 0 {
			t.Fatal(findings)
		}
	}
	for _, raw := range []string{
		`{"total":3,"items":[{"id":"movie"},{"id":"show"}]}`,
		`{"total":2,"items":[{"id":"movie"},{"id":"hidden"}]}`,
		`{"total":2,"items":[{"id":"movie"},{"id":"season"}]}`,
		`{"total":2,"items":[{"id":"movie"},{"id":"movie"}]}`,
		`{"total":2,"items":[{"id":"movie"}]}`,
		`{"total":2,"items":[{"id":"movie"},{"id":"show"}],"nextCursor":"more"}`,
		`{"items":[{"id":"movie"},{"id":"show"}]}`,
	} {
		if findings := integrationEntityCountFindings(raw, expected); len(findings) == 0 {
			t.Fatal("invalid count escaped exact visible-entity oracle", raw)
		}
	}
}
