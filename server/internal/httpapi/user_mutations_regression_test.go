package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func mutationFixKey(n int) string { return fmt.Sprintf("p251_%013d_%032x", time.Now().UnixMilli(), n) }
func mutationFixOK(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
}
func mutationFixExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func mutationFixCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func mutationFixCreate(t *testing.T, f preferenceFixture) catalog.SavedResourceReceipt {
	t.Helper()
	key := mutationFixKey(1)
	w := mutationHTTP(t, f, "POST", "/v1/saved-resources", key, map[string]any{"operationId": key, "expectedRevision": 0, "kind": "collection", "name": "Held"})
	mutationFixOK(t, w)
	var out struct{ Receipt catalog.SavedResourceReceipt }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Receipt
}
func mutationFixOwner(t *testing.T) (preferenceFixture, *catalogtest.Catalog) {
	t.Helper()
	c := catalogtest.Open(t)
	id, err := identity.New(c.DB, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`)
	token, err := id.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	f := preferenceFixture{db: c.DB, ident: id, token: token.AccessToken, t: t}
	f.handler = New(Dependencies{DB: c.DB, Identity: id, Catalog: catalog.New(c.DB), Console: operations.New(c.DB)})
	return f, c
}
func mutationFixLookup(t *testing.T, f preferenceFixture, key string, status int, hidden string) *httptest.ResponseRecorder {
	t.Helper()
	w := mutationHTTP(t, f, "GET", "/v1/user-mutations/"+key, "", nil)
	if w.Code != status || status != 200 && hidden != "" && strings.Contains(w.Body.String(), hidden) {
		t.Fatalf("lookup %d: %s", w.Code, w.Body.String())
	}
	return w
}

func TestUserMutationAllFilteredSavedEntriesCannotApplyAfterGrant(t *testing.T) {
	f := preferenceHTTP(t)
	c := catalogtest.New(t, f.db)
	lib := c.Library("hidden", "Hidden", "movie", "/hidden")
	item := c.Movie(lib, "/hidden/movie.mkv", "Hidden", 2020)
	c.Drain()
	made := mutationFixCreate(t, f)
	key := mutationFixKey(2)
	path := "/v1/saved-resources/" + made.ResourceID + "/entries"
	body := map[string]any{"operationId": key, "expectedRevision": made.Revision, "addItemIds": []string{item.Public}}
	w := mutationHTTP(t, f, "PUT", path, key, body)
	mutationFixOK(t, w)
	if !strings.Contains(w.Body.String(), `"not_found"`) {
		t.Fatal(w.Body.String())
	}
	lookup := mutationFixLookup(t, f, key, 200, "")
	if !strings.Contains(lookup.Body.String(), `"status":"partial"`) || strings.Contains(lookup.Body.String(), `"entries":`) || strings.Contains(lookup.Body.String(), item.Public) {
		t.Fatal(lookup.Body.String())
	}
	mutationFixExec(t, f.db, `UPDATE direct_memberships SET revision=revision+1,allowed_libraries='["a","hidden"]' WHERE account_id='member'`)
	mutationFixOK(t, mutationHTTP(t, f, "GET", "/v1/items/"+item.Public, "", nil))
	mutationFixOK(t, mutationHTTP(t, f, "PUT", path, key, body))
	if n := mutationFixCount(t, f.db, `SELECT count(*) FROM saved_resource_entries WHERE resource_id=?`, made.ResourceID); n != 0 {
		t.Fatalf("replay added %d previously rejected items", n)
	}
	if n := mutationFixCount(t, f.db, `SELECT count(*) FROM saved_resource_receipts WHERE operation_id=?`, key); n != 0 {
		t.Fatal("protocol stored a legacy response")
	}
	body["addItemIds"] = []string{item.Public, item.Public}
	w = mutationHTTP(t, f, "PUT", path, key, body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_conflict") {
		t.Fatalf("changed original body: %d %s", w.Code, w.Body.String())
	}
}

func TestUserMutationBatchAndContainerReceiptLookup(t *testing.T) {
	f, c := mutationFixOwner(t)
	lib := c.Library("tv", "TV", "show", "/tv")
	show := c.Show(lib, "Show", 2020)
	season := c.Season(show, 1)
	ep := c.Episode(show, season, 1, "/tv/ep.mkv")
	c.Drain()
	batchKey := mutationFixKey(3)
	mutationFixOK(t, mutationHTTP(t, f, "PUT", "/v1/items/personal-state:batch", batchKey, map[string]any{"operationId": batchKey, "items": []any{map[string]any{"itemId": ep.Public, "favorite": true}}}))
	mutationFixLookup(t, f, batchKey, 200, "")
	key := mutationFixKey(4)
	mutationFixOK(t, mutationHTTP(t, f, "PUT", "/v1/containers/show/"+show.Public+"/personal-state", key, map[string]any{"expectedRevision": 0, "favorite": true}))
	mutationFixLookup(t, f, key, 200, "")
	c.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,revision) VALUES('profile',0,0,1)`)
	mutationFixLookup(t, f, key, 404, show.Public)
	mutationFixLookup(t, f, batchKey, 200, "")
}

