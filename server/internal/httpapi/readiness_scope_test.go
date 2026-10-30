package httpapi

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A rebuild serves the previous published generation without exposing a preparing state.
func TestBrowseServesItsPublishedGenerationDuringDerivedRebuild(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalogtest.New(t, db)
	movies := cat.Library("movies", "Movies", "movie", "/movies")
	cat.Movie(movies, "/movies/film.mkv", "Film", 2000)
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	owner, err := ident.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	cat.Drain()
	if _, err = db.Exec(`UPDATE catalog_derivations SET rebuilding=1 WHERE domain=18`); err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)})
	r := httptest.NewRequest("POST", "/v1/libraries/movies/browse", strings.NewReader(`{"pivot":"movies","limit":10}`))
	r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"title":"Film"`) {
		t.Fatalf("browse during derived rebuild: %d %s", w.Code, w.Body.String())
	}
}
