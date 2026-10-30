package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// The backup and state-permissions routes are owner-only; backups list,
// start, stage and delete over HTTP, and the fix tightens loose permissions.
func TestBackupRoutesOwnerBoundaryAndLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	owner, err := ident.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	member, _ := ident.Issue("member", "member-profile", "hosted", "member", 1)
	svc := backup.New(root, db, "srv-test", "test", persistence.SchemaVersion())
	handler := New(Dependencies{DB: db, Identity: ident, Administration: administration.New(db), Console: operations.New(db), Backups: svc})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/v1/admin/backups", ""},
		{"POST", "/v1/admin/backups", `{"operationId":"op-12345678"}`},
		{"DELETE", "/v1/admin/backups/2026-09-24T013000Z", ""},
		{"POST", "/v1/admin/backups/restore", `{"operationId":"op-restore-1","source":{"backupId":"2026-09-24T013000Z"}}`},
		{"POST", "/v1/admin/state-permissions:fix", `{}`},
	} {
		for _, token := range []string{"", member.AccessToken} {
			if w := request(route.method, route.path, token, route.body); w.Code != 401 {
				t.Fatalf("%s %s with a non-owner token answered %d: %s", route.method, route.path, w.Code, w.Body.String())
			}
		}
	}
	got := request("GET", "/v1/admin/backups", owner.AccessToken, "")
	if got.Code != 200 {
		t.Fatalf("GET backups as owner answered %d: %s", got.Code, got.Body.String())
	}
	assertSpecResponse(t, "GET", "/v1/admin/backups", got)
	started := request("POST", "/v1/admin/backups", owner.AccessToken, `{"operationId":"op-12345678"}`)
	if started.Code != 202 {
		t.Fatalf("POST backups answered %d: %s", started.Code, started.Body.String())
	}
	var startedDoc struct {
		JobID string `json:"jobId"`
	}
	if err = json.Unmarshal(started.Body.Bytes(), &startedDoc); err != nil || startedDoc.JobID == "" {
		t.Fatalf("start backup answer: %s %v", started.Body.String(), err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		listed, err := svc.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup job never finished")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The same operation id replays the same job.
	again := request("POST", "/v1/admin/backups", owner.AccessToken, `{"operationId":"op-12345678"}`)
	var againDoc struct {
		JobID string `json:"jobId"`
	}
	if err = json.Unmarshal(again.Body.Bytes(), &againDoc); err != nil || againDoc.JobID != startedDoc.JobID {
		t.Fatalf("replay answered: %s %v", again.Body.String(), err)
	}
	listed, err := svc.List()
	if err != nil || len(listed) != 1 {
		t.Fatal(err)
	}
	restored := request("POST", "/v1/admin/backups/restore", owner.AccessToken, `{"operationId":"op-restore-1","source":{"backupId":"`+listed[0].ID+`"}}`)
	if restored.Code != 202 {
		t.Fatalf("POST restore answered %d: %s", restored.Code, restored.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/admin/backups/restore", restored)
	for {
		progress := svc.RestoreProgress()
		if progress != nil && progress.Phase == "staged" {
			break
		}
		if progress != nil && progress.ErrorCode != "" {
			t.Fatal(progress)
		}
		if time.Now().After(deadline) {
			t.Fatal("restore never staged")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = os.Stat(storagepaths.Path(root, "restore-staged", "restore.json")); err != nil {
		t.Fatal("restore not staged", err)
	}
	bad := request("POST", "/v1/admin/backups/restore", owner.AccessToken, `{"operationId":"op-restore-2","source":{"backupId":"2026-01-01T000000Z"}}`)
	if bad.Code != 409 {
		t.Fatalf("unknown backup staged: %d %s", bad.Code, bad.Body.String())
	}
	deleted := request("DELETE", "/v1/admin/backups/"+listed[0].ID, owner.AccessToken, "")
	if deleted.Code != 204 {
		t.Fatalf("DELETE answered %d: %s", deleted.Code, deleted.Body.String())
	}
	missing := request("DELETE", "/v1/admin/backups/2026-01-01T000000Z", owner.AccessToken, "")
	if missing.Code != 404 {
		t.Fatalf("missing backup deleted: %d", missing.Code)
	}
	// Loosened permissions are fixed, not refused.
	loose := filepath.Join(root, "loose.txt")
	if err = os.WriteFile(loose, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	fixed := request("POST", "/v1/admin/state-permissions:fix", owner.AccessToken, `{}`)
	if fixed.Code != 200 {
		t.Fatalf("fix answered %d: %s", fixed.Code, fixed.Body.String())
	}
	var fixDoc struct {
		Exposed bool `json:"exposed"`
	}
	if err = json.Unmarshal(fixed.Body.Bytes(), &fixDoc); err != nil || fixDoc.Exposed {
		t.Fatalf("fix answer: %s %v", fixed.Body.String(), err)
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("state folder still loose")
	}
}
