package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestBulkDeleteRequiresOwnerOneLibraryAndBoundedSelection(t *testing.T) {
	d, owner, member := tl6SupportFixture(t)
	d.Administration = administration.NewAt(d.DB, t.TempDir())
	c := catalogtest.New(t, d.DB)
	a := c.Movie(c.Handle("private-library"), "/private/SOURCE-PATH/a.mkv", "A", 2020)
	b := c.Movie(c.Handle("private-library"), "/private/SOURCE-PATH/b.mkv", "B", 2021)
	other := c.Library("other", "Other", "movie", "/other")
	x := c.Movie(other, "/other/x.mkv", "X", 2022)
	c.Drain()
	h := New(d)
	preview := func(token string, ids []string) int {
		raw, _ := json.Marshal(map[string]any{"itemIds": ids})
		return editorRequest(t, h, "POST", "/v1/admin/media/delete/preview", token, "application/json", raw).Code
	}
	if code := preview(owner.AccessToken, []string{a.Public, b.Public}); code != 200 {
		t.Fatal("same-library preview", code)
	}
	if code := preview(owner.AccessToken, []string{a.Public, x.Public}); code != 400 {
		t.Fatal("cross-library preview accepted", code)
	}
	if code := preview(owner.AccessToken, []string{"99999999999999999999"}); code != 404 {
		t.Fatal("malformed public identity reached SQL", code)
	}
	if code := preview(member.AccessToken, []string{a.Public}); code < 400 || code >= 500 {
		t.Fatal("member delete authority", code)
	}
	if _, err := d.DB.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	admin, err := d.Identity.Issue("member", "member-profile", "local", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	if code := preview(admin.AccessToken, []string{a.Public}); code < 400 || code >= 500 {
		t.Fatal("administrator gained owner delete", code)
	}
	if code := preview(owner.AccessToken, make([]string, administration.MaxDeleteTargets+1)); code != 400 {
		t.Fatal("uncapped delete selection", code)
	}
	raw, _ := json.Marshal(administration.DeleteRequest{ItemIDs: []string{a.Public, x.Public}, DeleteFiles: true, Confirmation: "2", ExpectedRevision: 0, OperationID: "cross-library-delete"})
	response := editorRequest(t, h, "POST", "/v1/admin/media/delete", owner.AccessToken, "application/json", raw)
	if response.Code != 400 {
		t.Fatalf("cross-library command %d %s", response.Code, response.Body.String())
	}
	var remaining int
	if err := d.DB.QueryRow(`SELECT count(*) FROM catalog_entities WHERE public_id IN(pid_blob(?),pid_blob(?),pid_blob(?))`, a.Public, b.Public, x.Public).Scan(&remaining); err != nil || remaining != 3 {
		t.Fatal("refused selection changed catalogue", remaining, err)
	}
}
