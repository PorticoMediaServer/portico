package httpapi

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// SEC-02 / BE-SRV-01, part three: counts. A restricted profile must not learn
// how many hidden titles there are from a total. Two oracles:
//   - a total beside the one list it counts, with no further page and fewer
//     entries than a row shows (12), must equal the entries shown;
//   - no total anywhere in an answer may exceed the number of titles the
//     profile can see (in that library, for a library route): a larger total
//     counts titles the profile may not see.
func TestRestrictedProfileCountsMatchWhatIsShown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	m := buildRestrictionMatrix(t)
	visible, perLibrary := m.visibleItemCounts(t)
	visiblePeople := m.visiblePeopleCount(t)
	visibleEntities := m.visibleIntegrationEntities(t)
	checked := 0
	check := func(pattern, method, path, body string) {
		status, response := m.call(method, path, body, "restricted")
		if status != 200 {
			return
		}
		var v any
		if json.Unmarshal([]byte(response), &v) != nil {
			return
		}
		for _, finding := range totalsBeyondShown(v, "") {
			t.Errorf("%s: %s %s: %s", pattern, method, path, finding)
		}
		bound := visible
		// Compatibility item aliases share the mixed entity domain. The exact
		// ID/count/offset oracle covers every alias separately.
		route := strings.TrimPrefix(strings.ToLower(strings.TrimPrefix(pattern, "GET ")), "/mediabrowser")
		if route == "/items" || route == "/users/{id}/items" {
			bound = len(visibleEntities)
		}
		for library, n := range perLibrary {
			if strings.Contains(path, "/libraries/"+library+"/") {
				bound = n
			}
		}
		// Integration Items counts catalogue entities, including visible shows and
		// seasons. Keep its exact entity oracle separate from playable-title counts.
		if pattern == "GET /v1/integrations/items" {
			expected := integrationCountMatches(visibleEntities, path)
			bound = len(expected)
			for _, finding := range integrationEntityCountFindings(response, expected) {
				t.Errorf("%s: %s: %s", pattern, path, finding)
			}
		}
		for _, finding := range totalsAbove(v, "", bound, visiblePeople) {
			t.Errorf("%s: %s %s: %s (the profile can see %d titles here)", pattern, method, path, finding, bound)
		}
		checked++
	}
	routes := restrictionRoutes()
	patterns := make([]string, 0, len(routes))
	for pattern := range routes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		route := routes[pattern]
		if route.probe == nil || route.viewer != "" {
			continue
		}
		for _, path := range route.probe(m) {
			check(pattern, "GET", path, "")
		}
	}
	for pattern, bodies := range restrictionReadPOSTs(m) {
		for _, request := range bodies {
			check(pattern, "POST", request.path, request.body)
		}
	}
	if checked < 40 {
		t.Fatalf("only %d answers were checked; the probes or the fixture are wrong", checked)
	}
	t.Logf("%d answers checked for totals beyond what they show", checked)
}

// rowLimit is the fewest entries a row or page shows; a shorter list is
// complete, so its total must match it.
const rowLimit = 12

// totalKeys are the fields that count a list; pageKeys say there is more.
var totalKeys = []string{"totalCount", "total", "TotalRecordCount"}
var pageKeys = []string{"nextCursor", "next", "cursor", "nextSeasonCursor", "nextAfter", "continuation"}

// totalsBeyondShown walks a JSON answer. At every object with exactly one list
// of objects and a total field, and no further page, the total must equal the
// list's length.
func totalsBeyondShown(v any, at string) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for i, e := range x {
			out = append(out, totalsBeyondShown(e, at+"["+itoaTest(i)+"]")...)
		}
	case map[string]any:
		// Whole-selection counts titles partitioned into kind groups, not the
		// number of groups. Its total still goes through totalsAbove below.
		partition := at == "wholeSelection"
		if partition {
			groups, ok := x["types"].([]any)
			total, totalOK := x["total"].(float64)
			sum := float64(0)
			for _, raw := range groups {
				group, groupOK := raw.(map[string]any)
				count, countOK := group["count"].(float64)
				if !groupOK || !countOK || count < 0 || count != float64(int(count)) {
					ok = false
				}
				sum += count
			}
			if !ok || !totalOK || total != sum {
				out = append(out, at+".total does not equal its exact type counts")
			}
		}
		var list []any
		lists := 0
		for _, value := range x {
			if a, ok := value.([]any); ok && len(a) > 0 {
				if _, isObject := a[0].(map[string]any); isObject {
					list = a
					lists++
				}
			}
		}
		more := false
		for _, key := range pageKeys {
			if s, ok := x[key].(string); ok && s != "" && s != "0" {
				more = true
			}
		}
		if truncated, ok := x["truncated"].(bool); ok && truncated {
			more = true
		}
		if lists == 1 && !more && len(list) < rowLimit && !partition {
			for _, key := range totalKeys {
				if total, ok := x[key].(float64); ok && int(total) > len(list) {
					out = append(out, at+"."+key+" is "+itoaTest(int(total))+" but "+itoaTest(len(list))+" entries are shown")
				}
			}
		}
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out, totalsBeyondShown(x[key], strings.TrimPrefix(at+"."+key, "."))...)
		}
	}
	return out
}

