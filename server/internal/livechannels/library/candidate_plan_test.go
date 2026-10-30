package librarychannels

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestCandidateAndGuideStatementsSeek pins NEW-47: a candidate page and a
// guide page reach every row by key, for every rule shape. (It once caught
// LEFT JOINs onto join views that copied every episode or file link of every
// library before the first row; any statement that materialises, builds an
// automatic index or scans a catalogue table fails it the same way.)
func TestCandidateAndGuideStatementsSeek(t *testing.T) {
	f := openFixture(t)
	at := time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC)
	filter := json.RawMessage(`{"field":"year","operator":"equals","value":1985}`)
	rules := map[string]Rule{
		"by title":       {Query: Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}}},
		"episode order":  {Query: Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode", ShowIDs: []string{"s1", "s2"}}},
		"rotate":         {EpisodeMode: "rotate", Query: Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}}},
		"text and pins":  {Query: Query{LibraryIDs: []string{"tv", "movies"}, Kinds: []string{"movie", "episode"}, Text: "night sky", IncludeItemIDs: []string{"s1"}, ExcludeItemIDs: []string{"e9"}}},
		"browse filter":  {Query: Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Filter: filter, Genres: []string{"Drama"}}},
		"rating ordered": {Query: Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "rating", RecentDays: 30}},
	}
	for label, r := range rules {
		where, args, err := selectionSQL(r.Query, at)
		if err != nil {
			t.Fatal(label, err)
		}
		// The by-id page: a chunk of item ids, filtered.
		queryArgs := []any{"m1", "m2", "e1"}
		query := candidateSelect + orderKey(r) + candidateFrom + ` WHERE i.id IN (?,?,?) AND ` + where + ` ORDER BY i.id`
		seekOnly(t, f, "candidate page, "+label, query, append(queryArgs, args...))
	}
	seekOnly(t, f, "guide item facts", guideItemFactsSQL, []any{`["m1","e1"]`})
}

func seekOnly(t *testing.T, f *fixtureLibrary, label, query string, args []any) {
	t.Helper()
	rows, err := f.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	defer rows.Close()
	scan := regexp.MustCompile(`^SCAN (\S+)`)
	plan, bad := []string{}, []string{}
	for rows.Next() {
		var node, parent, unused int
		var detail string
		if err = rows.Scan(&node, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		if strings.Contains(detail, "MATERIALIZE") || strings.Contains(detail, "AUTOMATIC") {
			bad = append(bad, detail)
		}
		if m := scan.FindStringSubmatch(detail); m != nil && m[1] != "json_each" && m[1] != "CONSTANT" {
			bad = append(bad, detail)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("%s reads beyond its keys: %q\nplan:\n%s", label, bad, strings.Join(plan, "\n"))
	}
}
