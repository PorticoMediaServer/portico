package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestListeningTimelineHTTPScopesPagingAndPrivateFacts(t *testing.T) {
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
	books := c.Library("books", "Books", "audiobook", "/private/books")
	secret := c.Library("secret", "Secret", "audiobook", "/private/secret")
	book := c.Book(books, "Book", "Author")
	first := c.BookFile(book, 1, "/private/books/part-one.m4b")
	second := c.BookFile(book, 2, "/private/books/part-two.m4b")
	denied := c.Book(secret, "Secret title", "Secret author")
	c.BookFile(denied, 1, "/private/secret/part.m4b")
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1),('member','member',x'00','profile',1),('other','other',x'00','other-profile',1)`)
	c.Exec(`UPDATE direct_memberships SET role='member',allowed_libraries='["books"]' WHERE account_id IN('member','other')`)
	c.Drain()
	token, err := ident.Issue("member", "profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ident.Issue("other", "other-profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)})
	get := func(path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	path := "/v1/books/" + book.Public + "/timeline?limit=1"
	w := get(path, token.AccessToken)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	var envelope struct {
		Scope map[string]string    `json:"scope"`
		Data  catalog.BookTimeline `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Scope["profileId"] != "profile" || envelope.Data.TotalCount != 2 || len(envelope.Data.Parts) != 1 || envelope.Data.Parts[0].ItemID != first.Public || envelope.Data.NextCursor == "" || bytes.Contains(w.Body.Bytes(), []byte("/private/")) {
		t.Fatal(w.Body.String())
	}
	continued := path + "&cursor=" + url.QueryEscape(envelope.Data.NextCursor)
	w = get(continued, token.AccessToken)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(second.Public)) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = get(continued, other.AccessToken)
	if w.Code == 200 {
		t.Fatal("cursor crossed current profile")
	}
	for _, route := range []string{"/v1/books/" + denied.Public + "/timeline", "/v1/items/" + denied.Public + "/similar"} {
		w = get(route, token.AccessToken)
		if w.Code == 200 || bytes.Contains(w.Body.Bytes(), []byte("Secret title")) || bytes.Contains(w.Body.Bytes(), []byte("Secret author")) {
			t.Fatal("denied listening facts leaked", w.Code, w.Body.String())
		}
	}
	for _, route := range []string{"/v1/items/99999999999999999999/similar", "/v1/items/not-an-id/similar", "/v1/books/99999999999999999999/timeline"} {
		w = get(route, token.AccessToken)
		if w.Code != 404 {
			t.Fatal("malformed listening identity", w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"positionMs=-1", "positionMs=nope", "positionMs=999999999"} {
		w = get("/v1/books/"+book.Public+"/timeline?"+query, token.AccessToken)
		if w.Code != 400 {
			t.Fatal("unverified seek accepted", w.Code, w.Body.String())
		}
	}
}