func TestUserMutationPersonalDeletedTargetReplayAndCurrentRestrictions(t *testing.T) {
	f, c := mutationFixOwner(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Movie(lib, "/movies/one.mkv", "One", 2020)
	c.Drain()
	key := mutationFixKey(5)
	path := "/v1/items/" + item.Public + "/personal-state"
	body := map[string]any{"operationId": key, "expectedRevision": 0, "favorite": true}
	mutationFixOK(t, mutationHTTP(t, f, "PUT", path, key, body))
	mutationFixLookup(t, f, key, 200, "")
	c.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,revision) VALUES('profile',0,0,1)`)
	mutationFixLookup(t, f, key, 404, item.Public)
	w := mutationHTTP(t, f, "PUT", path, key, body)
	if w.Code != 404 || strings.Contains(w.Body.String(), item.Public) {
		t.Fatalf("restricted replay %d: %s", w.Code, w.Body.String())
	}
	c.Exec(`DELETE FROM catalog_entities WHERE id=?`, item.ID)
	mutationFixLookup(t, f, key, 200, "")
	mutationFixOK(t, mutationHTTP(t, f, "PUT", path, key, body))
	body["favorite"] = false
	w = mutationHTTP(t, f, "PUT", path, key, body)
	if w.Code != 409 {
		t.Fatalf("deleted changed replay %d: %s", w.Code, w.Body.String())
	}
	if n := mutationFixCount(t, f.db, `SELECT count(*) FROM user_mutation_receipts WHERE key=?`, key); n != 1 {
		t.Fatal(n)
	}
}

func TestUserMutationResourceAndPlaylistLookupReproveOperationPermission(t *testing.T) {
	for _, kind := range []string{"resource", "playlist"} {
		t.Run(kind, func(t *testing.T) {
			f, c := mutationFixOwner(t)
			lib := c.Library("movies", "Movies", "movie", "/movies")
			item := c.Movie(lib, "/movies/one.mkv", "One", 2020)
			c.Drain()
			c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',x'00','member-profile',1)`)
			session, err := f.ident.Issue("member", "member-profile", "local", "member", 1)
			if err != nil {
				t.Fatal(err)
			}
			member := f
			member.token = session.AccessToken
			c.Exec(`UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id='member'`)
			var id, path, shareTable, shareColumn, deleteSQL string
			key := mutationFixKey(6)
			if kind == "resource" {
				made := mutationFixCreate(t, f)
				id = made.ResourceID
				path = "/v1/saved-resources/" + id
				shareTable, shareColumn = "saved_resource_shares", "resource_id"
				deleteSQL = `DELETE FROM saved_resources WHERE id=?`
			} else {
				w := mutationHTTP(t, f, "POST", "/v1/playlists", key, map[string]any{"operationId": key, "name": "Held"})
				mutationFixOK(t, w)
				var out struct{ PlaylistID string }
				if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				id = out.PlaylistID
				path = "/v1/playlists/" + id
				shareTable, shareColumn = "playlist_shares", "playlist_id"
				deleteSQL = `DELETE FROM catalog_playlists WHERE token=?`
			}
			mutationFixExec(t, f.db, `INSERT INTO `+shareTable+` VALUES(?,'local','member','member-profile','editor')`, id)
			key = mutationFixKey(7)
			body := map[string]any{"operationId": key, "expectedRevision": 1, "addItemIds": []string{item.Public}}
			method := "PUT"
			if kind == "playlist" {
				method = "POST"
				body = map[string]any{"operationId": key, "expectedRevision": 1, "itemId": item.Public}
			}
			mutationFixOK(t, mutationHTTP(t, member, method, path+"/entries", key, body))
			mutationFixLookup(t, member, key, 200, "")
			mutationFixExec(t, f.db, `UPDATE `+shareTable+` SET role='viewer' WHERE `+shareColumn+`=?`, id)
			mutationFixOK(t, mutationHTTP(t, member, "GET", path, "", nil))
			mutationFixLookup(t, member, key, 404, id)
			mutationFixExec(t, f.db, `UPDATE `+shareTable+` SET role='editor' WHERE `+shareColumn+`=?`, id)
			mutationFixLookup(t, member, key, 200, "")
			mutationFixExec(t, f.db, `DELETE FROM `+shareTable+` WHERE `+shareColumn+`=?`, id)
			w := mutationHTTP(t, member, "GET", path, "", nil)
			if w.Code != 404 {
				t.Fatalf("revoked GET: %d %s", w.Code, w.Body.String())
			}
			mutationFixLookup(t, member, key, 404, id)
			mutationFixExec(t, f.db, deleteSQL, id)
			mutationFixLookup(t, member, key, 200, "")
		})
	}
}

