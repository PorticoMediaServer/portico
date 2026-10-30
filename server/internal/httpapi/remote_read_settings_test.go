package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSourceReadSettingsHTTPUsesCurrentOwnerCASAndDurableReceipt(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	state := t.TempDir()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d.Mounts, err = mounts.New(d.DB, filepath.Join(state, "managed"), "", helper, storage.New(helper))
	if err != nil {
		t.Fatal(err)
	}
	d.RemoteSources, err = remotesources.New(d.DB, state, d.Mounts)
	if err != nil {
		t.Fatal(err)
	}
	// This is a DB-only authority/profile fixture; no network configuration is
	// installed and no provider admission or transport is asserted here.
	if _, err = d.DB.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation) VALUES('profile-source','webdav','Profile source',?,1)`, filepath.Join(state, "source")); err != nil {
		t.Fatal(err)
	}
	if _, err = d.DB.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',X'00','member-profile',1)`); err != nil {
		t.Fatal(err)
	}
	member, err := d.Identity.Issue("member", "member-profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Register the actual complete API: isolated route registration missed the
	// read-profile/WebDAV wildcard conflict and allowed an unbootable server.
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		registry.ServeHTTP(w, r)
		return w
	}
	path := "/v1/storage/sources/profile-source/settings/read-profile"
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {member.AccessToken, 403}} {
		if w := request("GET", path, actor.token, nil); w.Code != actor.status {
			t.Fatal("read did not enforce owner", w.Code, w.Body.String())
		}
	}
	w := request("GET", path, owner.AccessToken, nil)
	var before SourceReadSettingsResponse
	if err = json.Unmarshal(w.Body.Bytes(), &before); err != nil || w.Code != 200 || before.Settings.Saved || before.Settings.Values.ScanDepth != "file_list_only" || before.Settings.Revision != 0 || before.Settings.Generation != "1" {
		t.Fatal("read inferred a saved profile", w.Code, before, err)
	}
	advanced := false
	change := remotesources.SourceReadSettingsChange{OperationID: fmt.Sprintf("%013d-00000000-0000-4000-8000-000000000801", time.Now().UnixMilli()), ExpectedGeneration: "1", Values: remotesources.SourceReadValues{ScanDepth: "basic", WholeFileReads: false, Connections: 4, RequestsPerSecond: 4, BytesPerSecond: 0, AllowAdvancedMediaAnalysis: &advanced}}
	if w = request("PUT", path, member.AccessToken, change); w.Code != 403 {
		t.Fatal("member changed source settings", w.Code, w.Body.String())
	}
	w = request("PUT", path, owner.AccessToken, change)
	var receipt SourceReadSettingsReceiptResponse
	if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || w.Code != 200 || receipt.Receipt.ReadSettings == nil || receipt.Receipt.ReadSettings.Revision != 1 || !reflect.DeepEqual(receipt.Receipt.ReadSettings.Values, change.Values) {
		t.Fatal("owner save did not publish exact settings", w.Code, receipt, err)
	}
	if w = request("PUT", path, owner.AccessToken, change); w.Code != 200 {
		t.Fatal("exact receipt retry failed", w.Code, w.Body.String())
	}
	changed := change
	changed.Values.Connections = 5
	if w = request("PUT", path, owner.AccessToken, changed); w.Code != 422 || !strings.Contains(w.Body.String(), "idempotency_key_reused") {
		t.Fatal("operation reused with changed values", w.Code, w.Body.String())
	}
	changed.OperationID = fmt.Sprintf("%013d-00000000-0000-4000-8000-000000000802", time.Now().UnixMilli())
	if w = request("PUT", path, owner.AccessToken, changed); w.Code != 412 {
		t.Fatal("stale profile revision accepted", w.Code, w.Body.String())
	}
	changed.ExpectedRevision = 1
	changed.ExpectedGeneration = "2"
	if w = request("PUT", path, owner.AccessToken, changed); w.Code != 412 {
		t.Fatal("stale source generation accepted", w.Code, w.Body.String())
	}
	partial := map[string]any{"operationId": change.OperationID, "expectedGeneration": "1", "expectedRevision": 1, "values": map[string]any{"scanDepth": "basic", "connections": 4, "requestsPerSecond": 4, "bytesPerSecond": 0}}
	if w = request("PUT", path, owner.AccessToken, partial); w.Code != 400 {
		t.Fatal("omitted false value accepted", w.Code, w.Body.String())
	}
	if w = request("GET", "/v1/storage/sources/unknown/settings/read-profile", owner.AccessToken, nil); w.Code != 404 {
		t.Fatal("unknown source inherited settings", w.Code, w.Body.String())
	}
	if _, err = d.DB.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, owner.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if w = request("PUT", path, owner.AccessToken, change); w.Code != http.StatusUnauthorized {
		t.Fatal("revoked owner recovered settings through save", w.Code, w.Body.String())
	}
}
