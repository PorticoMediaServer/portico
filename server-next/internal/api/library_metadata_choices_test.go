package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
)

func TestMetadataChoicesExistBeforeTheFirstLibrary(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	read := func() AdminLibraryPage { return get[AdminLibraryPage](t, s, owner, "/v1/admin/libraries") }
	before := read()
	if len(before.Items) != 0 || len(before.MetadataProviders) != 7 {
		t.Fatalf("choices must not depend on an existing library: %+v", before)
	}
	for kind, choices := range before.MetadataProviders {
		if !slices.ContainsFunc(choices, func(p metadata.LibraryMetadataProvider) bool { return p.ID == "local" && p.Available }) {
			t.Fatalf("local must always be offered for %s: %+v", kind, choices)
		}
	}
	for _, choice := range before.MetadataProviders[db.LibraryShows] {
		if choice.ID == "tvdb" && (choice.Available || !strings.Contains(choice.Reason, "Server › Metadata")) {
			t.Fatalf("TheTVDB must explain its unavailable key: %+v", choice)
		}
	}
	provider := tmdb.New("fixture-token", tmdbtest.New(t).URL)
	s.UseProvider(libraryNamedProvider{Provider: provider, name: "tvdb"})
	s.UseProvider(libraryNamedProvider{Provider: provider, name: "anilist"})
	after := read()
	for kind, name := range map[string]string{db.LibraryShows: "tvdb", db.LibraryAnime: "anilist"} {
		if !slices.ContainsFunc(after.MetadataProviders[kind], func(p metadata.LibraryMetadataProvider) bool { return p.ID == name && p.Available }) {
			t.Fatalf("the current registration is missing for %s: %+v", kind, after.MetadataProviders[kind])
		}
	}
	if slices.ContainsFunc(after.MetadataProviders[db.LibraryMovies], func(p metadata.LibraryMetadataProvider) bool { return p.ID == "anilist" }) {
		t.Fatal("AniList must not be offered for Movies")
	}
}
