package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Extend the recorded answers for CB-1/2/3/6/7 without re-recording them. These
// expectations read fixture facts directly, never Browse/Facets or the actual
// response. Recorded membership, ordering, counts and unrelated fields stay exact.
func goldenBrowseViewer(f *goldenFixture, viewer string) catalog.Viewer {
	v := catalog.Viewer{Profile: "golden-open-profile", Libraries: []string{f.library.Movies, f.library.Shows, f.library.Music, f.library.Books}}
	if viewer == "restricted" {
		r := fixture.Restriction(f.library)
		v.Profile = "golden-limited-profile"
		v.Libraries = r.Libraries
		v.Restrictions = identity.ContentRestrictions{MaximumAge: &r.MaximumAge, BlockUnrated: true, BlockedLabels: r.BlockedLabels}
	}
	return v
}

type goldenPlayable struct {
	id            int64
	public, title string
	year          int
	seconds       float64
}

func goldenBrowsePlayables(t *testing.T, f *goldenFixture, c goldenCase) []goldenPlayable {
	t.Helper()
	library := strings.Split(strings.TrimPrefix(c.Path, "/v1/libraries/"), "/")[0]
	var request struct {
		Pivot string              `json:"pivot"`
		Query *catalog.BrowseNode `json:"query"`
	}
	if err := json.Unmarshal([]byte(c.Body), &request); err != nil {
		t.Fatal(err)
	}
	query, err := json.Marshal(request.Query)
	if err != nil {
		t.Fatal(err)
	}
	key := library + ":" + request.Pivot + ":" + c.Viewer + ":" + string(query)
	if f.browsePlayables == nil {
		f.browsePlayables = map[string][]goldenPlayable{}
	}
	if cached, ok := f.browsePlayables[key]; ok {
		return cached
	}
	rows, err := f.db.Query(`SELECT e.id,pid(e.public_id),e.title,e.year,COALESCE((SELECT max(a.duration) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=e.id),0) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE l.library_id=? AND e.kind IN(1,4,7,9)`, library)
	if err != nil {
		t.Fatal(err)
	}
	all := []goldenPlayable{}
	for rows.Next() {
		var p goldenPlayable
		if err = rows.Scan(&p.id, &p.public, &p.title, &p.year, &p.seconds); err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	visible := []goldenPlayable{}
	viewer := goldenBrowseViewer(f, c.Viewer)
	for _, p := range all {
		if catalog.New(f.db).VisibleItem(context.Background(), viewer, p.public) != nil {
			continue
		}
		if request.Pivot == "collections" {
			var n int
			if err = f.db.QueryRow(`SELECT count(*) FROM catalog_collection_members WHERE item_id=?`, p.id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				continue
			}
		}
		if q := request.Query; q != nil {
			keep := true
			switch q.Field {
			case "year":
				keep = p.year >= 2000
			case "title":
				keep = strings.HasPrefix(p.title, "W")
			case "genre":
				var n int
				err = f.db.QueryRow(`SELECT count(*) FROM catalog_term_sources s JOIN catalog_terms t ON t.id=s.term_id WHERE s.entity_id=? AND t.vocab=1 AND COALESCE(s.label_override,t.label)='Drama'`, p.id).Scan(&n)
				keep = n > 0
			case "contentRating", "label":
				rows, e := f.db.Query(`SELECT edge.source_value FROM catalog_item_attribute_edges edge JOIN catalog_attribute_terms term ON term.id=edge.term_id JOIN catalog_attribute_fields field ON field.id=term.field_id WHERE edge.item_id=? AND field.field=?`, p.id, q.Field)
				if e != nil {
					t.Fatal(e)
				}
				keep = false
				for rows.Next() {
					var value string
					if e = rows.Scan(&value); e != nil {
						t.Fatal(e)
					}
					if q.Field == "label" && value == "violence" || q.Field == "contentRating" && (value == "G" || value == "PG") {
						keep = true
					}
				}
				err = rows.Close()
			case "playState", "availability": // golden profiles have no personal state; all fixture files are live.
			default:
				t.Fatalf("specify independent golden predicate %s", q.Field)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !keep {
				continue
			}
		}
		visible = append(visible, p)
	}
	f.browsePlayables[key] = visible
	return visible
}

func goldenBrowseExtension(t *testing.T, f *goldenFixture, c goldenCase, want string) string {
	t.Helper()
	var doc map[string]any
	if json.Unmarshal([]byte(want), &doc) != nil {
		return want
	}
	if strings.HasPrefix(c.Name, "browse:") && c.Status == 200 {
		page, ok := doc["pageInfo"].(map[string]any)
		if !ok {
			return want
		}
		duration := float64(0)
		lower := false
		if page["revision"] != "" && page["revision"] != nil {
			for _, p := range goldenBrowsePlayables(t, f, c) {
				duration += p.seconds * 1000
				lower = lower || p.seconds <= 0
			}
		}
		page["durationMs"], page["durationIsLowerBound"] = duration, lower
		goldenWholeSelection(t, f, c, doc, page)
		if strings.HasPrefix(c.Name, "browse:query:") {
			anchors := []any{}
			if page["total"].(float64) > 0 {
				anchors = append(anchors, map[string]any{"key": "W", "index": 0})
			}
			doc["positionIndex"] = anchors
		}
	}
	if strings.HasPrefix(c.Name, "facets:") && c.Status == 200 {
		doc["totalIsLowerBound"] = false
		for _, v := range doc["values"].([]any) {
			v.(map[string]any)["countIsLowerBound"] = false
		}
	}
	if strings.HasPrefix(c.Name, "capabilities/") && c.Status == 200 {
		fields := doc["fields"].([]any)
		match := map[string]any{"id": "match", "labelKey": "browse.field.match", "type": "enum", "operators": []string{"equals", "not-equals", "in", "not-in"}, "controlHint": "select", "complexity": "standard", "cost": "indexed-join", "applicableKinds": []string{"movie", "show", "season", "episode", "artist", "album", "song"}, "allowedValues": []string{"matched", "unmatched"}}
		next := []any{fields[0], match}
		next = append(next, fields[1:]...)
		doc["fields"] = next
		for _, v := range next {
			field := v.(map[string]any)
			id := field["id"]
			// The approved relative-date vocabulary adds this operator to
			// these three existing date fields. Preserve every recorded
			// operator and all unrelated capability facts exactly.
			switch id {
			case "releaseDate", "dateAdded", "lastPlayedAt":
				field["operators"] = append([]any{"within-last-days"}, field["operators"].([]any)...)
			}
			if id == "actor" || id == "director" || id == "writer" {
				field["controlHint"] = "facet-multi-select"
				field["facetSource"] = map[string]any{"endpoint": "/v1/libraries/{id}/facets", "field": id}
			}
		}
		for _, v := range doc["sorts"].([]any) {
			s := v.(map[string]any)
			s["ownerOnly"] = s["id"] == "fileSize"
		}
		addMatch := func(p map[string]any) {
			old := p["filters"].([]any)
			if len(old) == 0 {
				return
			}
			n := []any{old[0], "match"}
			p["filters"] = append(n, old[1:]...)
		}
		for _, v := range doc["pivots"].([]any) {
			p := v.(map[string]any)
			if p["id"] != "collections" {
				addMatch(p)
			}
		}
		addMatch(doc["resolvedPivot"].(map[string]any))
		for _, v := range doc["surfaces"].([]any) {
			addMatch(v.(map[string]any))
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}

// The Select-all descriptor counts the independent fixture's authorized
// complete view. It must never be inferred from this response's loaded window.
func goldenWholeSelection(t *testing.T, f *goldenFixture, c goldenCase, doc, page map[string]any) {
	t.Helper()
	var request struct {
		Pivot               string `json:"pivot"`
		Range, Seek, Cursor json.RawMessage
	}
	if err := json.Unmarshal([]byte(c.Body), &request); err != nil {
		t.Fatal(err)
	}
	kind := map[string]string{"movies": "movie", "episodes": "episode", "songs": "song"}[request.Pivot]
	if kind == "" || len(request.Range) > 0 || len(request.Seek) > 0 || len(request.Cursor) > 0 || page["revision"] == nil || page["revision"] == "" {
		return
	}
	library := strings.Split(strings.TrimPrefix(c.Path, "/v1/libraries/"), "/")[0]
	applied := doc["applied"].(map[string]any)
	query := map[string]any{"libraryId": library, "pivot": request.Pivot, "sort": applied["sort"]}
	if applied["query"] != nil {
		query["filter"] = applied["query"]
	}
	n := len(goldenBrowsePlayables(t, f, c))
	types := []any{}
	if n > 0 {
		// Both fixed viewers are members; this fixture installs neither a
		// bulk scheduler, downloads nor owner mutation services. All its
		// visible files are available, so only Play and Queue are offered.
		counts := map[string]any{"play": n, "queue": n, "playlist": 0, "myList": 0, "watched": 0, "collection": 0, "favorite": 0, "download": 0, "edit": 0, "refresh": 0, "delete": 0}
		types = append(types, map[string]any{"kind": kind, "count": n, "capabilityCounts": counts, "libraries": []any{map[string]any{"libraryId": library, "count": n}}})
	}
	// The raw revision equality is checked before viewer-bound browse
	// digests are normalized in TestReadPathGoldenEquivalence.
	doc["wholeSelection"] = map[string]any{"selector": map[string]any{"query": query}, "catalogRevision": page["revision"], "total": n, "types": types}
}

func goldenNewBrowseFacets(t *testing.T, f *goldenFixture, c goldenCase) (goldenCase, bool) {
	t.Helper()
	u, err := url.Parse(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	field := u.Query().Get("field")
	if !strings.HasPrefix(c.Name, "facets:") || (field != "actor" && field != "director" && field != "writer" && field != "library") {
		return goldenCase{}, false
	}
	counts := map[string]int{}
	labels := map[string]string{}
	members := goldenBrowsePlayables(t, f, goldenCase{Path: "/v1/libraries/" + f.library.Movies + "/browse", Body: `{"pivot":"movies"}`, Viewer: c.Viewer})
	if field == "library" && len(members) > 0 {
		counts[f.library.Movies] = len(members)
		var label string
		if err = f.db.QueryRow(`SELECT name FROM libraries WHERE id=?`, f.library.Movies).Scan(&label); err != nil {
			t.Fatal(err)
		}
		labels[f.library.Movies] = label
	}
	for _, p := range members {
		if field == "library" {
			continue
		}
		// Tiny intentionally seeds old provider department spellings. Acting is
		// therefore empty, while its explicit Director/Writer role facts are real.
		role := "Director"
		if field == "writer" {
			role = "Writer"
		}
		if field == "actor" {
			continue
		}
		rows, e := f.db.Query(`SELECT DISTINCT c.credited_name FROM catalog_credits c JOIN catalog_credit_labels r ON r.id=c.role_id WHERE c.entity_id=? AND r.label=?`, p.id, role)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var name string
			if e = rows.Scan(&name); e != nil {
				t.Fatal(e)
			}
			counts[name]++
			labels[name] = name
		}
		if e = rows.Close(); e != nil {
			t.Fatal(e)
		}
	}
	keys := []string{}
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return strings.ToLower(labels[keys[i]]) < strings.ToLower(labels[keys[j]])
	})
	values := []any{}
	for _, key := range keys {
		values = append(values, map[string]any{"value": key, "label": labels[key], "count": counts[key], "countIsLowerBound": false})
	}
	doc := map[string]any{"field": field, "values": values, "total": len(keys), "totalIsLowerBound": false, "revision": "<volatile>"}
	raw, e := json.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	c.Status = 200
	c.Response = canonical(string(raw))
	return c, true
}

// aggregateCardKinds is the entry kind each grouping aggregate publishes: a
// decade or genre grouping is a category card, a book series card names its
// series. Only these aggregates are grouping cards; a studio card is a
// different navigation and is never extended here.
var aggregateCardKinds = map[string]string{"decade": "category", "genre": "category", "series": "book_series"}

// browsePivotAggregates is the one aggregate each aggregate browse pivot
// publishes. A grouping card of another aggregate on that page is a change to
// specify, not one to absorb.
var browsePivotAggregates = map[string]string{"categories": "decade", "genres": "genre", "series": "series"}

// goldenCaseLibrary is the fixture library a browse or content request names.
func goldenCaseLibrary(t *testing.T, f *goldenFixture, c goldenCase) string {
	t.Helper()
	rest, ok := strings.CutPrefix(c.Path, "/v1/libraries/")
	if !ok {
		t.Fatalf("%s: expected a library page, got %q", c.Name, c.Path)
	}
	library := strings.SplitN(rest, "/", 2)[0]
	for _, candidate := range []string{f.library.Movies, f.library.Shows, f.library.Music, f.library.Books} {
		if library == candidate {
			return library
		}
	}
	t.Fatalf("%s: %q is not one of the fixture's libraries", c.Name, library)
	return ""
}

func goldenLibraryKind(t *testing.T, f *goldenFixture, library string) string {
	t.Helper()
	var kind string
	if err := f.db.QueryRow(`SELECT kind FROM libraries WHERE id=?`, library).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	return kind
}

// applyAggregateCardFacts states the library a published grouping card belongs
// to and, for Music's genre cards, the Album releases destination c60d8961c
// authors. Both are derived from the request's library and the card's own
// aggregate key; a conflicting, malformed or misplaced recorded value is an
// error rather than something to overwrite. Every other recorded field of the
// card is left exactly as it was.
//
// The recorded baseline is the destination every grouping card published before
// c60d8961c: view "browse" and no browse query. Music's genre cards are the one
// authorized transition, from that recorded "browse" to "releases" plus the
// Album query. A card that already carries the derived facts is accepted
// unchanged. A card whose view is absent, null, of another type or any other
// string is a change to specify, not one to absorb: ContentNavigation.View is a
// plain string the server always publishes, so a missing one is not a recorded
// shape, and only the two named destinations are authorized.
func applyAggregateCardFacts(entry map[string]any, library, libraryKind, aggregate string) error {
	id, ok := entry["id"].(string)
	if !ok {
		return nil
	}
	cardAggregate, value, found := strings.Cut(id, ":")
	wantKind, isAggregate := aggregateCardKinds[cardAggregate]
	if !found || value == "" || !isAggregate {
		return nil
	}
	if aggregate == "" || cardAggregate != aggregate {
		return fmt.Errorf("grouping card %q appears on a page that publishes no %s grouping", id, cardAggregate)
	}
	if kind, _ := entry["kind"].(string); kind != wantKind {
		return fmt.Errorf("grouping card %q is a %q, want %q", id, kind, wantKind)
	}
	navigation, ok := entry["navigation"].(map[string]any)
	if !ok {
		return fmt.Errorf("grouping card %q has no navigation", id)
	}
	if category, _ := navigation["category"].(string); category != id {
		return fmt.Errorf("grouping card %q navigates to category %q", id, category)
	}
	view, destination := "browse", (map[string]any)(nil)
	if cardAggregate == "genre" && libraryKind == "music" {
		view = "releases"
		destination = map[string]any{
			"pivot": "albums",
			"query": map[string]any{"field": "genre", "operator": "equals", "value": value},
			"sort":  []any{map[string]any{"field": "title", "direction": "asc"}},
		}
	}
	existing, present := navigation["view"]
	if !present {
		return fmt.Errorf("grouping card %q has no destination view, want the recorded %q", id, "browse")
	}
	recorded, ok := existing.(string)
	if !ok {
		return fmt.Errorf("grouping card %q has a malformed destination view %v", id, existing)
	}
	if recorded != "browse" && recorded != view {
		return fmt.Errorf("grouping card %q records destination view %q, want the recorded %q", id, recorded, "browse")
	}
	navigation["view"] = view
	if destination != nil {
		if existing, present := navigation["browse"]; present && !reflect.DeepEqual(existing, destination) {
			return fmt.Errorf("grouping card %q has a conflicting Album destination %v", id, existing)
		}
		navigation["browse"] = destination
	} else if existing, present := navigation["browse"]; present {
		// ContentNavigation.Browse is an omitempty pointer: a card with no Album
		// destination publishes no browse key at all, so a recorded one here is
		// a change to specify.
		return fmt.Errorf("grouping card %q unexpectedly carries a browse destination %v", id, existing)
	}
	switch existing, present := entry["libraryId"]; {
	case !present:
		entry["libraryId"] = library
	case existing == nil:
		return fmt.Errorf("grouping card %q has a null libraryId", id)
	default:
		text, ok := existing.(string)
		if !ok {
			return fmt.Errorf("grouping card %q has a malformed libraryId %v", id, existing)
		}
		if text != library {
			return fmt.Errorf("grouping card %q names library %q, not its own %q", id, text, library)
		}
	}
	return nil
}

// c60d8961c publishes libraryId on every browse grouping card and gives Music's
// genre cards an exact Album releases destination. Apply that to the recorded
// answer for the pages the browse aggregate produces, and to Discover's genre
// rail, which reuses the same listing; the recorded matrix itself is unchanged.
func goldenAggregateCardFacts(t *testing.T, f *goldenFixture, c goldenCase, want string) string {
	t.Helper()
	if c.Status != 200 {
		return want
	}
	browse := strings.HasPrefix(c.Name, "browse:")
	if !browse && !strings.HasPrefix(c.Name, "content:") {
		return want
	}
	library := goldenCaseLibrary(t, f, c)
	libraryKind := goldenLibraryKind(t, f, library)
	var doc map[string]any
	if err := json.Unmarshal([]byte(want), &doc); err != nil {
		t.Fatal(err)
	}
	apply := func(entries []any, aggregate string) {
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if err := applyAggregateCardFacts(entry, library, libraryKind, aggregate); err != nil {
				t.Fatalf("%s: %v", c.Name, err)
			}
		}
	}
	if browse {
		var request struct {
			Pivot string `json:"pivot"`
		}
		if err := json.Unmarshal([]byte(c.Body), &request); err != nil {
			t.Fatal(err)
		}
		entries, ok := doc["entries"].([]any)
		if !ok {
			return want
		}
		apply(entries, browsePivotAggregates[request.Pivot])
	} else {
		touched := false
		sections, _ := doc["sections"].([]any)
		for _, raw := range sections {
			section, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := section["id"].(string); id != "genres" {
				continue
			}
			entries, ok := section["entries"].([]any)
			if !ok {
				continue
			}
			apply(entries, "genre")
			touched = true
		}
		if !touched {
			return want
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}

// The extension is bounded: it fills an absent value, accepts the value it
// derives, and refuses anything else. These controls run without a catalogue.
func TestGoldenAggregateCardFactsBoundaries(t *testing.T) {
	card := func(id, kind, view string) map[string]any {
		return map[string]any{
			"id": id, "kind": kind, "title": "Card",
			"navigation": map[string]any{"category": id, "view": view},
		}
	}
	albumDestination := func(value string) map[string]any {
		return map[string]any{
			"pivot": "albums",
			"query": map[string]any{"field": "genre", "operator": "equals", "value": value},
			"sort":  []any{map[string]any{"field": "title", "direction": "asc"}},
		}
	}
	decade := card("decade:1950", "category", "browse")
	if err := applyAggregateCardFacts(decade, "fixture-movies", "movie", "decade"); err != nil {
		t.Fatalf("absent libraryId: %v", err)
	}
	if decade["libraryId"] != "fixture-movies" || decade["navigation"].(map[string]any)["view"] != "browse" {
		t.Fatalf("decade card was not extended: %v", decade)
	}
	if _, present := decade["navigation"].(map[string]any)["browse"]; present {
		t.Fatalf("decade card gained an Album destination: %v", decade)
	}
	recorded := card("decade:1950", "category", "browse")
	recorded["libraryId"] = "fixture-movies"
	if err := applyAggregateCardFacts(recorded, "fixture-movies", "movie", "decade"); err != nil || recorded["libraryId"] != "fixture-movies" {
		t.Fatalf("agreeing libraryId: %v %v", err, recorded["libraryId"])
	}
	conflict := card("decade:1950", "category", "browse")
	conflict["libraryId"] = "fixture-tv"
	if err := applyAggregateCardFacts(conflict, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("conflicting libraryId was accepted")
	}
	null := card("decade:1950", "category", "browse")
	null["libraryId"] = nil
	if err := applyAggregateCardFacts(null, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("null libraryId was accepted")
	}
	malformed := card("decade:1950", "category", "browse")
	malformed["libraryId"] = 7
	if err := applyAggregateCardFacts(malformed, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("non-string libraryId was accepted")
	}
	if err := applyAggregateCardFacts(card("decade:1950", "movie", "browse"), "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("wrongly typed grouping card was accepted")
	}
	if err := applyAggregateCardFacts(card("decade:1950", "category", "browse"), "fixture-movies", "movie", "genre"); err == nil {
		t.Fatal("card on a page publishing another aggregate was accepted")
	}
	if err := applyAggregateCardFacts(card("decade:1950", "category", "browse"), "fixture-movies", "movie", ""); err == nil {
		t.Fatal("grouping card on a non-aggregate page was accepted")
	}
	broken := card("decade:1950", "category", "browse")
	broken["navigation"].(map[string]any)["category"] = "decade:1960"
	if err := applyAggregateCardFacts(broken, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("card navigating to another category was accepted")
	}
	if err := applyAggregateCardFacts(map[string]any{"id": "movie-0000000", "kind": "movie"}, "fixture-movies", "movie", "decade"); err != nil {
		t.Fatal("a title card must be left alone:", err)
	}
	genre := card("genre:Action", "category", "browse")
	if err := applyAggregateCardFacts(genre, "fixture-music", "music", "genre"); err != nil {
		t.Fatalf("music genre card: %v", err)
	}
	navigation := genre["navigation"].(map[string]any)
	if genre["libraryId"] != "fixture-music" || navigation["view"] != "releases" || !reflect.DeepEqual(navigation["browse"], albumDestination("Action")) {
		t.Fatalf("music genre card was not extended: %v", genre)
	}
	movieGenre := card("genre:Drama", "category", "browse")
	if err := applyAggregateCardFacts(movieGenre, "fixture-movies", "movie", "genre"); err != nil {
		t.Fatalf("movie genre card: %v", err)
	}
	if movieGenre["libraryId"] != "fixture-movies" || movieGenre["navigation"].(map[string]any)["view"] != "browse" {
		t.Fatalf("movie genre card took Music's destination: %v", movieGenre)
	}
	foreign := card("genre:Action", "category", "browse")
	foreign["navigation"].(map[string]any)["browse"] = albumDestination("Comedy")
	if err := applyAggregateCardFacts(foreign, "fixture-music", "music", "genre"); err == nil {
		t.Fatal("conflicting Album destination was accepted")
	}
	unexpected := card("genre:Action", "category", "browse")
	unexpected["navigation"].(map[string]any)["browse"] = albumDestination("Action")
	if err := applyAggregateCardFacts(unexpected, "fixture-movies", "movie", "genre"); err == nil {
		t.Fatal("movie genre card with an Album destination was accepted")
	}
	badView := card("decade:1950", "category", "browse")
	badView["navigation"].(map[string]any)["view"] = 3
	if err := applyAggregateCardFacts(badView, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("malformed destination view was accepted")
	}
	nullView := card("decade:1950", "category", "browse")
	nullView["navigation"].(map[string]any)["view"] = nil
	if err := applyAggregateCardFacts(nullView, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("an explicitly null destination view was accepted")
	}
	missingView := card("decade:1950", "category", "browse")
	delete(missingView["navigation"].(map[string]any), "view")
	if err := applyAggregateCardFacts(missingView, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("a card with no destination view was accepted")
	}
	// Only Music's genre cards may leave the recorded "browse" destination: a
	// title-library card naming Music's derived view is a change to specify.
	foreignView := card("decade:1950", "category", "releases")
	if err := applyAggregateCardFacts(foreignView, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("a movie card recording Music's releases view was accepted")
	}
	foreignGenreView := card("genre:Action", "category", "category")
	if err := applyAggregateCardFacts(foreignGenreView, "fixture-music", "music", "genre"); err == nil {
		t.Fatal("a Music genre card recording an unrelated view was accepted")
	}
	musicMissingView := card("genre:Action", "category", "browse")
	delete(musicMissingView["navigation"].(map[string]any), "view")
	if err := applyAggregateCardFacts(musicMissingView, "fixture-music", "music", "genre"); err == nil {
		t.Fatal("a Music genre card with no destination view was accepted")
	}
	musicNullView := card("genre:Action", "category", "browse")
	musicNullView["navigation"].(map[string]any)["view"] = nil
	if err := applyAggregateCardFacts(musicNullView, "fixture-music", "music", "genre"); err == nil {
		t.Fatal("a Music genre card with a null destination view was accepted")
	}
	// The legacy recorded "browse" is exactly the value that transitions to
	// releases; the derived view itself is accepted unchanged.
	alreadyExact := card("genre:Action", "category", "releases")
	if err := applyAggregateCardFacts(alreadyExact, "fixture-music", "music", "genre"); err != nil {
		t.Fatalf("already exact Music genre card: %v", err)
	}
	exactNavigation := alreadyExact["navigation"].(map[string]any)
	if exactNavigation["view"] != "releases" || !reflect.DeepEqual(exactNavigation["browse"], albumDestination("Action")) {
		t.Fatalf("already exact Music genre card was not completed: %v", alreadyExact)
	}
	// A title-library card keeps the exact recorded "browse" view and gains no
	// Album destination, including from an explicit null browse key.
	movieKept := card("genre:Drama", "category", "browse")
	if err := applyAggregateCardFacts(movieKept, "fixture-movies", "movie", "genre"); err != nil {
		t.Fatalf("movie genre card: %v", err)
	}
	if movieKept["navigation"].(map[string]any)["view"] != "browse" {
		t.Fatalf("movie genre card's recorded view changed: %v", movieKept)
	}
	nullBrowse := card("decade:1950", "category", "browse")
	nullBrowse["navigation"].(map[string]any)["browse"] = nil
	if err := applyAggregateCardFacts(nullBrowse, "fixture-movies", "movie", "decade"); err == nil {
		t.Fatal("an explicitly null browse destination on a title library was accepted")
	}

	// Ground both rules in the published schema: ContentNavigation.View is a
	// plain string field, so the server never omits it; ContentNavigation.Browse
	// is an omitempty pointer, so a card without an Album destination has no
	// browse key at all.
	view, err := json.Marshal(catalog.ContentNavigation{View: "browse"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(view), `"view":"browse"`) {
		t.Fatalf("ContentNavigation.View is no longer always published: %s", view)
	}
	if strings.Contains(string(view), `"browse":`) {
		t.Fatalf("ContentNavigation.Browse is no longer omitted when absent: %s", view)
	}
}
