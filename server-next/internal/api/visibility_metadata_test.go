package api

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// What the metadata rows do: the owner's edits, Undo, merges and splits, and a provider's answers.

// edit sends one editor change as the owner, with the title's revision.
func (w *world) edit(id string, patch MetadataPatch) {
	w.t.Helper()
	var doc MetadataDocument
	if code := call(w.t, w.s, "GET", "/v1/admin/items/"+id+"/metadata", w.owner, nil, &doc).Code; code != 200 {
		w.t.Fatalf("opening %s in the editor answered %d", id, code)
	}
	r := newRequest("PATCH", "/v1/admin/items/"+id+"/metadata", w.owner, patch)
	r.Header.Set("If-Match", `"`+doc.Revision+`"`)
	if res := serve(w.s, r); res.Code != 200 {
		w.t.Fatalf("editing %s answered %d: %s", id, res.Code, res.Body)
	}
}

func (w *world) owned(method, path string, body, out any) {
	w.t.Helper()
	if res := call(w.t, w.s, method, path, w.owner, body, out); res.Code != 200 {
		w.t.Fatalf("%s %s answered %d: %s", method, path, res.Code, res.Body)
	}
}

// mildShows are the seeded shows the profile with a rating limit of 13 may see, by title.
func (w *world) mildShows() []string {
	f, v := w.read(), w.viewers[4]
	var out []string
	for _, id := range f.byTitle() {
		if f.items[id].kind == db.KindShow && !f.hidden(v.profile, id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		w.t.Fatal("the profile with a rating limit may see no show")
	}
	return out
}

// adultsOnly makes a show NC-17 in the editor and labels the first film a profile with a rating limit may see.
func (w *world) adultsOnly() {
	w.t.Helper()
	nc17 := "NC-17"
	w.edit(w.mildShows()[0], MetadataPatch{ContentRating: &nc17})
	f, v := w.read(), w.viewers[4]
	for _, id := range f.byTitle() {
		if it := f.items[id]; it.kind == db.KindMovie && !f.hidden(v.profile, id) {
			w.edit(id, MetadataPatch{Labels: &[]string{w.labels[0].Name}})
			return
		}
	}
	w.t.Fatal("the profile with a rating limit may see no film")
}

// undoAfterRating renames an episode, makes its show NC-17, then undoes the rename.
func (w *world) undoAfterRating() {
	w.t.Helper()
	show, f := w.mildShows()[0], w.read()
	var episode string
	for _, id := range f.byTitle() {
		if f.items[id].kind == db.KindEpisode && f.items[id].root == show {
			episode = id
			break
		}
	}
	renamed, nc17 := "Renamed", "NC-17"
	w.edit(episode, MetadataPatch{Title: &renamed})
	w.edit(show, MetadataPatch{ContentRating: &nc17})
	w.check("the show is adults only")
	var history Page[MetadataHistoryEntry]
	w.owned("GET", "/v1/admin/items/"+episode+"/metadata/history", nil, &history)
	w.owned("POST", "/v1/admin/items/"+episode+"/metadata/history/"+history.Items[0].ID+"/undo", nil, nil)
}

// mergeIntoAdult makes a seeded show NC-17 and merges a show rated 8 into it.
func (w *world) mergeIntoAdult() {
	w.t.Helper()
	into, from, nc17 := w.mildShows()[0], w.addShow("Merged", 8), "NC-17"
	w.edit(into, MetadataPatch{ContentRating: &nc17})
	w.check("the show is adults only")
	w.owned("POST", "/v1/admin/items/"+into+"/merge", MergeRequest{ItemID: from.item}, nil)
}

// splitByRoute splits the second version off a film with a label, as the owner does.
func (w *world) splitByRoute() map[string]string {
	w.t.Helper()
	film := w.film(intp(8), []db.TagName{w.label(0)}, "film.mkv", "film - extended.mkv")
	w.check("the film is added")
	var item Item
	w.owned("GET", "/v1/items/"+film, nil, &item)
	var made MatchResult
	w.owned("POST", "/v1/admin/versions/"+item.Versions[1].ID+"/split", nil, &made)
	return map[string]string{made.ItemID: film}
}

// lookUp scans a film folder, labels two films, and has the stand-in for TMDB answer: a lookup
// of the library, a match chosen by hand, a second film merged by choosing the same match, a
// film kept as it is, a refresh, and a show's episodes told their names.
func (w *world) lookUp() {
	w.t.Helper()
	root := filepath.Join(w.t.TempDir(), "looked-up")
	if err := os.MkdirAll(root, 0o755); err != nil {
		w.t.Fatal(err)
	}
	for n, name := range []string{"Heat.mkv", "the.matrix.1999.1080p.mkv", "Zzyzx Home Video (2011).mkv", "Nothing Like It (2003).mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, 100+n), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
	created, err := w.s.db.CreateLibrary(background, "Looked-up films", db.LibraryMovies, "en", []string{root})
	if err != nil {
		w.t.Fatal(err)
	}
	all, err := w.s.db.AllLibraries(background)
	if err != nil {
		w.t.Fatal(err)
	}
	var lib db.Library
	for _, l := range all {
		if l.ID == created.ID {
			lib = l
		}
	}
	if _, err := (&scan.Scanner{DB: w.s.db, Prober: stillProber{}}).Scan(background, lib); err != nil {
		w.t.Fatal(err)
	}
	w.views = append(w.views, [2]string{lib.PublicID, "movie"})
	byTitle := func(title string) string {
		return w.idOf(`SELECT public_id FROM items WHERE library_id = ? AND title LIKE ? || '%'`, lib.ID, title)
	}
	heat, zzyzx := byTitle("Heat"), byTitle("Zzyzx")
	w.edit(heat, MetadataPatch{Labels: &[]string{w.labels[0].Name}})
	w.edit(zzyzx, MetadataPatch{Labels: &[]string{w.labels[1%len(w.labels)].Name}})
	w.check("the films are scanned and labelled")
	w.s.UseProvider(tmdb.New("test-token", tmdbtest.New(w.t).URL))
	if _, err := w.s.metadata.MatchLibrary(background, lib.ID); err != nil {
		w.t.Fatal(err)
	}
	w.check("the library is looked up")
	w.owned("PUT", "/v1/admin/items/"+heat+"/match", MatchRequest{Provider: "tmdb", ID: "949"}, nil)
	w.owned("PUT", "/v1/admin/items/"+zzyzx+"/match", MatchRequest{Provider: "tmdb", ID: "949"}, nil)
	w.owned("PUT", "/v1/admin/items/"+byTitle("Nothing Like It")+"/match", MatchRequest{KeepAsIs: true}, nil)
	w.check("matches are chosen")
	w.owned("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, nil)
	// A provider names a seeded adults-only show's episodes; it gives them no rating.
	f := w.read()
	for _, id := range f.byTitle() {
		it := f.items[id]
		if it.kind != db.KindSeason || f.items[it.parent].rating.Int64 < 17 {
			continue
		}
		var show, season int64
		if err := w.raw.QueryRow(`SELECT s.parent_id, s.index_number FROM items s WHERE s.public_id = ?`, id).Scan(&show, &season); err != nil {
			w.t.Fatal(err)
		}
		var children []db.ItemDetails
		for n := 1; n <= 3; n++ {
			children = append(children, db.ItemDetails{Title: fmt.Sprint("Named ", n), Index: n})
		}
		if err := w.s.db.ApplyChildren(background, db.Background, show, db.KindEpisode, int(season), children); err != nil {
			w.t.Fatal(err)
		}
		return
	}
	w.t.Fatal("no season of an adults-only show")
}
