package api

import (
	"net/http/httptest"
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
	sh.patch(heat, doc.Revision, MetadataPatch{Title: &title, Labels: &labels, Credits: &credits}, &edited, 200)
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
	// Undo to before the edit: the provider's title, cast and no labels, and the match as the
	// server made it.
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/history/"+history.Items[1].ID+"/undo", nil, &doc, 200)
	if doc.Fields["title"].Value != "Heat" || doc.Fields["title"].Locked || len(doc.Credits.Values) != cast ||
		len(doc.Lists["labels"].Values) != 0 || doc.Match.State != "matched" {
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