func TestWholeSelectionCountOracleUsesExactPartitionAndVisibleBound(t *testing.T) {
	decode := func(raw string) any {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	valid := decode(`{"wholeSelection":{"total":13,"types":[{"kind":"movie","count":10},{"kind":"episode","count":3}]}}`)
	if got := totalsBeyondShown(valid, ""); len(got) != 0 {
		t.Fatal(got)
	}
	for _, raw := range []string{
		`{"wholeSelection":{"total":14,"types":[{"kind":"movie","count":13}]}}`,
		`{"wholeSelection":{"total":1.5,"types":[{"kind":"movie","count":1.5}]}}`,
		`{"wholeSelection":{"total":13,"types":[{"kind":"movie"}]}}`,
	} {
		if got := totalsBeyondShown(decode(raw), ""); len(got) == 0 {
			t.Fatal("invalid aggregate escaped the oracle", raw)
		}
	}
	if got := totalsAbove(valid, "", 12, 0); len(got) == 0 {
		t.Fatal("whole-view total escaped the current-viewer bound")
	}
	if got := totalsBeyondShown(decode(`{"total":13,"entries":[{"id":"one"}]}`), ""); len(got) == 0 {
		t.Fatal("ordinary short list escaped its cardinality check")
	}
}

func itoaTest(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// totalsAbove lists every total in an answer larger than bound (a count of
// titles), or than people for a group or list of people.
func totalsAbove(v any, at string, bound, people int) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for i, e := range x {
			out = append(out, totalsAbove(e, at+"["+itoaTest(i)+"]", bound, people)...)
		}
	case map[string]any:
		for _, key := range []string{"id", "kind", "type"} {
			if s, ok := x[key].(string); ok && (s == "people" || s == "person" || s == "persons") {
				bound = people
			}
		}
		// A facet page's total counts its choices (one title can carry several
		// genres), so it is not bounded by titles; each choice's own count is.
		_, facetField := x["field"].(string)
		choices, facetPage := x["values"].([]any)
		facetPage = facetPage && facetField
		if facetPage {
			for i, choice := range choices {
				if value, ok := choice.(map[string]any); ok {
					if n, ok := value["count"].(float64); ok && int(n) > bound {
						out = append(out, strings.TrimPrefix(at+".values["+itoaTest(i)+"].count", ".")+" is "+itoaTest(int(n)))
					}
				}
			}
		}
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if facetPage && key == "total" {
				continue
			}
			if n, ok := x[key].(float64); ok && (key == "total" || key == "totalCount" || key == "itemCount" || key == "TotalRecordCount") && int(n) > bound {
				out = append(out, strings.TrimPrefix(at+"."+key, ".")+" is "+itoaTest(int(n)))
				continue
			}
			out = append(out, totalsAbove(x[key], strings.TrimPrefix(at+"."+key, "."), bound, people)...)
		}
	}
	return out
}

// visibleItemCounts is how many items the restricted profile can see, in all
// and per library (the matrix's hidden set is computed with the catalogue's
// own visibility predicate).
func (m *restrictionEnv) visibleItemCounts(t *testing.T) (int, map[string]int) {
	t.Helper()
	hidden := map[string]bool{}
	for _, ids := range m.hiddenItems {
		for _, id := range ids {
			hidden[id] = true
		}
	}
	rows, err := m.db.Query(`SELECT pid(e.public_id),cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE k.playable=1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	total, per := 0, map[string]int{}
	for rows.Next() {
		var id, library string
		if err = rows.Scan(&id, &library); err != nil {
			t.Fatal(err)
		}
		if hidden[id] {
			continue
		}
		total++
		per[library]++
	}
	return total, per
}

// visiblePeopleCount is how many people are credited on a title the
// restricted profile can see.
func (m *restrictionEnv) visiblePeopleCount(t *testing.T) int {
	t.Helper()
	hidden := []string{}
	for _, ids := range m.hiddenItems {
		hidden = append(hidden, ids...)
	}
	var n int
	if err := m.db.QueryRow(`SELECT count(DISTINCT c.person_id) FROM catalog_credits c JOIN catalog_entities e ON e.id=c.entity_id JOIN catalog_kinds k ON k.id=e.kind WHERE k.playable=1 AND e.public_id NOT IN(SELECT pid_blob(value) FROM json_each(?))`, mustJSON(hidden)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
