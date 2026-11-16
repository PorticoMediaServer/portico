package api

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// patch sends an edit as the owner with the revision in If-Match.
func (sh *shelf) patch(id, revision string, body MetadataPatch, out any, want int) {
	sh.t.Helper()
	r := newRequest("PATCH", "/v1/admin/items/"+id+"/metadata", sh.owner, body)
	if revision != "" {
		r.Header.Set("If-Match", `"`+revision+`"`)
	}
	w := httptest.NewRecorder()
	sh.s.ServeHTTP(w, r)
	if w.Code != want {
		sh.t.Fatalf("PATCH %s answered %d, want %d: %s", id, w.Code, want, w.Body)
	}
	if out != nil {
		jsonInto(w, out)
	}
}

// The owner edits a matched film's title, labels and cast; the edit is locked against a
// refresh; a stale or out-of-range edit changes nothing; History lists the edit and a chosen
// match, and Undo puts the film back.
func TestMetadataEditorHistoryAndUndo(t *testing.T) {
	sh := newShelf(t, "Heat (1995).mkv")
	var items ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &items, 200)
	heat := items.Items[0].ID
	var doc MetadataDocument
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	if f := doc.Fields["title"]; f.Value != "Heat" || f.Source != "provider" || f.Locked || doc.Credits == nil || len(doc.Credits.Values) == 0 ||
		doc.Match.IDs["tmdb"] != "949" || len(doc.Lists["genres"].Values) == 0 {
		t.Fatalf("the editor's document: %+v", doc)
	}
	cast := len(doc.Credits.Values)
	title, year := "Heat (Director's Definitive Edition)", 10000
	sh.patch(heat, "", MetadataPatch{Title: &title}, nil, 428)
	sh.patch(heat, doc.Revision, MetadataPatch{Title: &title, Year: &year}, nil, 422)
	labels := []string{"Late night"}
	credits := []MetadataCredit{{Name: "Someone New", Role: "actor", Character: "A Thief"}, {PersonID: doc.Credits.Values[0].PersonID, Role: "actor"}}
	var edited MetadataDocument
	sh.patch(heat, doc.Revision, MetadataPatch{Title: &title, Labels: &labels, Credits: &credits, Lock: []string{"tagline"}}, &edited, 200)
	if f := edited.Fields["tagline"]; !f.Locked || f.Source != "provider" {
		t.Fatalf("a field only locked: %+v", f)
	}
	if f := edited.Fields["title"]; f.Value != title || f.Source != "edit" || !f.Locked || len(edited.Credits.Values) != 2 ||
		!edited.Credits.Locked || edited.Lists["labels"].Values[0] != "Late night" || edited.Revision == doc.Revision {
		t.Fatalf("after the edit: %+v", edited)
	}
	sh.patch(heat, doc.Revision, MetadataPatch{Title: &title}, nil, 412) // made on the copy before the edit

	var refreshed MetadataRefreshed
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, &refreshed, 200)
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	if doc.Fields["title"].Value != title || len(doc.Credits.Values) != 2 {
		t.Fatalf("a refresh changed what the owner locked: %+v", doc)
	}

	sh.do("PUT", "/v1/admin/items/"+heat+"/match", MatchRequest{Provider: "tmdb", ID: "949"}, nil, 200)
	var history Page[MetadataHistoryEntry]
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata/history", nil, &history, 200)
	if len(history.Items) != 2 || history.Items[0].Kind != "match_changed" || history.Items[1].Kind != "fields_edited" {
		t.Fatalf("history: %+v", history.Items)
	}
	// Undo to before the edit: the provider's title, cast and no labels. The match chosen after
	// the edit is another change, and stays.
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/history/"+history.Items[1].ID+"/undo", nil, &doc, 200)
	if doc.Fields["title"].Value != "Heat" || doc.Fields["title"].Locked || len(doc.Credits.Values) != cast ||
		len(doc.Lists["labels"].Values) != 0 || doc.Match.State != "chosen" {
		t.Fatalf("after Undo: %+v", doc)
	}
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata/history", nil, &history, 200)
	if len(history.Items) != 3 || history.Items[0].Kind != "undone" {
		t.Fatalf("history after Undo: %+v", history.Items)
	}
	if w := call(t, sh.s, "GET", "/v1/admin/items/"+heat+"/metadata", sh.limitedAdmin, nil, nil); w.Code != 404 {
		t.Fatalf("an admin limited to 13 and under opened an R-rated film's editor: %d", w.Code)
	}
}

// Undo of an older change puts back only what that change made: a label and a genre added
// after it, and a field edited after it, all stay.
func TestUndoOfAnOlderChangeKeepsWhatCameAfter(t *testing.T) {
	sh := newShelf(t, "Heat (1995).mkv")
	var items ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &items, 200)
	heat := items.Items[0].ID
	var doc MetadataDocument
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	title := "Heat (an older edit)"
	sh.patch(heat, doc.Revision, MetadataPatch{Title: &title}, &doc, 200)
	labels, genres, tagline := []string{"Kids"}, append(doc.Lists["genres"].Values, "Heist"), "A later tagline."
	sh.patch(heat, doc.Revision, MetadataPatch{Labels: &labels, Genres: &genres, Tagline: &tagline}, &doc, 200)

	var history Page[MetadataHistoryEntry]
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata/history", nil, &history, 200)
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/history/"+history.Items[1].ID+"/undo", nil, &doc, 200)
	if doc.Fields["title"].Value != "Heat" || doc.Fields["title"].Locked {
		t.Fatalf("the older edit was not undone: %+v", doc.Fields["title"])
	}
	if got := doc.Lists["labels"].Values; len(got) != 1 || got[0] != "Kids" {
		t.Fatalf("Undo of an older edit removed a label added since: %v", got)
	}
	if got := doc.Lists["genres"].Values; !slices.Contains(got, "Heist") || !doc.Lists["genres"].Locked {
		t.Fatalf("Undo of an older edit removed a genre added since: %+v", doc.Lists["genres"])
	}
	if f := doc.Fields["tagline"]; f.Value != tagline || !f.Locked {
		t.Fatalf("Undo of an older edit changed a field edited since: %+v", f)
	}
}

