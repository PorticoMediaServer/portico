package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"slices"
	"strings"
	"testing"
)

func TestAdminOwnerScopePathsRenameAndReloadedCancel(t *testing.T) {
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
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1);INSERT INTO libraries(id,name,kind,root) VALUES('lib','Library','movie','/private-owner-only-path');INSERT INTO jobs(id,library_id,status,error,created_at) VALUES('bad','lib','failed','secret signedURL token credential','2026-09-03');`)
	if e != nil {
		t.Fatal(e)
	}
	settleCompactCatalogue(t, db)
	owner, e := ident.Issue("owner", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	member, _ := ident.Issue("member", "member-profile", "hosted", "member", 1)
	hostedOwner, _ := ident.Issue("hosted-owner", "hosted-profile", "hosted", "owner", 1)
	cat := catalog.New(db)
	scan := ingestion.New(db, cat, assets.Probe{})
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), zeroHostedRootID)
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: cat, Ingestion: scan, Hosted: control})
	req := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/v1/admin/libraries", "/v1/admin/libraries/lib", "/v1/admin/jobs", "/v1/admin/jobs?libraryId=lib"} {
		for _, token := range []string{"", member.AccessToken, hostedOwner.AccessToken} {
			if w := req("GET", path, token, ""); w.Code != 401 {
				t.Fatal("owner boundary", path, w.Code)
			}
		}
		if w := req("GET", path, owner.AccessToken, ""); w.Code != 200 || strings.Contains(w.Body.String(), "credential") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := req("GET", "/v1/libraries", owner.AccessToken, ""); w.Code != 200 || strings.Contains(w.Body.String(), "private-owner-only") {
		t.Fatal("public path leak", w.Code, w.Body.String())
	}
	if w := req("PATCH", "/v1/admin/libraries/lib", member.AccessToken, `{"name":"Denied","expectedRevision":1}`); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := req("PATCH", "/v1/admin/libraries/lib", owner.AccessToken, `{"name":"Renamed","expectedRevision":1}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := req("PATCH", "/v1/admin/libraries/lib", owner.AccessToken, `{"name":"Lost update","expectedRevision":1}`); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := req("POST", "/v1/libraries/lib/scans", owner.AccessToken, "")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job ingestion.Job
	json.Unmarshal(w.Body.Bytes(), &job)
	w = req("GET", "/v1/admin/jobs?libraryId=lib&status=queued", owner.AccessToken, "")
	var directory catalog.AdminJobs
	json.Unmarshal(w.Body.Bytes(), &directory)
	if w.Code != 200 || len(directory.Items) != 1 || directory.Items[0].ID != job.ID || !slices.Contains(directory.Items[0].Actions, "cancel") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = req("DELETE", "/v1/admin/ingestion/jobs/"+job.ID, owner.AccessToken, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = req("GET", "/v1/admin/jobs?libraryId=lib&status=cancelled", owner.AccessToken, "")
	json.Unmarshal(w.Body.Bytes(), &directory)
	if w.Code != 200 || len(directory.Items) != 1 || directory.Items[0].FinishedAt == nil || !slices.Contains(directory.Items[0].Actions, "retry") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/admin/jobs?limit=41", "/v1/admin/jobs?status=invalid", "/v1/admin/libraries?limit=1&limit=2", "/v1/admin/libraries/lib?probe=0"} {
		if w = req("GET", path, owner.AccessToken, ""); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	source := t.TempDir()
	body, _ := json.Marshal(map[string]string{"name": "New source", "kind": "movie", "path": source})
	if w = req("POST", "/v1/libraries", owner.AccessToken, string(body)); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	// B71: creating a library queues its first scan in the same commit, and
	// pressing Scan now afterwards joins that job instead of adding another.
	var created catalog.Library
	json.Unmarshal(w.Body.Bytes(), &created)
	w = req("GET", "/v1/admin/jobs?libraryId="+created.ID+"&status=queued", owner.AccessToken, "")
	directory = catalog.AdminJobs{}
	json.Unmarshal(w.Body.Bytes(), &directory)
	if w.Code != 200 || created.ID == "" || len(directory.Items) != 1 {
		t.Fatal("new library has no first scan", w.Code, w.Body.String())
	}
	first := directory.Items[0].ID
	if w = req("POST", "/v1/libraries/"+created.ID+"/scans", owner.AccessToken, ""); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &job)
	if job.ID != first || job.ScanMode != "" && job.ScanMode != "first-import" {
		t.Fatal("manual scan did not join the queued first scan", first, w.Body.String())
	}
	if w = req("POST", "/v1/libraries", owner.AccessToken, string(body)); w.Code != 409 || !strings.Contains(w.Body.String(), "source_already_configured") {
		t.Fatal("duplicate source", w.Code, w.Body.String())
	}
	p, e := ident.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	ident.Logout(p)
	if w = req("GET", "/v1/admin/libraries", owner.AccessToken, ""); w.Code != 401 {
		t.Fatal("revoked owner", w.Code)
	}
}
