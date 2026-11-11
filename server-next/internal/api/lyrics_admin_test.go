package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/lrclib"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/lrclib/lrclibtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// The owner chooses LRCLIB for a music library and what a scan finds gets lyrics, shown with
// LRCLIB's credit; pasted lyrics come first and no fetch replaces them; pasted text is checked;
// a member may read lyrics but not set them.
func TestLyricsRoutes(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	signedIn := setup(t, s)
	owner := onMain(t, s, signedIn).AccessToken
	server := lrclibtest.New(t)
	s.metadata.Lyrics = lrclib.New(server.URL, 0)
	lib, err := s.db.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	libraries, _ := s.db.AllLibraries(ctx)
	paths := libraries[0].Paths
	artist, album := db.ItemKey{Kind: db.KeyName, Key: "artist"}, db.ItemKey{Kind: db.KeyName, Key: "album"}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{RelPath: ""}},
		Items: []db.ImportItem{{Kind: db.KindArtist, Title: "The Beatles", Keys: []db.ItemKey{artist}},
			{Kind: db.KindAlbum, Title: "Abbey Road", Parent: &artist, Keys: []db.ItemKey{album}}}}
	for n, title := range []string{"Come Together", "Something"} {
		ref := db.FileRef{Name: title + ".flac"}
		batch.Files = append(batch.Files, db.ImportFile{FileRef: ref, Role: db.FileMedia, Size: 1000, Container: "flac", DurationMS: 260_000})
		index, disc := n+1, 1
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindTrack, Title: title, Parent: &album, IndexNumber: &index, ParentIndex: &disc,
			Keys: []db.ItemKey{{Kind: db.KeyFile, Key: ref.Name}}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: ref}}}}})
	}
	if _, err := s.db.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	songs := map[string]string{}
	for _, it := range get[ItemPage](t, s, owner, "/v1/items?kind=track&library="+lib.PublicID).Items {
		songs[it.Title] = it.ID
	}
	lyrics := func(song string) []Lyrics {
		t.Helper()
		return get[Page[Lyrics]](t, s, owner, "/v1/items/"+songs[song]+"/lyrics").Items
	}

	// Files only, the default: what a scan finds is not looked up.
	afterScan := func() {
		t.Helper()
		job, err := s.jobs.Start(ctx, jobAfterScan, jobs.Request{Target: jobs.Target{Library: lib.PublicID}})
		if err != nil {
			t.Fatal(err)
		}
		waitForJob(t, s, owner, job.PublicID)
	}
	afterScan()
	if got := lyrics("Come Together"); len(got) != 0 || server.Requests.Load() != 0 {
		t.Fatalf("a Files-only library looked lyrics up: %+v", got)
	}
	var admin AdminLibrary
	call(t, s, "GET", "/v1/admin/libraries/"+lib.PublicID, owner, nil, &admin)
	options := admin.Options
	options.Lyrics = "lrclib"
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+lib.PublicID, owner, LibraryEdit{Options: &options}, &admin); w.Code != 200 || admin.Options.Lyrics != "lrclib" {
		t.Fatalf("choosing LRCLIB: %d %+v", w.Code, admin.Options)
	}
	afterScan()
	if got := lyrics("Come Together"); len(got) != 1 || got[0].Origin != "provider" || got[0].Format != "lrc" || got[0].Credit != "Lyrics provided by LRCLIB" {
		t.Fatalf("lyrics after the scan's lookup: %+v", got)
	}

	// Pasted lyrics are what everyone sees, and a fetch keeps them.
	var pasted Page[Lyrics]
	if w := call(t, s, "PUT", "/v1/admin/items/"+songs["Come Together"]+"/lyrics", owner, LyricsPaste{Text: "Here come old flat top"}, &pasted); w.Code != 200 ||
		pasted.Items[0].Origin != "pasted" || pasted.Items[0].Credit != "" {
		t.Fatalf("pasting: %d %+v", w.Code, pasted.Items)
	}
	var fetched LyricsFetched
	if w := call(t, s, "POST", "/v1/admin/items/"+songs["Come Together"]+"/lyrics/fetch", owner, nil, &fetched); w.Code != 200 || fetched.Found {
		t.Fatalf("a fetch over pasted lyrics: %d %+v", w.Code, fetched)
	}
	if got := lyrics("Come Together"); got[0].Text != "Here come old flat top" {
		t.Fatalf("a fetch replaced pasted lyrics: %+v", got)
	}
	for _, bad := range []LyricsPaste{{Text: "a line\x1b[2J"}, {Text: "fine", Language: "english"}, {Text: "  "}, {Text: "abc‮dcb"}} {
		if w := call(t, s, "PUT", "/v1/admin/items/"+songs["Something"]+"/lyrics", owner, bad, nil); w.Code != 400 {
			t.Errorf("pasting %q answered %d", bad.Text, w.Code)
		}
	}

	// Fetch missing lyrics for the library, by hand.
	var job Job
	if w := call(t, s, "POST", "/v1/jobs", owner, JobStart{Kind: "fetch_lyrics", Target: JobTarget{Library: lib.PublicID}}, &job); w.Code != 202 {
		t.Fatalf("Fetch missing lyrics answered %d", w.Code)
	}
	if done := waitForJob(t, s, owner, job.ID); done.State != "done" {
		t.Fatalf("Fetch missing lyrics: %+v", done)
	}

	viewers, _ := seed.Profiles(ctx, s.db, 1)
	started, _ := s.tokens.CheckAccess(signedIn.Session.AccessToken, time.Now())
	member, _ := s.tokens.Access(auth.Claims{UserID: viewers[0].UserID, ProfileID: viewers[0].ProfileID, Role: "member", Boot: started.Boot}, time.Now(), time.Now().Add(time.Hour))
	if w := call(t, s, "GET", "/v1/items/"+songs["Come Together"]+"/lyrics", member, nil, nil); w.Code != 200 {
		t.Fatalf("a member reading lyrics: %d", w.Code)
	}
	if w := call(t, s, "PUT", "/v1/admin/items/"+songs["Come Together"]+"/lyrics", member, LyricsPaste{Text: "mine"}, nil); w.Code != 403 {
		t.Fatalf("a member pasting lyrics: %d", w.Code)
	}
}
