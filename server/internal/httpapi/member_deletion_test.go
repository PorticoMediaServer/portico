package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The member deletion routes: read what will go, then confirm with the
// revision that was shown.
func TestMemberDeletionRoutes(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	passwordAccount(t, f, "leaver", "Testing1!")
	passwordAccount(t, f, "bystander", "Testing1!")
	_, _, bystander := directSignIn(t, f, "bystander", "Testing1!")
	_, _, leaver := directSignIn(t, f, "leaver", "Testing1!")
	if bystander.Session == nil || leaver.Session == nil {
		t.Fatal("fixture sign-ins failed")
	}
	path := "/v1/direct/members/leaver"

	// A member may neither look nor delete, and is not signed out for asking.
	if w := f.raw("GET", path+"/deletion-preview", bystander.Session.AccessToken, nil, nil); w.Code != 403 || v1Code(w) != "not_permitted" {
		t.Fatalf("a member previewing a deletion: %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("DELETE", path, bystander.Session.AccessToken, nil, map[string]any{"expectedRevision": 1}); w.Code != 403 || v1Code(w) != "not_permitted" {
		t.Fatalf("a member deleting a member: %d %s", w.Code, w.Body.String())
	}
	tokenWorks(t, f, bystander.Session.AccessToken, 200)
	// No session at all is a 401.
	if w := f.raw("DELETE", path, "", nil, map[string]any{"expectedRevision": 1}); w.Code != 401 {
		t.Fatalf("no session: %d %s", w.Code, w.Body.String())
	}

	w := f.raw("GET", path+"/deletion-preview", f.owner.AccessToken, nil, nil)
	var preview identity.MemberDeletionPreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 || preview.Username != "leaver" || preview.Profiles != 1 || preview.Revision < 1 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if w = f.raw("DELETE", path, f.owner.AccessToken, nil, map[string]any{"expectedRevision": preview.Revision + 5}); w.Code != 412 || v1Code(w) != "revision_mismatch" {
		t.Fatalf("a stale confirmation: %d %s", w.Code, w.Body.String())
	}
	// A change that names no revision is refused the same way on every route.
	if w = f.raw("DELETE", path, f.owner.AccessToken, nil, map[string]any{}); w.Code != 428 || v1Code(w) != "revision_required" {
		t.Fatalf("a confirmation with no revision: %d %s", w.Code, w.Body.String())
	}
	if w = f.raw("DELETE", "/v1/direct/members/"+f.owner.Viewer.AccountID, f.owner.AccessToken, nil, map[string]any{"expectedRevision": 1}); w.Code != 403 {
		t.Fatalf("deleting the owner: %d %s", w.Code, w.Body.String())
	}
	tokenWorks(t, f, leaver.Session.AccessToken, 200)
	if w = f.raw("DELETE", path, f.owner.AccessToken, nil, map[string]any{"expectedRevision": preview.Revision}); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	tokenWorks(t, f, leaver.Session.AccessToken, 401)
	if code, _, _ := directSignIn(t, f, "leaver", "Testing1!"); code != 401 {
		t.Fatalf("a deleted member signed in: %d", code)
	}
	if w = f.raw("GET", path+"/deletion-preview", f.owner.AccessToken, nil, nil); w.Code != 404 {
		t.Fatalf("a deleted member's preview: %d %s", w.Code, w.Body.String())
	}
	// The other member is untouched.
	tokenWorks(t, f, bystander.Session.AccessToken, 200)
}
