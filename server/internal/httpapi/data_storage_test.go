package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"
)

func TestDataStorageOwnerUsageAndDeferredCacheEmpty(t *testing.T) {
	f := newTL11AdmissionFixture(t, "movie")
	l := storagepaths.At(filepath.Join(t.TempDir(), "data"))
	l.Transcode = filepath.Join(filepath.Dir(l.Data), "transcode")
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(l.Cache, "derived"), []byte("cache"), 0600)
	restarted := false
	f.d.StorageLayout = &l
	f.d.Restart = func() { restarted = true }
	h := New(f.d)
	r := logoutHTTPRequest(h, "GET", "/v1/admin/storage/usage", f.token)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var usage storagepaths.Usage
	if err := json.Unmarshal(r.Body.Bytes(), &usage); err != nil || usage.DataDirectory != l.Data || len(usage.Folders) != 8 {
		t.Fatal(usage, err)
	}
	for _, route := range []struct{ method, path string }{{"GET", "/v1/admin/storage/usage"}, {"POST", "/v1/admin/storage/cache/empty"}} {
		r = logoutHTTPRequest(h, route.method, route.path, "")
		if r.Code != http.StatusUnauthorized {
			t.Fatal(route, r.Code, r.Body.String())
		}
	}
	memberDependencies, memberSession, _ := hostedAdministrationFixture(t, "member")
	memberDependencies.StorageLayout = &l
	memberDependencies.Restart = func() { t.Error("member restarted server") }
	for _, route := range []struct{ method, path string }{{"GET", "/v1/admin/storage/usage"}, {"POST", "/v1/admin/storage/cache/empty"}} {
		denied := logoutHTTPRequest(New(memberDependencies), route.method, route.path, memberSession.AccessToken)
		if denied.Code != http.StatusForbidden {
			t.Fatal("member storage access", route, denied.Code, denied.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/v1/admin/storage/cache/empty", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 202 || !restarted {
		t.Fatal(w.Code, w.Body.String(), restarted)
	}
	if _, err := os.Stat(filepath.Join(l.Cache, "derived")); err != nil {
		t.Fatal("removed an active cache", err)
	}
	if err := l.ApplyCacheEmpty(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.Cache, "derived")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := l.Usage(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
