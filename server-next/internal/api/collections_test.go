package api

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

type stubProber struct{}

func (stubProber) Probe(context.Context, string) (scan.Facts, error) {
	return scan.Facts{Container: "mkv", DurationMS: 60_000, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264"}}}, nil
}

// The owner makes a collection of two films, renames it, adds, removes and orders its titles,
// and deletes it; a member may not; a film of another library is refused. A collection whose
// last film leaves the server goes with it.
func TestLibraryCollections(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	signedIn := setup(t, s)
	owner := onMain(t, s, signedIn).AccessToken
	root := t.TempDir()
	var libs []db.Library
	for _, folder := range []string{"films", "more"} {
		dir := filepath.Join(root, folder)
		os.MkdirAll(dir, 0o755)
		for i, name := range []string{"Heat (1995).mkv", "Alien (1979).mkv", "Brazil (1985).mkv"} {
			os.WriteFile(filepath.Join(dir, name), make([]byte, 100+i), 0o644)
		}
		lib, err := s.db.CreateLibrary(ctx, folder, db.LibraryMovies, "en", []string{dir})
		if err != nil {
			t.Fatal(err)
		}
		libs = append(libs, lib)
	}
	scanAll := func() {
		all, err := s.db.AllLibraries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range all {
			if _, err := (&scan.Scanner{DB: s.db, Prober: stubProber{}}).Scan(ctx, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	scanAll()
	films := map[string]string{}
	for _, it := range get[ItemPage](t, s, owner, "/v1/items?kind=movie&library="+libs[0].PublicID).Items {
		films[it.Title] = it.ID
	}
	other := get[ItemPage](t, s, owner, "/v1/items?kind=movie&library="+libs[1].PublicID).Items[0].ID
	do := func(method, path, key string, body, out any, want int) {
		t.Helper()
		r := newRequest(method, path, owner, body)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s answered %d, want %d: %s", method, path, w.Code, want, w.Body)
		}
		if out != nil {
			jsonInto(w, out)
		}
	}
	// held is what a collection's page shows it holds, in its order.
	held := func(id string) (titles []string) {
		t.Helper()
		for _, group := range get[Related](t, s, owner, "/v1/items/"+id+"/related").Groups {
			for _, it := range group.Items {
				if group.Relation == "collection_member" {
					titles = append(titles, it.Title)
				}
			}
		}
		return titles
	}

	create := CollectionCreate{Title: "Favourites", ItemIDs: []string{films["Heat"], films["Alien"]}}
	var made, again CollectionCreated
	do("POST", "/v1/admin/libraries/"+libs[0].PublicID+"/collections", "k1", create, &made, 200)
	do("POST", "/v1/admin/libraries/"+libs[0].PublicID+"/collections", "k1", create, &again, 200)
	if made.ID == "" || again.ID != made.ID {
		t.Fatalf("a repeated create made %q then %q", made.ID, again.ID)
	}
	if got := held(made.ID); !slices.Equal(got, []string{"Heat", "Alien"}) {
		t.Fatalf("the new collection holds %v", got)
	}
	do("POST", "/v1/admin/libraries/"+libs[0].PublicID+"/collections", "", CollectionCreate{Title: "Mixed", ItemIDs: []string{other}}, nil, 400)

	title := "Best of"
	edit := CollectionEdit{Title: &title, Add: []string{films["Brazil"], films["Heat"]}, Remove: []string{films["Alien"]}}
	do("PATCH", "/v1/admin/collections/"+made.ID, "", edit, nil, 204)
	do("PATCH", "/v1/admin/collections/"+made.ID, "", edit, nil, 204) // the same edit again changes nothing
	if item := get[Item](t, s, owner, "/v1/items/"+made.ID); item.Title != "Best of" || item.Kind != "collection" {
		t.Fatalf("after the rename: %+v", item)
	}
	if got := held(made.ID); !slices.Equal(got, []string{"Heat", "Brazil"}) {
		t.Fatalf("after adding and removing: %v", got)
	}
	do("PATCH", "/v1/admin/collections/"+made.ID, "", CollectionEdit{Order: []string{films["Brazil"]}}, nil, 204)
	if got := held(made.ID); !slices.Equal(got, []string{"Brazil", "Heat"}) {
		t.Fatalf("after putting Brazil first: %v", got)
	}

	viewers, err := seed.Profiles(ctx, s.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	started, _ := s.tokens.CheckAccess(signedIn.Session.AccessToken, time.Now())
	member, _ := s.tokens.Access(auth.Claims{UserID: viewers[0].UserID, ProfileID: viewers[0].ProfileID, Role: "member", Boot: started.Boot},
		time.Now(), time.Now().Add(time.Hour))
	if w := call(t, s, "DELETE", "/v1/admin/collections/"+made.ID, member, nil, nil); w.Code != 403 {
		t.Fatalf("a member deleting a library's collection was answered %d", w.Code)
	}

	// Heat's file goes and the trash is emptied: a collection holding only Heat goes with it,
	// and one holding Heat and Brazil keeps Brazil.
	var only CollectionCreated
	do("POST", "/v1/admin/libraries/"+libs[0].PublicID+"/collections", "", CollectionCreate{Title: "Just Heat", ItemIDs: []string{films["Heat"]}}, &only, 200)
	os.Remove(filepath.Join(root, "films", "Heat (1995).mkv"))
	scanAll()
	if _, err := s.db.EmptyTrash(ctx, libs[0].ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	do("GET", "/v1/items/"+only.ID, "", nil, nil, 404)
	if got := held(made.ID); !slices.Equal(got, []string{"Brazil"}) {
		t.Fatalf("after Heat left the server the collection holds %v", got)
	}

	do("DELETE", "/v1/admin/collections/"+made.ID, "", nil, nil, 204)
	do("DELETE", "/v1/admin/collections/"+made.ID, "", nil, nil, 204)
	do("GET", "/v1/items/"+made.ID, "", nil, nil, 404)
	do("GET", "/v1/items/"+films["Brazil"], "", nil, nil, 200)
}
