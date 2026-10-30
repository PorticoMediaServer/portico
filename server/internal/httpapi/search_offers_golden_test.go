package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Search-offer extension changes no recorded result, ordering, count or
// restriction fact. These are literal expectations for buildGoldenFixture's
// fresh local members: Catalog/Playback/queue exist, but Scheduler, Metadata,
// Downloads and owner administration do not. Do not call ProjectHomeActions
// (the producer under test) or derive expected offers from the actual response.
func goldenSearchOffers(t *testing.T, f *goldenFixture, c goldenCase, recorded string) string {
	t.Helper()
	if !strings.HasPrefix(c.Name, "search:") || c.Status != 200 {
		return recorded
	}
	if c.Viewer != "open" && c.Viewer != "restricted" {
		t.Fatalf("%s: specify Search offers for this fixture viewer", c.Name)
	}
	profile := "golden-open-profile"
	if c.Viewer == "restricted" {
		profile = "golden-limited-profile"
	}
	var personal int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM personal_items WHERE profile_id=?)+(SELECT count(*) FROM container_personal_state WHERE profile_id=?)`, profile, profile).Scan(&personal); err != nil {
		t.Fatal(err)
	}
	if personal != 0 {
		t.Fatalf("%s: fresh member fixture has unexpected personal state", c.Name)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(recorded), &doc); err != nil {
		t.Fatal(err)
	}
	groups, ok := doc["groups"].([]any)
	if !ok {
		t.Fatalf("%s: recorded Search groups missing", c.Name)
	}
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			t.Fatal("recorded Search group is not an object")
		}
		// People/authors/channels remain exactly recorded, with no media offers.
		if group["entityKind"] == "person" || group["entityKind"] == "author" || group["entityKind"] == "channel" {
			continue
		}
		items, ok := group["items"].([]any)
		if !ok {
			t.Fatal("recorded Search items missing")
		}
		for _, raw := range items {
			entry, ok := raw.(map[string]any)
			if !ok {
				t.Fatal("recorded Search item is not an object")
			}
			if _, recordedOffers := entry["homeActions"]; recordedOffers {
				continue
			}
			entry["homeActions"] = goldenMemberOffers(t, f, c, entry)
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}

// goldenMemberOffers spells the offers a fresh fixture member is given for one
// recorded entry: every key deliberately, so a new producer field still fails
// the golden rather than be accepted by a shared production struct.
func goldenMemberOffers(t *testing.T, f *goldenFixture, c goldenCase, entry map[string]any) map[string]any {
	t.Helper()
	id, idOK := entry["id"].(string)
	kind, kindOK := entry["kind"].(string)
	public, known := f.public[id]
	if !idOK || !kindOK || !known {
		t.Fatalf("%s: offer target is not a known fixture entity", c.Name)
	}
	var entity int64
	var actualKind, library string
	var playable, available bool
	if err := f.db.QueryRow(`SELECT e.id,k.name,l.library_id,k.playable,COALESCE(a.available,0) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries l ON l.id=e.library_id LEFT JOIN catalog_item_availability a ON a.entity_id=e.id AND a.retired=0 WHERE e.public_id=pid_blob(?)`, public).Scan(&entity, &actualKind, &library, &playable, &available); err != nil {
		t.Fatal(err)
	}
	if actualKind != kind || library != entry["libraryId"] {
		t.Fatalf("%s: recorded identity differs from fixture", c.Name)
	}
	if c.Viewer == "restricted" && library != f.library.Movies && library != f.library.Shows {
		t.Fatalf("%s: recorded entry escaped the fixture library restriction", c.Name)
	}
	leaf := kind == "movie" || kind == "episode" || kind == "song"
	if leaf {
		if !playable || !available || entry["available"] != true {
			t.Fatalf("%s: specify offers for this unavailable fixture leaf", c.Name)
		}
	} else {
		switch kind {
		case "show", "album", "book", "season", "artist", "collection":
		default:
			t.Fatalf("%s: specify offers for kind %q", c.Name, kind)
		}
		var members int
		if err := f.db.QueryRow(`SELECT count(*) FROM catalog_browse_memberships m JOIN catalog_item_availability a ON a.entity_id=m.item_id AND a.available=1 AND a.retired=0 WHERE m.entity_id=?`, entity).Scan(&members); err != nil {
			t.Fatal(err)
		}
		if playable || members == 0 {
			t.Fatalf("%s: fixture container has no available member", c.Name)
		}
		if navigation, ok := entry["navigation"].(map[string]any); ok && navigation["entityId"] != id {
			t.Fatalf("%s: specify offers for a redirected fixture container", c.Name)
		}
	}
	// Every key is spelled here deliberately: a new producer field must still
	// fail the golden rather than be accepted by a shared production struct.
	selection := map[string]any{"play": true, "queue": true, "playlist": false, "collection": false, "myList": false, "favorite": false, "watched": false, "download": false, "edit": false, "refresh": false, "delete": false}
	actions := map[string]any{"notInterested": true, "play": true, "queue": true, "playlist": leaf, "collection": leaf, "myList": true, "favorite": true, "watched": false, "played": false, "rating": leaf, "editMetadata": false, "refreshMetadata": false, "delete": false, "download": false, "details": true,
		"state": map[string]any{"watchlisted": false, "favorite": false, "watched": false, "rating": nil, "personalRevision": 0, "containerRevision": 0}, "selection": selection}
	switch kind {
	case "movie", "episode":
		actions["watched"] = true
	case "song":
		actions["played"] = true
	case "show":
		actions["watched"] = true
		selection["favorite"], selection["watched"] = true, true
	case "album":
		actions["myList"] = false
		actions["played"] = true
		selection["favorite"] = true
	case "book":
		actions["played"] = true
		selection["favorite"] = true
	case "season":
		delete(actions, "notInterested")
		actions["myList"], actions["favorite"], actions["watched"] = false, false, true
		selection["watched"] = true
	case "artist":
		delete(actions, "notInterested")
		actions["myList"] = false
		selection["favorite"] = true
	case "collection":
		delete(actions, "notInterested")
		actions["myList"], actions["favorite"] = false, false
	}
	return actions
}

// goldenBrowseOffers adds the aggregate selection offers library and content
// pages now give their displayed containers (MOB-030) to cases recorded
// before them. Leaves and non-container rows (authors, people) stay as recorded.
func goldenBrowseOffers(t *testing.T, f *goldenFixture, c goldenCase, recorded string) string {
	t.Helper()
	if !strings.HasPrefix(c.Name, "browse:") && !strings.HasPrefix(c.Name, "content:") || c.Status != 200 {
		return recorded
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(recorded), &doc); err != nil {
		t.Fatal(err)
	}
	// A library page lists entries; a content page its entity and sections.
	entries, _ := doc["entries"].([]any)
	if entity, ok := doc["entity"]; ok && entity != nil {
		entries = append(entries, entity)
	}
	sections, _ := doc["sections"].([]any)
	for _, raw := range sections {
		if section, ok := raw.(map[string]any); ok {
			members, _ := section["entries"].([]any)
			entries = append(entries, members...)
		}
	}
	changed := false
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatal("recorded browse entry is not an object")
		}
		if _, recordedOffers := entry["homeActions"]; recordedOffers {
			continue
		}
		switch entry["kind"] {
		case "show", "season", "album", "artist", "book", "collection":
		default:
			continue
		}
		// A library page's entries leave their library implied by the route.
		library, hadLibrary := entry["libraryId"]
		if !hadLibrary {
			public := f.public[entry["id"].(string)]
			var id string
			if err := f.db.QueryRow(`SELECT l.library_id FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.public_id=pid_blob(?)`, public).Scan(&id); err != nil {
				t.Fatal(err)
			}
			entry["libraryId"] = id
		}
		entry["homeActions"] = goldenMemberOffers(t, f, c, entry)
		if !hadLibrary {
			delete(entry, "libraryId")
		} else {
			entry["libraryId"] = library
		}
		changed = true
	}
	if !changed {
		return recorded
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}