// The editor shows the sort title a person set, as they typed it, and empty when none was set:
// never the server's sort key (Builder X, 16 Nov: the demo showed "portico hdr00000000002:10; …").
func TestTheEditorShowsTheSortTitleAsTyped(t *testing.T) {
	sh := newShelf(t, "Heat (1995).mkv")
	var items ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &items, 200)
	heat := items.Items[0].ID
	var doc MetadataDocument
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	if v := doc.Fields["sortTitle"].Value; v != "" {
		t.Fatalf("a sort title nobody set reads %q; want empty", v)
	}
	mine := "Heat 2: The Long Version"
	sh.patch(heat, doc.Revision, MetadataPatch{SortTitle: &mine}, &doc, 200)
	if f := doc.Fields["sortTitle"]; f.Value != mine || f.Source != "edit" || !f.Locked {
		t.Fatalf("after setting it: %+v; want it as typed", f)
	}
	again := "Heat"
	sh.patch(heat, doc.Revision, MetadataPatch{Title: &again}, &doc, 200)
	if v := doc.Fields["sortTitle"].Value; v != mine {
		t.Fatalf("after a title edit the sort title reads %q; want %q kept", v, mine)
	}
	// Undo of the edit that set it puts back no sort title of the person's own.
	var history Page[MetadataHistoryEntry]
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata/history", nil, &history, 200)
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/history/"+history.Items[len(history.Items)-1].ID+"/undo", nil, &doc, 200)
	if v := doc.Fields["sortTitle"].Value; v != "" {
		t.Fatalf("after undoing the edits the sort title reads %q; want empty", v)
	}
}

// "Use it" (definition area 4, rule 23): beside a field a person edited, the editor offers the
// provider's value, kept through a refresh, and taking it puts the provider's value back now,
// unlocked and the provider's again.
func TestTheEditorOffersTheProvidersValueBesideAnEditedField(t *testing.T) {
	sh := newShelf(t, "Heat (1995).mkv")
	var items ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &items, 200)
	heat := items.Items[0].ID
	var doc MetadataDocument
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	tmdbSummary := doc.Fields["summary"].Value
	if doc.Fields["summary"].Source != "provider" || doc.Fields["summary"].ProviderValue != nil || tmdbSummary == nil {
		t.Fatalf("before any edit: %+v", doc.Fields["summary"])
	}
	mine := "My own words."
	sh.patch(heat, doc.Revision, MetadataPatch{Summary: &mine}, &doc, 200)
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, nil, 200)
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	if f := doc.Fields["summary"]; f.Value != mine || f.Source != "edit" || !f.Locked || f.ProviderValue != tmdbSummary {
		t.Fatalf("an edited summary after a refresh: %+v; want mine, locked, with the provider's beside it", f)
	}
	if f := doc.Fields["title"]; f.ProviderValue != nil {
		t.Errorf("a field showing the provider's own value offers it again: %+v", f)
	}
	sh.patch(heat, doc.Revision, MetadataPatch{UseProvider: []string{"summary"}}, &doc, 200)
	if f := doc.Fields["summary"]; f.Value != tmdbSummary || f.Source != "provider" || f.Locked || f.ProviderValue != nil {
		t.Fatalf("after Use it: %+v; want the provider's, unlocked, with nothing beside it", f)
	}
	sh.patch(heat, doc.Revision, MetadataPatch{UseProvider: []string{"tagline"}}, nil, 400)
}

// A film's own Metadata language (definition area 4, rule 19) is the language its details are
// fetched in instead of its library's; clearing it goes back to the library's.
func TestAFilmsOwnMetadataLanguage(t *testing.T) {
	sh := newShelf(t, "Heat (1995).mkv")
	var items ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &items, 200)
	heat := items.Items[0].ID
	var doc MetadataDocument
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	if f := doc.Fields["metadataLanguage"]; f.Value != nil || f.Source != "library" {
		t.Fatalf("a film's language before any choice: %+v; want the library's", f)
	}
	french := "fr"
	sh.patch(heat, doc.Revision, MetadataPatch{MetadataLanguage: &french}, &doc, 200)
	if f := doc.Fields["metadataLanguage"]; f.Value != "fr" || f.Source != "edit" {
		t.Fatalf("after choosing French: %+v", f)
	}
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, nil, 200)
	if got := *sh.tmdb.Language.Load(); got != "fr" && !strings.HasPrefix(got, "fr-") {
		t.Fatalf("the refresh asked TMDB in %q; want French", got)
	}
	none := ""
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	sh.patch(heat, doc.Revision, MetadataPatch{MetadataLanguage: &none}, &doc, 200)
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, nil, 200)
	if got := *sh.tmdb.Language.Load(); strings.HasPrefix(got, "fr") {
		t.Fatalf("after going back to the library's, TMDB was asked in %q", got)
	}
	bad := "French"
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata", nil, &doc, 200)
	sh.patch(heat, doc.Revision, MetadataPatch{MetadataLanguage: &bad}, nil, 422)
}
