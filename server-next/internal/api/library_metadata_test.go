package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
)

// Registering another implementation is enough to offer it; no request is made to it here.
type libraryNamedProvider struct {
	metadata.Provider
	name string
}

func (p libraryNamedProvider) Name() string { return p.name }

func TestLibraryMetadataChoicesAndRegion(t *testing.T) {
	th := newTheatre(t)
	s, token, id := th.s, th.owner.AccessToken, th.libs[db.LibraryMovies].PublicID
	provider := tmdb.New("fixture-token", tmdbtest.New(t).URL)
	s.UseProvider(provider)
	read := func() AdminLibrary { return get[AdminLibrary](t, s, token, "/v1/admin/libraries/"+id) }
	before := read()
	if before.MetadataProvider != "tmdb" || !slices.Equal(before.MetadataProviders, []string{"local", "tmdb"}) || before.Region == nil {
		t.Fatalf("the current source and available choices: %+v", before)
	}
	local, country := "local", "CA"
	var saved AdminLibrary
	change := LibraryEdit{MetadataProvider: &local, Region: &country, Options: &LibraryOptions{Depth: "complete"}}
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+id, token, change, &saved); w.Code != 200 {
		t.Fatalf("saving the library's source: %d %s", w.Code, w.Body)
	}
	if saved.MetadataProvider != local || saved.Region == nil || *saved.Region != country || saved.Revision == before.Revision {
		t.Fatalf("saved source: %+v", saved)
	}
	// Another settings save cannot erase the region kept beside those options.
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+id, token, LibraryEdit{Options: &LibraryOptions{Depth: "basic"}}, nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	source, err := s.db.LibrarySource(context.Background(), th.libs[db.LibraryMovies].ID)
	if err != nil || source.Provider != local || source.Region != country {
		t.Fatalf("the metadata service must read exactly what was saved: %+v, %v", source, err)
	}
	// The old copy is refused, and invalid or unavailable choices change nothing.
	r := newRequest("PATCH", "/v1/admin/libraries/"+id, token, LibraryEdit{MetadataProvider: new("tmdb")})
	r.Header.Set("If-Match", before.Revision)
	if w := serve(s, r); w.Code != 412 {
		t.Fatalf("stale source edit: %d %s", w.Code, w.Body)
	}
	for _, edit := range []LibraryEdit{{MetadataProvider: new("tvdb")}, {MetadataProvider: new("tmdb"), Region: new("Canada")}} {
		if w := call(t, s, "PATCH", "/v1/admin/libraries/"+id, token, edit, nil); w.Code != 400 {
			t.Fatalf("invalid source edit: %d %s", w.Code, w.Body)
		}
	}
	current := read()
	if current.MetadataProvider != local || current.Region == nil || *current.Region != country {
		t.Fatalf("a refused edit changed the source: %+v", current)
	}
	// The artwork lane's providers become selectable as soon as they are registered.
	s.UseProvider(libraryNamedProvider{Provider: provider, name: "tvdb"})
	if !slices.Contains(read().MetadataProviders, "tvdb") {
		t.Fatal("a newly registered provider is missing")
	}
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+id, token, LibraryEdit{MetadataProvider: new("tvdb")}, nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	s.UseProvider(libraryNamedProvider{Provider: provider, name: "anilist"})
	if slices.Contains(read().MetadataProviders, "anilist") {
		t.Fatal("AniList must not be offered for a Movies library")
	}
	var made AdminLibrary
	create := LibraryCreate{Name: "Anime", Kind: "anime", Folders: []string{t.TempDir()}, MetadataProvider: "anilist", Region: "JP"}
	if w := call(t, s, "POST", "/v1/admin/libraries", token, create, &made); w.Code != 200 {
		t.Fatalf("create with source: %d %s", w.Code, w.Body)
	}
	if made.MetadataProvider != "anilist" || made.Region == nil || *made.Region != "JP" || !slices.Contains(made.MetadataProviders, "anilist") {
		t.Fatalf("created source: %+v", made)
	}
	book := get[AdminLibrary](t, s, token, "/v1/admin/libraries/"+th.libs[db.LibraryMusic].PublicID)
	if book.MetadataProvider != "musicbrainz" || slices.Contains(book.MetadataProviders, "tmdb") || book.Region != nil {
		t.Fatalf("the Music kind's source choices: %+v", book)
	}
}
