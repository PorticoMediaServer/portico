package api

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestLibraryLanguageChoicesAndValidation(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	page := get[AdminLibraryPage](t, s, owner, "/v1/admin/libraries")
	if len(page.Items) != 0 {
		t.Fatal("choices should be available before creating a library")
	}
	for _, kind := range []string{db.LibraryMovies, db.LibraryShows, db.LibraryAnime} {
		for _, provider := range page.MetadataProviders[kind] {
			switch provider.ID {
			case "tmdb", "tvdb":
				if len(provider.Languages) != 28 || provider.DefaultLanguage != "en" || !slices.Contains(provider.Languages, "fr") {
					t.Fatalf("video language choices: %+v", provider)
				}
			case "anilist":
				if !slices.Equal(provider.Languages, []string{"en"}) || provider.DefaultLanguage != "en" {
					t.Fatalf("AniList choices: %+v", provider)
				}
			case "local":
				if len(provider.Languages) != 0 || provider.DefaultLanguage != "" {
					t.Fatalf("local language choices: %+v", provider)
				}
			}
		}
	}
	for _, provider := range page.MetadataProviders[db.LibraryMusic] {
		if len(provider.Languages) != 0 || provider.DefaultLanguage != "" {
			t.Fatalf("Music language choices: %+v", provider)
		}
	}
	create := LibraryCreate{Name: "Anime", Kind: db.LibraryAnime, MetadataProvider: "anilist", Language: "ja", Folders: []string{t.TempDir()}}
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, nil); w.Code != 400 || !strings.Contains(w.Body.String(), "Choose a language offered by this metadata provider.") {
		t.Fatalf("unsupported POST language: %d %s", w.Code, w.Body)
	}
	create.Language = "en"
	var library AdminLibrary
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, &library); w.Code != 200 {
		t.Fatalf("English create: %d %s", w.Code, w.Body)
	}
	japanese := "ja"
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+library.ID, owner, LibraryEdit{Language: &japanese}, nil); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "Choose a language offered by this metadata provider.") {
		t.Fatalf("unsupported PATCH language: %d %s", w.Code, w.Body)
	}
	read := get[AdminLibrary](t, s, owner, "/v1/admin/libraries/"+library.ID)
	if read.Language != "en" {
		t.Fatalf("refused edit changed language: %+v", read)
	}
	if !reflect.DeepEqual(read.MetadataProviders, page.MetadataProviders[db.LibraryAnime]) {
		t.Fatal("library read differs from pre-create language choices")
	}
}

func TestLibraryProviderSwitchResetsUnsupportedLanguage(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	var library AdminLibrary
	create := LibraryCreate{Name: "Anime", Kind: db.LibraryAnime, Language: "fr", Folders: []string{t.TempDir()}}
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, &library); w.Code != 200 {
		t.Fatalf("French create: %d %s", w.Code, w.Body)
	}
	anilist := "anilist"
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+library.ID, owner, LibraryEdit{MetadataProvider: &anilist}, &library); w.Code != 200 {
		t.Fatalf("provider switch: %d %s", w.Code, w.Body)
	}
	if library.MetadataProvider != "anilist" || library.Language != "en" {
		t.Fatalf("unsupported French retained: %+v", library)
	}
	id, err := s.db.LibraryID(context.Background(), library.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.db.LibrarySource(context.Background(), id)
	if err != nil || stored.Language != "en" {
		t.Fatalf("reset was not stored: %+v %v", stored, err)
	}
	local := "local"
	var localLibrary AdminLibrary
	if w := call(t, s, "PATCH", "/v1/admin/libraries/"+library.ID, owner, LibraryEdit{MetadataProvider: &local}, &localLibrary); w.Code != 200 {
		t.Fatalf("local switch: %d %s", w.Code, w.Body)
	}
	stored, err = s.db.LibrarySource(context.Background(), id)
	if err != nil || stored.Language != "" || localLibrary.Language != "" {
		t.Fatalf("local kept a language: %+v %+v %v", stored, localLibrary, err)
	}
}

func TestLibraryEmptyLanguageReadsProviderDefault(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	// An older library stored with no language. A read resolves the default, without a migration.
	legacy, err := s.db.NewLibrary(context.Background(), db.NewLibrary{Name: "Older films", Kind: db.LibraryMovies, Folders: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	single := get[AdminLibrary](t, s, owner, "/v1/admin/libraries/"+legacy.PublicID)
	page := get[AdminLibraryPage](t, s, owner, "/v1/admin/libraries")
	if single.Language != "en" || len(page.Items) != 1 || page.Items[0].Language != "en" {
		t.Fatalf("default not read: %+v %+v", single, page.Items)
	}
	stored, err := s.db.LibrarySource(context.Background(), legacy.ID)
	if err != nil || stored.Language != "" {
		t.Fatalf("read rewrote stored language: %+v %v", stored, err)
	}
}
