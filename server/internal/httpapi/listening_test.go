package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestListeningHTTPPreferenceEnvelopeAndCAS(t *testing.T) {
	f := listeningPreferencesHTTP(t)
	read := f.call("GET", "/v1/listening/preferences", nil)
	if read.Code != 200 || !bytes.Contains(read.Body.Bytes(), []byte(`"profileId":"profile"`)) {
		t.Fatal(read.Code, read.Body.String())
	}
	for _, removed := range []string{"musicRate", "autoplayNext"} {
		if bytes.Contains(read.Body.Bytes(), []byte(`"`+removed+`"`)) {
			t.Fatal("removed listening field returned", read.Body.String())
		}
	}
	body := map[string]any{"revision": 1, "bookRate": 1.5, "passoutMinutes": 30}
	for _, retired := range []map[string]any{{"musicRate": 1}, {"musicRate": "go"}, {"autoplayNext": true}} {
		withRetired := make(map[string]any)
		for key, value := range body {
			withRetired[key] = value
		}
		for key, value := range retired {
			withRetired[key] = value
		}
		response := f.call("PUT", "/v1/listening/preferences", withRetired)
		if response.Code != 400 {
			t.Fatal("removed listening field accepted", response.Code, response.Body.String())
		}
	}
	for key := range body {
		incomplete := make(map[string]any)
		for k, v := range body {
			if k != key {
				incomplete[k] = v
			}
		}
		response := f.call("PUT", "/v1/listening/preferences", incomplete)
		if response.Code != 400 {
			t.Fatalf("missing %s accepted: %d %s", key, response.Code, response.Body.String())
		}
	}
	first := f.call("PUT", "/v1/listening/preferences", body)
	if first.Code != 200 || !bytes.Contains(first.Body.Bytes(), []byte(`"revision":2`)) {
		t.Fatal(first.Code, first.Body.String())
	}
	stale := f.call("PUT", "/v1/listening/preferences", body)
	if stale.Code != 409 || !bytes.Contains(stale.Body.Bytes(), []byte(`"listening_preferences_conflict"`)) {
		t.Fatal(stale.Code, stale.Body.String())
	}
	for _, id := range []string{f.itemB, "missing"} {
		response := f.call("GET", "/v1/items/"+id+"/listening", nil)
		if response.Code == 200 || bytes.Contains(response.Body.Bytes(), []byte(`"libraryId":"b"`)) {
			t.Fatal("listening metadata leak", response.Code, response.Body.String())
		}
	}
}

// listeningPreferencesHTTP is the ordinary HTTP/auth fixture this route needs:
// one visible music library, one retired library b whose song must stay absent,
// and the real owner profile. Playback bytes are never involved, so the reviewed
// media-helper gate stays untouched for the tests that do need it.
type listeningPreferencesHTTPFixture struct {
	handler http.Handler
	owner   string
	itemB   string
}

func (f listeningPreferencesHTTPFixture) call(method, path string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		if encoded, err := json.Marshal(body); err == nil {
			raw = encoded
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.owner)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func listeningPreferencesHTTP(t *testing.T) listeningPreferencesHTTPFixture {
	t.Helper()
	d, owner, _ := tl6SupportFixture(t)
	c := catalogtest.New(t, d.DB)
	c.Library("a", "Visible", "music", "/a")
	hidden := c.Library("b", "Hidden", "music", "/b")
	artist := c.Artist(hidden, "Hidden Artist")
	album := c.Album(artist, "Hidden Release", 2020)
	song := c.Song(album, 1, "/b/01.mp3", "Hidden Track")
	c.Drain()
	// The library itself is withdrawn, so its song is absent to this viewer while
	// the item row and its libraryId still exist in the catalogue.
	if _, err := d.DB.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='b'`); err != nil {
		t.Fatal(err)
	}
	return listeningPreferencesHTTPFixture{handler: New(d), owner: owner.AccessToken, itemB: song.Public}
}

// Selection counts are exact on the wire, with no totalAtLeast key.
func TestListeningSelectionCountsExactlyOnTheWire(t *testing.T) {
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
	c := catalogtest.New(t, db)
	bigLibrary := c.Library("big", "Big", "music", "/big")
	smallLibrary := c.Library("small", "Small", "music", "/small")
	bigArtist := c.Artist(bigLibrary, "Artist")
	bigAlbum := c.Album(bigArtist, "Release", 2020)
	smallArtist := c.Artist(smallLibrary, "Artist")
	smallAlbum := c.Album(smallArtist, "Release", 2020)
	for i := 1; i <= 3; i++ {
		c.Song(smallAlbum, i, filepath.Join("/small", fmt.Sprintf("small-%d.m4a", i)), fmt.Sprintf("small-%d", i))
	}
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1)`)
	owner, err := ident.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	s := catalog.New(db)
	for {
		more, err := s.RefreshListeningGroups(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	settleCompactCatalogue(t, db)
	// The large fixture still uses the compact-catalogue write API; one
	// transaction keeps fixture setup bounded while the worker derives the same
	// rows a scan would publish.
	const bigSongs = 50
	var firstBigID int64
	bigArtistID, bigAlbumID := bigArtist.ID, bigAlbum.ID
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < bigSongs; i++ {
			path := filepath.Join("/big", fmt.Sprintf("big-%05d.m4a", i))
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: bigLibrary, Kind: compactcatalog.Track, Parent: bigAlbumID, Key: compactcatalog.ItemKey("/big", path, 0), Title: fmt.Sprintf("Big %05d", i), Year: 2020})
			if err != nil {
				return err
			}
			if i == 0 {
				firstBigID = id
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"album_id": bigAlbumID, "track_number": i + 1}); err != nil {
				return err
			}
			if err = compactcatalog.SetSongArtistsTx(ctx, tx, id, []int64{bigArtistID}); err != nil {
				return err
			}
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "m4a", AudioCodec: "aac", Duration: 300})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
				return err
			}
		}
		return nil
	})
	firstBig := c.Public(firstBigID)
	c.Drain()
	for {
		more, err := s.RefreshListeningGroups(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: s})
	get := func(path string) map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	// A startItem past the beginning counts the selection's scope exactly.
	counted := get("/v1/libraries/big/listening/selection?kind=library&entityId=big&mode=ordered&startItem=" + firstBig)
	if _, floor := counted["totalAtLeast"]; floor {
		t.Fatalf("selection carries a floor: %v", counted["totalAtLeast"])
	}
	if counted["totalCount"] != float64(bigSongs) || counted["unavailableCount"] != float64(0) {
		t.Fatalf("selection counts: %v/%v", counted["totalCount"], counted["unavailableCount"])
	}
	if entries, _ := counted["entries"].([]any); len(entries) == 0 {
		t.Fatal("selection returned no entries")
	}
	// The small library's anchorless first page is answered exactly from
	// maintained counts, with no totalAtLeast key.
	plain, err := json.Marshal(get("/v1/libraries/small/listening/selection?kind=library&entityId=small&mode=ordered"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte(`"totalAtLeast"`)) {
		t.Fatalf("uncapped selection carries totalAtLeast: %s", plain)
	}
}
