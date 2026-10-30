package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func TestUserMutationPinLookupReprovesCurrentTargetAccess(t *testing.T) {
	for _, kind := range []string{"collection", "view", "playlist"} {
		t.Run(kind, func(t *testing.T) {
			owner, c := mutationFixOwner(t)
			c.Library("movies", "Movies", "movie", "/movies")
			c.Drain()
			c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',x'00','member-profile',1)`)
			session, err := owner.ident.Issue("member", "member-profile", "local", "member", 1)
			if err != nil {
				t.Fatal(err)
			}
			member := owner
			member.token = session.AccessToken
			c.Exec(`UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id='member'`)

			createKey := mutationFixKey(21)
			body := map[string]any{"operationId": createKey, "expectedRevision": 0, "kind": kind, "name": "Pinned target"}
			path, shareTable, shareColumn, targetTable, targetColumn := "/v1/saved-resources", "saved_resource_shares", "resource_id", "saved_resources", "id"
			if kind == "view" {
				body["definition"] = catalog.SavedViewDefinition{LibraryID: "movies", Pivot: "movies", Sort: []catalog.BrowseSortSelection{{Field: "title", Direction: "asc"}}, Presentation: "grid"}
			} else if kind == "playlist" {
				path, shareTable, shareColumn, targetTable, targetColumn = "/v1/playlists", "playlist_shares", "playlist_id", "catalog_playlists", "token"
				delete(body, "kind")
			}
			created := mutationHTTP(t, owner, "POST", path, createKey, body)
			mutationFixOK(t, created)
			var out struct {
				Receipt    catalog.SavedResourceReceipt
				PlaylistID string
			}
			if err = json.Unmarshal(created.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			id := out.Receipt.ResourceID
			if kind == "playlist" {
				id = out.PlaylistID
			}
			if id == "" {
				t.Fatal("create omitted target ID", created.Body.String())
			}
			path += "/" + id
			pinPath := "/v1/saved-pins/" + kind + "/" + id
			memberKey, ownerKey := mutationFixKey(22), mutationFixKey(23)
			pinBody := func(key string) map[string]any {
				return map[string]any{"operationId": key, "expectedRevision": 0, "pinned": true}
			}
			mutationFixExec(t, owner.db, `INSERT INTO `+shareTable+` VALUES(?,'local','member','member-profile','viewer')`, id)
			mutationFixOK(t, mutationHTTP(t, member, "PUT", pinPath, memberKey, pinBody(memberKey)))
			mutationFixOK(t, mutationHTTP(t, owner, "PUT", pinPath, ownerKey, pinBody(ownerKey)))

			checkMember := func(status int) {
				t.Helper()
				lookup := mutationFixLookup(t, member, memberKey, status, id)
				if status == 200 {
					var receipt usermutation.Receipt
					if err := json.Unmarshal(lookup.Body.Bytes(), &receipt); err != nil {
						t.Fatal(err)
					}
					if receipt.Operation != "put_saved_pins_kind_id" || receipt.TargetID != id || receipt.Revision != 1 {
						t.Fatalf("unexpected pin receipt: %+v", receipt)
					}
				}
				replay := mutationHTTP(t, member, "PUT", pinPath, memberKey, pinBody(memberKey))
				if replay.Code != status || status != 200 && strings.Contains(replay.Body.String(), id) {
					t.Fatalf("pin replay: %d %s", replay.Code, replay.Body.String())
				}
			}
			mutationFixOK(t, mutationHTTP(t, member, "GET", path, "", nil))
			checkMember(200) // A viewer may pin; editing permission is unnecessary.
			mutationFixExec(t, owner.db, `UPDATE `+shareTable+` SET role='editor' WHERE `+shareColumn+`=?`, id)
			checkMember(200)
			mutationFixExec(t, owner.db, `UPDATE `+shareTable+` SET role='viewer' WHERE `+shareColumn+`=?`, id)
			checkMember(200)
			mutationFixExec(t, owner.db, `DELETE FROM `+shareTable+` WHERE `+shareColumn+`=?`, id)
			if w := mutationHTTP(t, member, "GET", path, "", nil); w.Code != 404 {
				t.Fatalf("revoked target GET: %d %s", w.Code, w.Body.String())
			}
			checkMember(404)
			mutationFixLookup(t, owner, ownerKey, 200, "")

			if kind == "collection" {
				mutationFixExec(t, owner.db, `UPDATE saved_resources SET visibility='server' WHERE id=?`, id)
				mutationFixOK(t, mutationHTTP(t, member, "GET", path, "", nil))
				checkMember(200) // Server visibility independently grants pin access.
				mutationFixExec(t, owner.db, `UPDATE saved_resources SET visibility='private' WHERE id=?`, id)
				checkMember(404)
			}
			mutationFixExec(t, owner.db, `INSERT INTO `+shareTable+` VALUES(?,'local','member','member-profile','viewer')`, id)
			checkMember(200)
			mutationFixExec(t, owner.db, `UPDATE `+targetTable+` SET deleted=1 WHERE `+targetColumn+`=?`, id)
			checkMember(404) // A tombstone is recoverable only by its owner.
			mutationFixLookup(t, owner, ownerKey, 200, "")
			mutationFixOK(t, mutationHTTP(t, owner, "PUT", pinPath, ownerKey, pinBody(ownerKey)))
			mutationFixExec(t, owner.db, `DELETE FROM `+targetTable+` WHERE `+targetColumn+`=?`, id)
			checkMember(200) // Genuine absence still acknowledges the committed pin.
			mutationFixLookup(t, owner, ownerKey, 200, "")
			changed := pinBody(memberKey)
			changed["pinned"] = false
			if w := mutationHTTP(t, member, "PUT", pinPath, memberKey, changed); w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_conflict") {
				t.Fatalf("changed deleted-target replay: %d %s", w.Code, w.Body.String())
			}
			if n := mutationFixCount(t, owner.db, `SELECT count(*) FROM user_mutation_receipts WHERE key IN(?,?)`, memberKey, ownerKey); n != 2 {
				t.Fatalf("reconciliation changed receipt count: %d", n)
			}
		})
	}
}