func TestUserMutationSavedEntriesQuotaAndStorageErrorsRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, phase := range []string{"quota", "storage", "quota-filtered", "storage-filtered"} {
		t.Run(phase, func(t *testing.T) {
			f, c := mutationFixOwner(t)
			lib := c.Library("movies", "Movies", "movie", "/movies")
			item := c.Movie(lib, "/movies/one.mkv", "One", 2020)
			c.Drain()
			made := mutationFixCreate(t, f)
			key := mutationFixKey(8)
			if strings.HasSuffix(phase, "-filtered") {
				c.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,revision) VALUES('profile',0,0,1)`)
			}
			status := 500
			if strings.HasPrefix(phase, "quota") {
				status = 503
				c.Exec(`CREATE TRIGGER fail_http_receipt BEFORE INSERT ON user_mutation_receipts BEGIN SELECT RAISE(ABORT,'user_mutation_capacity'); END`)
			} else {
				c.Exec(`CREATE TRIGGER fail_http_receipt BEFORE UPDATE OF status ON user_mutation_receipts WHEN NEW.status<>'reserved' BEGIN SELECT RAISE(ABORT,'http_receipt_storage_failure'); END`)
			}
			w := mutationHTTP(t, f, "PUT", "/v1/saved-resources/"+made.ResourceID+"/entries", key, map[string]any{"operationId": key, "expectedRevision": made.Revision, "addItemIds": []string{item.Public}})
			if w.Code != status {
				t.Fatalf("%s error %d: %s", phase, w.Code, w.Body.String())
			}
			if strings.HasPrefix(phase, "quota") && (w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "writer_capacity")) {
				t.Fatal("capacity refusal lost retry policy", w.Body.String())
			}
			if n := mutationFixCount(t, f.db, `SELECT count(*) FROM saved_resource_entries WHERE resource_id=?`, made.ResourceID); n != 0 {
				t.Fatal("membership effect survived failure")
			}
			if n := mutationFixCount(t, f.db, `SELECT count(*) FROM saved_resources WHERE id=? AND revision=?`, made.ResourceID, made.Revision); n != 1 {
				t.Fatal("revision effect survived failure")
			}
			if n := mutationFixCount(t, f.db, `SELECT count(*) FROM user_mutation_receipts WHERE key=?`, key); n != 0 {
				t.Fatal("receipt survived failure")
			}
		})
	}
}

func TestUserMutationOwnerSettingsLookupRequiresCurrentOwner(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(fmt.Sprint("owner=", owner), func(t *testing.T) {
			var f preferenceFixture
			status := 403
			if owner {
				f, _ = mutationFixOwner(t)
				status = 200
			} else {
				f = preferenceHTTP(t)
			}
			p, err := f.ident.Authenticate(f.token)
			if err != nil {
				t.Fatal(err)
			}
			key := mutationFixKey(9)
			// A family-bound compact receipt alone cannot satisfy owner-only metadata.
			err = dbwork.WithWriteTx(dbwork.WithDurableUser(context.Background()), f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				family, e := f.ident.SessionFamilyTx(context.Background(), tx, p)
				if e != nil {
					return e
				}
				b := usermutation.Binding{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, FamilyID: family.ID}
				envelope := usermutation.Envelope{Key: key, Operation: "put_admin_backup_settings", Digest: strings.Repeat("a", 64)}
				if _, e = usermutation.Check(context.Background(), tx, b, envelope, time.Now().UnixMilli()); e != nil {
					return e
				}
				return usermutation.Save(context.Background(), tx, b, envelope, usermutation.Result{Status: "applied"}, time.Now().UnixMilli())
			})
			if err != nil {
				t.Fatal(err)
			}
			mutationFixLookup(t, f, key, status, "")
		})
	}
}

// The completion method refuses a retained read snapshot. Exercise the real
// HTTP Stop/lookup composition and keep a successor's mutable state untouched.
func TestUserMutationStopLookupCompletesOutsideSnapshot(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "http-stop-lookup-start"}, startBody(f.items[0], nil), 201, &session)
	key := mutationFixKey(10)
	f.call("DELETE", "/v1/playback/sessions/"+session.ID, map[string]string{"Idempotency-Key": key}, map[string]any{"generation": session.Presentation.Generation, "positionMs": 1000}, 204, nil)
	w := f.raw("GET", "/v1/user-mutations/"+key, f.owner.AccessToken, nil, nil)
	mutationFixOK(t, w)
	if !strings.Contains(w.Body.String(), `"operation":"stop_playback_session"`) || !strings.Contains(w.Body.String(), session.ID) {
		t.Fatal(w.Body.String())
	}
	mutationFixExec(t, f.db, `UPDATE playback_v1_sessions SET generation=generation+1,ended_ms=0,end_reason='',position_ms=123 WHERE id=?`, session.ID)
	mutationFixOK(t, f.raw("GET", "/v1/user-mutations/"+key, f.owner.AccessToken, nil, nil))
	if n := mutationFixCount(t, f.db, `SELECT count(*) FROM playback_v1_sessions WHERE id=? AND generation=? AND ended_ms=0 AND position_ms=123`, session.ID, session.Presentation.Generation+1); n != 1 {
		t.Fatal("receipt lookup changed a successor occurrence")
	}
}
