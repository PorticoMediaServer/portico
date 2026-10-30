package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestMusicBrainzOwnerEditionSelectionAndScope(t *testing.T) {
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','op',1),('member','member',x'00','mp',1);INSERT INTO libraries(id,name,kind,root) VALUES('lib','Music','music','/music');`)
	if e != nil {
		t.Fatal(e)
	}
	_, artistID := seedHTTPAPICatalogEntity(t, db, "lib", compactcatalog.Artist, compactcatalog.ArtistKey("artist"), "", 0, map[string]any{"local_key": "artist"})
	albumPublic, albumID := seedHTTPAPICatalogEntity(t, db, "lib", compactcatalog.Album, compactcatalog.AlbumKey("album"), "Album", artistID, map[string]any{"artist_id": artistID, "local_key": "album"})
	// A blank local artist prevents provider network work. Let the actual
	// worker capture current album membership/policy evidence, then attach this
	// synthetic observed candidate to that exact operation digest.
	meta := metadata.New(db, "")
	if e = meta.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	const providerID = "11111111-1111-1111-1111-111111111111"
	_, e = db.Exec(`INSERT INTO mb_candidates(kind,entity_id,provider_id,entity_type,title,artist,edition,confidence,decision,query_digest,payload,observed_at) SELECT 'album',? ,?,'release','Album','Artist','CA 2020',0.5,'candidate',query_digest,'{}',created_at FROM mb_publication_operations WHERE kind='album' AND entity_id=? ORDER BY created_at DESC LIMIT 1;INSERT INTO mb_candidate_reasons VALUES('album',?,?,0,'owner_review_required');`, albumID, providerID, albumID, albumID, providerID)
	if e != nil {
		t.Fatal(e)
	}
	owner, _ := ident.Issue("owner", "op", "local", "owner", 1)
	member, _ := ident.Issue("member", "mp", "local", "member", 1)
	control, e := hosted.New(db, ident, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Metadata: meta, Hosted: control})
	request := func(method, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/albums/"+albumPublic+"/metadata/musicbrainz", bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body := `{"expectedRevision":1,"providerId":"11111111-1111-1111-1111-111111111111"}`
	for _, token := range []string{"", member.AccessToken} {
		if w := request("GET", token, ""); w.Code != refusedStatus(token) {
			t.Fatal(w.Code)
		}
		if w := request("PUT", token, body); w.Code != refusedStatus(token) {
			t.Fatal(w.Code)
		}
	}
	w := request("GET", owner.AccessToken, "")
	var state metadata.MBState
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.ServerID != ident.ID() || state.ViewerFence == "" || state.LibraryID != "lib" || len(state.Candidates) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	body = fmt.Sprintf(`{"expectedRevision":%d,"providerId":"11111111-1111-1111-1111-111111111111"}`, state.Revision)
	if w = request("PUT", owner.AccessToken, body); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var authority, account, profile string
	if e = db.QueryRow(`SELECT actor_authority,actor_account_id,actor_profile_id FROM mb_selection_receipts WHERE kind='album' AND entity_id=? AND status='pending'`, albumID).Scan(&authority, &account, &profile); e != nil || authority != "local" || account != "owner" || profile != "op" {
		t.Fatal("selection did not retain authenticated actor", authority, account, profile, e)
	}
	if w = request("PUT", owner.AccessToken, body); w.Code != 409 {
		t.Fatal("stale edition choice admitted", w.Code)
	}
	db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE account_id='owner'`)
	if w = request("GET", owner.AccessToken, ""); w.Code != 401 {
		t.Fatal("revoked owner read", w.Code)
	}
}

func seedHTTPAPICatalogEntity(t *testing.T, db *sql.DB, library string, kind compactcatalog.Kind, key, title string, parent int64, fields map[string]any) (string, int64) {
	t.Helper()
	ctx := context.Background()
	var id int64
	var public string
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		handle, err := compactcatalog.LibraryTx(ctx, tx, library)
		if err != nil {
			return err
		}
		id, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
			Library: handle, Kind: kind, Parent: parent, Key: key, Title: title,
		})
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			if err = compactcatalog.SetFactsTx(ctx, tx, id, fields); err != nil {
				return err
			}
		}
		public, err = entityid.Public(ctx, tx, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return public, id
}
