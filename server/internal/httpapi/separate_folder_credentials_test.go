package httpapi

import (
	"net/http"
	"net/url"
	"testing"
)

// Exercise the real credential gate: issued keys authenticate as their live
// local member, rather than an invented api-key principal passed to catalog.
func TestSeparateFolderHTTPPreservesCredentialScopesAndRole(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.69s")
	}
	f := newSeparateFolderHTTPFixture(t, 2)
	path := "/v1/admin/libraries/music/inventory/" + url.PathEscape(f.objects[0]) + "/separate-folder"
	body := `{"revision":"r1"}`
	readKey := issueCredential(t, f.administrationFixture, "/v1/admin/api-keys", f.owner,
		`{"operationId":"folder-read-key","name":"Read automation","scopes":["read"]}`)
	memberToken := issueCredential(t, f.administrationFixture, "/v1/me/access-tokens", f.member,
		`{"operationId":"folder-member-token","name":"Member automation","scopes":["read","library","admin"]}`)
	for _, caller := range []struct {
		name, bearer, code string
	}{
		{"read key", readKey.Secret, "insufficient_scope"},
		{"member token with admin scope", memberToken.Secret, "forbidden"},
	} {
		t.Run(caller.name, func(t *testing.T) {
			w := f.call("POST", path, caller.bearer, body)
			if w.Code != http.StatusForbidden || registryErrorCode(t, w) != caller.code {
				t.Fatalf("request returned HTTP %d, code %q", w.Code, registryErrorCode(t, w))
			}
			if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='separate'`); n != 0 {
				t.Fatalf("refused credential admitted %d separation rows", n)
			}
		})
	}
	adminKey := issueCredential(t, f.administrationFixture, "/v1/admin/api-keys", f.owner,
		`{"operationId":"folder-admin-key","name":"Folder automation","scopes":["admin"]}`)
	w := f.call("POST", path, adminKey.Secret, body)
	if w.Code != http.StatusOK {
		t.Fatalf("admin key returned HTTP %d, code %q", w.Code, registryErrorCode(t, w))
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='separate' AND parent_mutation_id IS NULL AND status='terminal' AND outcome='separated' AND owner_id LIKE 'separate-local-v1:%'`); n != 1 {
		t.Fatalf("admin key did not complete with one bound local actor: %d", n)
	}
}
