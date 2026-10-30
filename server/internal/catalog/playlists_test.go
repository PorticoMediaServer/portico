package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"sync"
	"testing"

	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func phase34PlaylistDB(t *testing.T, path string) (*sql.DB, *catalogtest.Catalog, *Service) {
	t.Helper()
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return db, catalogtest.New(t, db), New(db)
}

func phase34SettlePlaylistProjection(t *testing.T, db *sql.DB) {
	t.Helper()
	catalogtest.New(t, db).Drain()
}

func TestPlaylistOccurrencesPermissionsCASAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, c, s := phase34PlaylistDB(t, path)
	defer func() { db.Close() }()
	aLibrary := c.Library("a", "A", "movie", "/a")
	bLibrary := c.Library("b", "B", "movie", "/b")
	visible := c.Movie(aLibrary, "/a/visible.mkv", "Visible", 2020)
	secretItem := c.Movie(bLibrary, "/b/secret.mkv", "Secret", 2020)
	c.Drain()
	owner := ResourceActor{"local", "owner", "p"}
	editor := ResourceActor{"local", "editor", "p"}
	name := "Mixed playlist"
	summary := "Shared safely"
	create := PlaylistMutation{OperationID: "create", Name: &name, Summary: &summary}
	first, e := s.MutatePlaylist(owner, "", "create", "", create, nil)
	if e != nil {
		t.Fatal(e)
	}
	id := first.PlaylistID
	add := PlaylistMutation{OperationID: "add", ExpectedRevision: 1, ItemID: visible.Public}
	one, e := s.MutatePlaylist(owner, id, "add", "", add, nil)
	if e != nil {
		t.Fatal(e)
	}
	two, e := s.MutatePlaylist(owner, id, "add", "", PlaylistMutation{OperationID: "add2", ExpectedRevision: 2, ItemID: visible.Public}, nil)
	if e != nil || one.EntryID == two.EntryID {
		t.Fatal("duplicates collapsed", e)
	}
	secret, e := s.MutatePlaylist(owner, id, "add", "", PlaylistMutation{OperationID: "secret", ExpectedRevision: 3, ItemID: secretItem.Public}, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.MutatePlaylist(owner, id, "share", "", PlaylistMutation{OperationID: "share", ExpectedRevision: 4, Authority: editor.Authority, AccountID: editor.AccountID, ProfileID: editor.ProfileID, Role: "editor"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	phase34SettlePlaylistProjection(t, db)
	request := ContentRequest{Viewer: Viewer{Profile: editor.ProfileID, Fence: "f", Libraries: []string{"a"}}, ServerID: "server", Profile: editor.ProfileID, ViewerFence: "f", View: "playlist", EntityID: id, Limit: 2}
	page, e := s.PlaylistContent(request, editor, []string{"a"})
	if e != nil || page.PlaylistRevision == nil || *page.PlaylistRevision != 5 {
		t.Fatal(page, e)
	}
	if len(page.Sections) != 1 || page.Sections[0].Entries[0].ID == page.Sections[0].Entries[1].ID {
		t.Fatal("occurrences missing")
	}
	request.Cursor = page.Sections[0].NextCursor
	hidden, e := s.PlaylistContent(request, editor, []string{"a"})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(hidden.Sections[0].Entries[0])
	var obj map[string]any
	json.Unmarshal(raw, &obj)
	if len(obj) != 3 || obj["hidden"] != true {
		t.Fatal("hidden metadata leaked", string(raw))
	}
	if _, e = s.MutatePlaylist(editor, id, "update", "", PlaylistMutation{OperationID: "rename", ExpectedRevision: 5, Name: &name}, nil); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("editor renamed", e)
	}
	if _, e = s.Playlist("server", "f", id, ResourceActor{"hosted", "editor", "p"}); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("authority collision", e)
	}
	reordered, e := s.MutatePlaylist(editor, id, "reorder", "", PlaylistMutation{OperationID: "reorder", ExpectedRevision: 5, EntryIDs: []string{secret.EntryID, two.EntryID, one.EntryID}}, nil)
	if e != nil || reordered.Revision != 6 {
		t.Fatal(e)
	}
	if _, e = s.PlaylistContent(request, editor, []string{"a"}); e != nil {
		t.Fatal("page did not continue after publication", e)
	}
	if _, e = s.MutatePlaylist(owner, id, "add", "", PlaylistMutation{OperationID: "old", ExpectedRevision: 5, ItemID: visible.Public}, nil); !errors.Is(e, ErrPlaylistConflict) {
		t.Fatal("stale CAS", e)
	}
	replay, e := s.MutatePlaylist(owner, id, "add", "", add, nil)
	if e != nil || replay.EntryID != one.EntryID || replay.Revision != 2 {
		t.Fatal("receipt not sticky", e)
	}
	if _, e = s.MutatePlaylist(owner, id, "add", "", add, func(*sql.Tx) error { return identity.ErrUnauthorized }); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("replay bypassed auth")
	}
	_, e = s.MutatePlaylist(owner, id, "unshare", "", PlaylistMutation{OperationID: "unshare", ExpectedRevision: 6, Authority: editor.Authority, AccountID: editor.AccountID, ProfileID: editor.ProfileID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutatePlaylist(editor, id, "reorder", "", PlaylistMutation{OperationID: "reorder", ExpectedRevision: 5, EntryIDs: []string{secret.EntryID, two.EntryID, one.EntryID}}, nil); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("removed share replay admitted", e)
	}
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	c = catalogtest.New(t, db)
	phase34SettlePlaylistProjection(t, db)
	s = New(db)
	resource, e := s.Playlist("server", "f", id, owner)
	if e != nil || resource.EntryCount != 3 || resource.Summary != summary || resource.Revision != 7 {
		t.Fatal(resource, e)
	}
	if _, e = s.MutatePlaylist(owner, id, "delete", "", PlaylistMutation{OperationID: "delete", ExpectedRevision: 7}, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE playlist_receipts SET created_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	replay, e = s.MutatePlaylist(owner, "", "create", "", create, nil)
	if e != nil || !replay.Deleted || replay.PlaylistID != id {
		t.Fatal("expired create recreated deleted resource", replay, e)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id IN(?,?)`, visible.ID, secretItem.ID).Scan(&n)
	if n != 2 {
		t.Fatal("playlist deletion deleted media")
	}
}
func TestPlaylistConcurrentEditorsAndBounds(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	a := ResourceActor{"local", "a", "p"}
	name := "Playlist"
	first, e := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, op := range []string{"a", "b"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			_, e := s.MutatePlaylist(a, first.PlaylistID, "update", "", PlaylistMutation{OperationID: op, ExpectedRevision: 1, Name: &name}, nil)
			results <- e
		}(op)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrPlaylistConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
	if _, e = s.MutatePlaylist(a, first.PlaylistID, "reorder", "", PlaylistMutation{OperationID: "huge", ExpectedRevision: 2, EntryIDs: make([]string, 1001)}, nil); !errors.Is(e, ErrPlaylistCapacity) {
		t.Fatal(e)
	}
}
func TestSavedProfilePermissionsAndPaging(t *testing.T) {
	db, c, s := phase34PlaylistDB(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	aLibrary := c.Library("a", "A", "movie", "/a")
	bLibrary := c.Library("b", "B", "movie", "/b")
	// As in the original fixture, none of the saved titles has a file: Saved
	// lists unavailable titles (without playback).
	fileless := func(library int64, root, name, title string, year int, added string) catalogtest.Item {
		return c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, root+"/"+name, 0), Title: title, Year: year, Added: added}, nil)
	}
	alpha := fileless(aLibrary, "/a", "alpha.mkv", "Alpha", 2001, "")
	beta := fileless(aLibrary, "/a", "beta.mkv", "Beta", 2002, "2026-01-01T00:00:00.000Z")
	secret := fileless(bLibrary, "/b", "secret.mkv", "Secret", 2003, "2026-02-01T00:00:00.000Z")
	c.Drain()
	if _, e := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES('p',?,1,1,1),('p',?,1,0,1),('p',?,1,1,1)`, alpha.ID, beta.ID, secret.ID); e != nil {
		t.Fatal(e)
	}
	r := ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, ServerID: "s", Profile: "p", ViewerFence: "f", View: "watchlist", Limit: 1}
	page, e := s.Saved(r, []string{"a"})
	if e != nil {
		t.Fatal(e)
	}
	if page.Sections[0].TotalCount != 2 || page.Sections[0].Entries[0].ID != alpha.Public || page.Sections[0].Entries[0].Playback != nil {
		t.Fatal("saved unavailable lost or scope leaked", page)
	}
	r.Cursor = page.Sections[0].NextCursor
	next, e := s.Saved(r, []string{"a"})
	if e != nil || next.Sections[0].Entries[0].ID != beta.Public {
		t.Fatal(next, e)
	}
	r.Profile = "other"
	r.Viewer.Profile = "other"
	r.Cursor = ""
	empty, e := s.Saved(r, []string{"a"})
	if e != nil || len(empty.Sections) != 0 {
		t.Fatal("profile leak", e)
	}
	r.Profile = "p"
	r.Viewer.Profile = "p"
	r.Sort = "added"
	r.Direction = "desc"
	recent, e := s.Saved(r, []string{"a"})
	if e != nil || recent.Sections[0].Entries[0].ID != beta.Public {
		t.Fatal(recent, e)
	}
}
func TestPlaylistAllLeafKindsAndCatalogDeletion(t *testing.T) {
	db, c, s := phase34PlaylistDB(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	movie := c.Movie(movies, "/movies/movie.mkv", "M", 2020)
	show := c.Show(tv, "Show", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/episode.mkv")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	song := c.Song(album, 1, "/music/song.mp3", "S")
	book := c.Book(books, "Book", "Author")
	part := c.BookFile(book, 1, "/books/part.m4b")
	c.Drain()
	a := ResourceActor{"local", "a", "p"}
	name := "Mixed"
	r, e := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range []struct {
		name string
		item catalogtest.Item
	}{{"movie", movie}, {"episode", episode}, {"song", song}, {"part", part}} {
		r, e = s.MutatePlaylist(a, r.PlaylistID, "add", "", PlaylistMutation{OperationID: entry.name, ExpectedRevision: r.Revision, ItemID: entry.item.Public}, nil)
		if e != nil {
			t.Fatal(entry.name, e)
		}
	}
	if _, e = s.MutatePlaylist(a, r.PlaylistID, "add", "", PlaylistMutation{OperationID: "group", ExpectedRevision: r.Revision, ItemID: show.Public}, nil); e == nil {
		t.Fatal("hierarchy container admitted")
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return compactcatalog.DeleteEntityTx(ctx, tx, movie.ID) })
	phase34SettlePlaylistProjection(t, db)
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=?`, r.PlaylistID).Scan(&count); e != nil || count != 4 {
		t.Fatal("deleted catalog media erased occurrence", count, e)
	}
	page, e := s.PlaylistContent(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, ServerID: "s", Profile: "p", ViewerFence: "f", EntityID: r.PlaylistID, Limit: 1}, a, []string{"a"})
	if e != nil || page.Sections[0].Entries[0].Hidden == nil || !*page.Sections[0].Entries[0].Hidden {
		t.Fatal(page, e)
	}
}
func TestPlaylistDirectoryCursorAndCandidateBounds(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	a := ResourceActor{"local", "owner", "p"}
	n := "Same"
	one, e := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "one", Name: &n}, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "two", Name: &n}, nil)
	if e != nil {
		t.Fatal(e)
	}
	phase34SettlePlaylistProjection(t, db)
	r := ContentRequest{ServerID: "s", Profile: "p", ViewerFence: "f", View: "playlists", Limit: 1}
	first, e := s.PlaylistDirectory(r, a)
	if e != nil || first.Sections[0].TotalCount != 2 {
		t.Fatal(first, e)
	}
	r.Cursor = first.Sections[0].NextCursor
	second, e := s.PlaylistDirectory(r, a)
	if e != nil || second.Sections[0].Entries[0].ID == first.Sections[0].Entries[0].ID {
		t.Fatal(second, e)
	}
	if _, e = s.MutatePlaylist(a, one.PlaylistID, "update", "", PlaylistMutation{OperationID: "rename", ExpectedRevision: 1, Name: &n}, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.PlaylistDirectory(r, a); e != nil {
		t.Fatal("directory cursor did not survive mutation", e)
	}
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('u1','private1',x'00','candidate1',1),('u2','private2',x'00','candidate2',1)`)
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.PlaylistCandidates("s", "f", one.PlaylistID, a, "", 1)
	if e != nil || len(c.Candidates) != 1 || c.NextCursor == "" {
		t.Fatal(c, e)
	}
	next, e := s.PlaylistCandidates("s", "f", one.PlaylistID, a, c.NextCursor, 1)
	if e != nil || next.Candidates[0].ID == c.Candidates[0].ID {
		t.Fatal(next, e)
	}
}
func TestPlaylistAdmissionLimitsAndExpiredCAS(t *testing.T) {
	db, c, s := phase34PlaylistDB(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	library := c.Library("a", "A", "movie", "/a")
	item := c.Movie(library, "/a/item.mkv", "I", 2020)
	c.Drain()
	a := ResourceActor{"local", "a", "p"}
	name := "List"
	r, e := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if e != nil {
		t.Fatal(e)
	}
	add := PlaylistMutation{OperationID: "add", ExpectedRevision: 1, ItemID: item.Public}
	r, e = s.MutatePlaylist(a, r.PlaylistID, "add", "", add, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE playlist_receipts SET created_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutatePlaylist(a, r.PlaylistID, "add", "", add, nil); !errors.Is(e, ErrOperationExpired) {
		t.Fatal("expired operation was not fenced", e)
	}
	var playlistPK int64
	if e = db.QueryRow(`SELECT id FROM catalog_playlists WHERE token=?`, r.PlaylistID).Scan(&playlistPK); e != nil {
		t.Fatal(e)
	}
	insert, e := db.Prepare(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position) VALUES(?,?,?,?)`)
	if e != nil {
		t.Fatal(e)
	}
	for position := 2; position <= 1000; position++ {
		if _, e = insert.Exec(identity.Token(), playlistPK, item.ID, position); e != nil {
			insert.Close()
			t.Fatal(e)
		}
	}
	if e = insert.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutatePlaylist(a, r.PlaylistID, "add", "", PlaylistMutation{OperationID: "overflow", ExpectedRevision: 2, ItemID: item.Public}, nil); e != nil {
		t.Fatal("entry cap not enforced", e)
	}
	_, e = db.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100) INSERT INTO playlist_shares SELECT ?,'local','a'||x,'p'||x,'viewer' FROM n`, r.PlaylistID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MutatePlaylist(a, r.PlaylistID, "share", "", PlaylistMutation{OperationID: "share-overflow", ExpectedRevision: 3, Authority: "local", AccountID: "new", ProfileID: "new", Role: "viewer"}, nil); !errors.Is(e, ErrPlaylistCapacity) {
		t.Fatal("share cap not enforced", e)
	}
}

func TestPlaylistDirectorySortsAndFilters(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	a := ResourceActor{"local", "owner", "p"}
	for _, name := range []string{"Bravo", "Alpha", "Charlie"} {
		name := name
		if _, e = s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create-" + name, Name: &name}, nil); e != nil {
			t.Fatal(e)
		}
	}
	phase34SettlePlaylistProjection(t, db)
	if _, e = db.Exec(`UPDATE catalog_playlists SET entry_count=CASE name WHEN 'Alpha' THEN 1 WHEN 'Bravo' THEN 9 ELSE 4 END,created_at=CASE name WHEN 'Alpha' THEN '2026-01-03T00:00:00Z' WHEN 'Bravo' THEN '2026-01-01T00:00:00Z' ELSE '2026-01-02T00:00:00Z' END`); e != nil {
		t.Fatal(e)
	}
	names := func(r ContentRequest) string {
		t.Helper()
		out, e := s.PlaylistDirectory(r, a)
		if e != nil {
			t.Fatal(e)
		}
		list := []string{}
		for _, section := range out.Sections {
			for _, entry := range section.Entries {
				list = append(list, entry.Title)
			}
		}
		return strings.Join(list, ",")
	}
	base := ContentRequest{ServerID: "s", Profile: "p", ViewerFence: "f", View: "playlists", Limit: 10}
	for sort, want := range map[string]string{"title": "Alpha,Bravo,Charlie", "created": "Alpha,Charlie,Bravo", "count": "Bravo,Charlie,Alpha"} {
		r := base
		r.Sort = sort
		if got := names(r); got != want {
			t.Fatalf("%s: %s", sort, got)
		}
	}
	r := base
	r.Sort, r.Direction, r.Limit = "count", "asc", 1
	first, e := s.PlaylistDirectory(r, a)
	if e != nil || first.Sections[0].Entries[0].Title != "Alpha" || first.Sections[0].NextCursor == "" {
		t.Fatal(first, e)
	}
	r.Cursor = first.Sections[0].NextCursor
	if got := names(r); got != "Charlie" {
		t.Fatalf("count cursor continued at %s", got)
	}
	r = base
	r.Filter = "shared"
	if got := names(r); got != "" {
		t.Fatalf("shared filter listed %s", got)
	}
	r.Filter = "mine"
	if got := names(r); got != "Alpha,Bravo,Charlie" {
		t.Fatalf("mine filter listed %s", got)
	}
	r.Filter = "nobody"
	if _, e = s.PlaylistDirectory(r, a); e == nil {
		t.Fatal("an unknown filter was accepted")
	}
}
