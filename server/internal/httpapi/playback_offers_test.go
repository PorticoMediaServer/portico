package httpapi

import (
	"encoding/base64"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strings"
	"testing"
)

func TestPlaybackOffersViewerBoundaryAndSafeProjection(t *testing.T) {
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
	db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`)
	catalogue := catalogtest.New(t, db)
	library := catalogue.Library("lib", "Library", "movie", "/private-secret-path")
	item := catalogue.Movie(library, "/private-secret-path/token.mp4", "Movie", 2000)
	catalogue.Drain()
	owner, _ := ident.Issue("owner", "profile", "local", "owner", 1)
	member, _ := ident.Issue("member", "member-profile", "hosted", "member", 1)
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), hostedtrust.KeyID(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Hosted: control, Catalog: catalog.New(db), Playback: playback.New(db)})
	req := func(token, query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/items/"+item.Public+"/playback-offers"+query, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", member.AccessToken} {
		w := req(token, "")
		if w.Code != 401 {
			t.Fatal("viewer boundary", w.Code, w.Body.String())
		}
	}
	w := req(owner.AccessToken, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), `"enabled":true`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = req(owner.AccessToken, "?revision=changed"); w.Code != 409 || !strings.Contains(w.Body.String(), "stale_playback_offer") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, q := range []string{"?other=1", "?sessionId=1&sessionId=2"} {
		if w = req(owner.AccessToken, q); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	principal, e := ident.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	session, e := playback.New(db).Create(principal, item.Public, "automatic", "chapters-http")
	if e != nil {
		t.Fatal(e)
	}
	for _, check := range []struct {
		token, query string
		code         int
	}{{owner.AccessToken, "", 200}, {"", "", 401}, {member.AccessToken, "", 401}, {owner.AccessToken, "?limit=101", 400}} {
		r := httptest.NewRequest("GET", "/v1/playback/sessions/"+session.ID+"/chapters"+check.query, nil)
		r.Header.Set("Authorization", "Bearer "+check.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != check.code {
			t.Fatal("chapter HTTP boundary", w.Code, w.Body.String())
		}
		if w.Code == 200 && (!strings.Contains(w.Body.String(), "chapter_facts_unavailable") || !strings.Contains(w.Body.String(), `"sourceId":"`+item.Token+`"`) || strings.Contains(w.Body.String(), "private-secret")) {
			t.Fatal("chapter unavailable scope", w.Body.String())
		}
	}
	db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id='owner'`)
	if w = req(owner.AccessToken, ""); w.Code != 401 {
		t.Fatal("revoked projection", w.Code)
	}
}

func TestAuthenticationAdmissionSafeRetryBoundary(t *testing.T) {
	w := httptest.NewRecorder()
	failure(w, identity.ErrBusy)
	if w.Code != 503 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), `"code":"authentication_busy"`) || !strings.Contains(w.Body.String(), `"retryable":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
