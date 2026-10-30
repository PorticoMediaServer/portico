package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
	"testing"
	"time"
)

func TestStorageOwnerProjectionReceiptsAndClock(t *testing.T) {
	root, canonicalErr := filepath.EvalSymlinks(t.TempDir())
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1);`)
	if e != nil {
		t.Fatal(e)
	}
	owner, _ := ident.Issue("owner", "profile", "local", "owner", 1)
	member, _ := ident.Issue("other", "other-profile", "hosted", "member", 1)
	svc, e := mounts.New(db, root, "", "/not-launched", storage.New("/not-launched"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO managed_mounts(id,name,executable,digest,remote,mount_path) VALUES('mount','Cloud','/owner/bin/rclone','digest','secret-remote:',?);`, filepath.Join(root, "mount"))
	if e != nil {
		t.Fatal(e)
	}
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), zeroHostedRootID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Revision(); e != nil {
		t.Fatal("mount revision:", e)
	}
	if _, e = svc.List(); e != nil {
		t.Fatal("mount list:", e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Mounts: svc, Hosted: control})
	req := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tok := range []string{"", member.AccessToken} {
		if w := req("GET", "/v1/storage/mounts", tok, ""); w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := req("GET", "/v1/storage/mounts", owner.AccessToken, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret-remote") || !strings.Contains(w.Body.String(), "viewerFence") {
		t.Fatal(w.Code, w.Body.String())
	}
	for i, check := range []struct{ config, code string }{{"bad", "storage_config_invalid"}, {"[cloud]\ntype=http\nurl=http://fixture.invalid\n", "storage_executable_invalid"}} {
		op := fmt.Sprintf("%013d-00000000-0000-4000-8000-%012d", time.Now().UnixMilli(), 100+i)
		raw, _ := json.Marshal(map[string]any{"operationId": op, "name": "Invalid fixture", "remote": "cloud:root", "config": check.config, "executable": "/not-existing-owner-rclone"})
		w := req("POST", "/v1/storage/mounts", owner.AccessToken, string(raw))
		if w.Code != 400 || !strings.Contains(w.Body.String(), check.code) || strings.Contains(w.Body.String(), "fixture.invalid") {
			t.Fatal("safe validation", w.Code, w.Body.String())
		}
	}
	op := fmt.Sprintf("%013d-00000000-0000-4000-8000-000000000001", time.Now().UnixMilli())
	body := fmt.Sprintf(`{"operationId":%q,"expectedRevision":1,"action":"stop"}`, op)
	w = req("POST", "/v1/storage/mounts/mount/actions", owner.AccessToken, body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	original := w.Body.String()
	if w = req("POST", "/v1/storage/mounts/mount/actions", owner.AccessToken, body); w.Code != 202 || w.Body.String() != original {
		t.Fatal("exact replay", w.Code, w.Body.String())
	}
	if w = req("GET", "/v1/storage/operations/"+op, owner.AccessToken, ""); w.Code != 200 || w.Body.String() != original {
		t.Fatal("recovery", w.Code, w.Body.String())
	}
	if w = req("POST", "/v1/storage/mounts/mount/actions", owner.AccessToken, strings.Replace(body, "stop", "start", 1)); w.Code != 409 {
		t.Fatal("changed body", w.Code, w.Body.String())
	}
	old := fmt.Sprintf("%013d-00000000-0000-4000-8000-000000000002", time.Now().Add(-time.Hour).UnixMilli())
	w = req("POST", "/v1/storage/mounts/mount/actions", owner.AccessToken, fmt.Sprintf(`{"operationId":%q,"expectedRevision":2,"action":"stop"}`, old))
	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 410 || !strings.Contains(w.Body.String(), "serverTime") {
		t.Fatal("clock", w.Code, w.Body.String())
	}
	if _, e = db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id='owner'`); e != nil {
		t.Fatal(e)
	}
	if w = req("GET", "/v1/storage/operations/"+op, owner.AccessToken, ""); w.Code != 401 {
		t.Fatal("revoked replay", w.Code, w.Body.String())
	}
}
