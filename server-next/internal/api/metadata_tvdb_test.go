package api

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tvdb/tvdbtest"
)

func TestTVDBOwnerKeyAndLibraryIDEntry(t *testing.T) {
	sh := shelfOf(t, db.LibraryShows, "Quiet Harbour/Season 1/Quiet Harbour - S01E01.mkv")
	stand := tvdbtest.New(t)
	s, owner := sh.s, sh.owner
	path := filepath.Join(s.dataDir, "main.db")
	s.UseTVDBHost(stand.URL)
	availability := func(want bool) {
		t.Helper()
		library := get[AdminLibrary](t, s, owner, "/v1/admin/libraries/"+sh.library)
		for _, provider := range library.MetadataProviders {
			if provider.ID != "tvdb" {
				continue
			}
			if provider.Available != want {
				t.Fatalf("TheTVDB availability: %+v", provider)
			}
			if want && provider.Reason != "" || !want && provider.Reason != "TheTVDB needs the owner's key in Server › Metadata." {
				t.Fatalf("TheTVDB reason: %+v", provider)
			}
			return
		}
		t.Fatal("TheTVDB is absent from the library's choices")
	}
	asks := func(want bool) {
		t.Helper()
		got, err := s.metadata.Asks(context.Background(), sh.library)
		if err != nil || got != want {
			t.Fatalf("availability: %v %v; want %v", got, err, want)
		}
	}
	availability(false)
	var log lockedBuffer
	const ownKey = "the-owner-project-key-for-tvdb"
	save := func(key string) MetadataSettings {
		t.Helper()
		var p MetadataSettings
		w := call(t, s, "PUT", "/v1/admin/metadata/settings", owner, MetadataSettingsChange{TVDBProjectKey: &key}, &p)
		if w.Code != 200 {
			t.Fatalf("saving key: %d %s", w.Code, w.Body)
		}
		if bytes.Contains(w.Body.Bytes(), []byte(ownKey)) {
			t.Fatal("settings answer holds the key")
		}
		return p
	}
	if p := save(ownKey); !p.TVDBCredentialConfigured || p.TVDBCredentialUnreadable {
		t.Fatalf("saved key status: %+v", p)
	}
	availability(true)
	choice, language, region := "tvdb", "fr", "CA"
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+sh.library, owner,
		LibraryEdit{MetadataProvider: &choice, Language: &language, Region: &region}, nil); w.Code != 200 {
		t.Fatalf("selecting TheTVDB: %d %s", w.Code, w.Body)
	}
	asks(true)
	shows := get[ItemPage](t, s, owner, "/v1/items?library="+sh.library+"&kind=show")
	if len(shows.Items) != 1 {
		t.Fatalf("shows: %+v", shows.Items)
	}
	for _, ref := range []string{"42", "https://thetvdb.com/series/quiet-harbour"} {
		found := get[MatchCandidates](t, s, owner, "/v1/admin/items/"+shows.Items[0].ID+"/matches?ref="+url.QueryEscape(ref))
		if len(found.Items) != 1 || found.Items[0].Provider != "tvdb" || found.Items[0].ID != "42" || found.Items[0].Title != "Le port tranquille" {
			t.Fatalf("provider id %s: %+v", ref, found)
		}
	}
	sh.do("PUT", "/v1/admin/items/"+shows.Items[0].ID+"/match", MatchRequest{Provider: "tvdb", ID: "42"}, nil, 200)
	episodes := get[ItemPage](t, s, owner, "/v1/items?library="+sh.library+"&kind=episode")
	if len(episodes.Items) != 1 || episodes.Items[0].Title != "Chapter 1" {
		t.Fatalf("matched local episode: %+v", episodes.Items)
	}
	episode := get[Item](t, s, owner, "/v1/items/"+episodes.Items[0].ID)
	if episode.ExternalIDs["tvdb"] != "100" {
		t.Fatalf("episode id: %+v", episode.ExternalIDs)
	}
	if key := stand.Key.Load(); key == nil || *key != ownKey {
		t.Fatal("the provider did not use the owner's key")
	}
	for _, file := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(ownKey)) {
			t.Fatal("database holds an unsealed key")
		}
	}
	if w := call(t, s, "GET", "/v1/admin/settings", owner, nil, nil); bytes.Contains(w.Body.Bytes(), []byte(ownKey)) {
		t.Fatal("settings documents expose key")
	}
	stopServer(s)
	closeDB(s.db)
	s = openLoggedServer(t, path, &log)
	owner = onMain(t, s, signIn(t, s, 1)).AccessToken
	s.UseTVDBHost(stand.URL)
	availability(true)
	asks(true)
	if p := get[MetadataSettings](t, s, owner, "/v1/admin/metadata/settings"); !p.TVDBCredentialConfigured {
		t.Fatal("key not reopened on restart")
	}
	if p := save(""); p.TVDBCredentialConfigured || p.TVDBCredentialUnreadable {
		t.Fatalf("cleared key status: %+v", p)
	}
	availability(false)
	asks(false)
	beforeTVDB, beforeTMDB := stand.Requests.Load(), sh.tmdb.Requests.Load()
	if w := call(t, s, "GET", "/v1/admin/items/"+shows.Items[0].ID+"/matches?ref=42", owner, nil, nil); w.Code != 409 {
		t.Fatalf("unavailable provider: %d %s", w.Code, w.Body)
	}
	if stand.Requests.Load() != beforeTVDB || sh.tmdb.Requests.Load() != beforeTMDB {
		t.Fatal("unavailable provider fell back")
	}
	if p := s.metadata.Providers["tvdb"]; p != nil {
		t.Fatal("cleared key left TVDB registered")
	}
	save(ownKey)
	stopServer(s)
	closeDB(s.db)
	if err := os.Remove(filepath.Join(filepath.Dir(path), "secrets.key")); err != nil {
		t.Fatal(err)
	}
	s = openLoggedServer(t, path, &log)
	owner = onMain(t, s, signIn(t, s, 2)).AccessToken
	availability(false)
	asks(false)
	if p := get[MetadataSettings](t, s, owner, "/v1/admin/metadata/settings"); p.TVDBCredentialConfigured || !p.TVDBCredentialUnreadable {
		t.Fatalf("unreadable key status: %+v", p)
	}
	if !log.contains("cannot be opened") || log.contains(ownKey) {
		t.Fatal("unreadable key log missing, or exposes key")
	}
}
