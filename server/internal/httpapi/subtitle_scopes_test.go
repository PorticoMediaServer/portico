package httpapi

import (
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func TestSubtitleCredentialScopesPreserveLibraryBoundaries(t *testing.T) {
	f := administrationFixtureNew(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Subtitles, err = subtitles.New(subtitles.Options{DB: f.deps.DB, Directory: t.TempDir(), Storage: storage.New(binary), HelperBinary: binary, Authorize: f.deps.AuthorizeSubtitles})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.deps.Subtitles.Close() })
	f.handler = New(f.deps)
	c := catalogtest.New(t, f.deps.DB)
	library := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Movie(library, "/movies/film.mkv", "Film", 2000)
	c.Drain()
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"subtitle-read-key","name":"Reader","scopes":["read"]}`)
	play := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"subtitle-play-key","name":"Player","scopes":["playback"]}`)
	base := "/v1/items/" + item.Public
	if w := f.call("GET", base+"/subtitles", read.Secret, ""); w.Code != 200 {
		t.Fatalf("subtitle read: %d %s", w.Code, w.Body)
	}
	for _, request := range []struct{ method, path, credential string }{
		{"POST", base + "/subtitles", read.Secret},
		{"POST", base + "/subtitles/import", play.Secret},
	} {
		w := f.call(request.method, request.path, request.credential, "{}")
		if w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
			t.Fatalf("scope boundary %s %s: %d %s", request.method, request.path, w.Code, w.Body)
		}
	}
}
