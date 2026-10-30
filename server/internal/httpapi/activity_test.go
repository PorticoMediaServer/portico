package httpapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strings"
	"testing"
	"time"
)

func TestActivityHTTPAuthorityAndSafeProjection(t *testing.T) {
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
	db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1);INSERT INTO libraries(id,name,kind,root) VALUES('lib','Movie library','movie','/secret-path');INSERT INTO jobs(id,library_id,status,error,created_at) VALUES('job','lib','failed','secret-token','2026-09-05')`)
	settleCompactCatalogue(t, db)
	owner, _ := ident.Issue("owner", "profile", "local", "owner", 1)
	member, _ := ident.Issue("member", "member", "hosted", "member", 1)
	hostOwner, _ := ident.Issue("host", "host", "hosted", "owner", 1)
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), zeroHostedRootID)
	if e != nil {
		t.Fatal(e)
	}
	h := New(Dependencies{DB: db, Identity: ident, Hosted: control, Catalog: catalog.New(db), Playback: playback.New(db)})
	req := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	op := fmt.Sprintf("%013d-00000000-0000-0000-0000-000000000001", time.Now().UnixMilli())
	body := fmt.Sprintf(`{"operationId":%q,"expectedGeneration":1}`, op)
	for _, token := range []string{"", member.AccessToken, hostOwner.AccessToken} {
		for _, route := range []string{"/v1/admin/playback-sessions", "/v1/admin/playback-operations/" + op} {
			if w := req("GET", route, token, ""); w.Code != 401 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		if w := req("POST", "/v1/admin/playback-sessions/id/stop", token, body); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := req("GET", "/v1/admin/playback-sessions", owner.AccessToken, ""); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := req("GET", "/v1/admin/jobs", owner.AccessToken, ""); w.Code != 200 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"libraryName":"Movie library"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, query := range []string{"limit=41", "status=pretend", "status=open&status=recent", "cursor=bad"} {
		if w := req("GET", "/v1/admin/playback-sessions?"+query, owner.AccessToken, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_activity_request") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	p, _ := ident.Authenticate(owner.AccessToken)
	db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash)
	if w := req("GET", "/v1/admin/playback-operations/"+op, owner.AccessToken, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
