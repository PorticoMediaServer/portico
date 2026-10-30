package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
)

func TestPathsChangedStrictUsesNativeOwnerAndScopeBoundary(t *testing.T) {
	f := administrationFixtureNew(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "New"), 0700); err != nil {
		t.Fatal(err)
	}
	library, err := f.deps.Catalog.Create("Strict", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Ingestion = ingestion.New(f.deps.DB, f.deps.Catalog, assets.Probe{})
	f.handler = New(f.deps)
	body, err := json.Marshal(PathsChangedRequest{Mode: "strict", Paths: []PathChange{{Path: filepath.Join(root, "New"), Change: "added"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []struct {
		token string
		code  int
	}{{"", 401}, {f.member, 403}, {f.admin, 403}} {
		if w := f.call("POST", "/v1/libraries/paths-changed", caller.token, string(body)); w.Code != caller.code {
			t.Fatalf("auth: %d %s", w.Code, w.Body)
		}
	}
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"strict-key-read","name":"Reader","scopes":["read","playback"]}`)
	if w := f.call("POST", "/v1/libraries/paths-changed", read.Secret, string(body)); w.Code != 403 {
		t.Fatalf("scope: %d %s", w.Code, w.Body)
	}
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"strict-key-library","name":"Library","scopes":["library"]}`)
	// Helper audience tags are descriptive metadata. A library-scoped native
	// credential still goes through the same native owner/scope checks.
	ordinary, err := f.deps.Ingestion.Queue(library.ID)
	if err != nil {
		t.Fatal(err)
	}
	busy := f.call("POST", "/v1/libraries/paths-changed", key.Secret, string(body))
	assertStrictDeferred(t, busy.Code, busy.Body.Bytes(), 202)
	if err = f.deps.Ingestion.Control(context.Background(), ordinary.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	w := f.call("POST", "/v1/libraries/paths-changed", key.Secret, string(body))
	var out ingestion.PathReport
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 202 || len(out.Jobs) != 1 || len(out.Paths) != 1 || out.Paths[0].Folder != "New" || out.Paths[0].LibraryID != library.ID || out.Jobs[0].ScanMode != "incremental" {
		t.Fatalf("strict route: %d %s", w.Code, w.Body)
	}
	if w = f.call("POST", "/v1/libraries/paths-changed", key.Secret, `{"mode":"broad","paths":[{"path":"/x"}]}`); w.Code != 400 || registryErrorCode(t, w) != "invalid_request" {
		t.Fatalf("invalid mode: %d %s", w.Code, w.Body)
	}
	rootBody, err := json.Marshal(PathsChangedRequest{Mode: "strict", Paths: []PathChange{{Path: root}}})
	if err != nil {
		t.Fatal(err)
	}
	w = f.call("POST", "/v1/libraries/paths-changed", key.Secret, string(rootBody))
	if w.Code != 400 || registryErrorCode(t, w) != "invalid_request" {
		t.Fatalf("root refusal: %d %s", w.Code, w.Body)
	}
	var response struct {
		Error struct {
			Fields []struct {
				Code string `json:"code"`
			} `json:"fields"`
		} `json:"error"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Error.Fields) != 1 || response.Error.Fields[0].Code != string(ingestion.StrictRootRefused) {
		t.Fatalf("missing typed refusal: %s", w.Body)
	}
	if _, err = f.deps.DB.Exec(`INSERT INTO server_settings(singleton,"integrations.compatibilityEnabled") VALUES(1,1) ON CONFLICT(singleton) DO UPDATE SET "integrations.compatibilityEnabled"=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.deps.DB.Exec(`UPDATE jobs SET status='running' WHERE id=?`, out.Jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	// The adapter durably accepts the same strict targets while busy; it neither
	// widens them nor invents a job or a successful-but-unrecorded update.
	foreignBody, _ := json.Marshal(map[string]any{"Updates": []any{map[string]string{"Path": filepath.Join(root, "New"), "UpdateType": "Created"}}})
	w = compatibilityCall(f, "POST", "/Library/Media/Updated", key.Secret, string(foreignBody))
	assertStrictDeferred(t, w.Code, w.Body.Bytes(), 200)

}

func assertStrictRetry(t *testing.T, status int, retryHeader string, body []byte, reason ingestion.StrictRefusalReason) {
	t.Helper()
	var response struct {
		Error struct {
			Code, Reason, Retry string
			RetryAfterMs        int64 `json:"retryAfterMs"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if status != 503 || retryHeader != "2" || response.Error.Code != "administration_unavailable" || response.Error.Reason != string(reason) || response.Error.Retry != "same_request" || response.Error.RetryAfterMs != 2000 {
		t.Fatalf("transient strict response: %d Retry-After=%s %s", status, retryHeader, body)
	}
}

func assertStrictDeferred(t *testing.T, status int, body []byte, expected int) {
	t.Helper()
	var out ingestion.PathReport
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if status != expected || !out.Deferred || len(out.Jobs) != 0 || len(out.Paths) != 1 || out.Paths[0].Status != "deferred" || out.Paths[0].ScanScope != "" {
		t.Fatalf("not truthful exact-folder acceptance: %d %s", status, body)
	}
}

func TestStrictDeferredGlobalCapacityKeepsRetryableBoundary(t *testing.T) {
	f, library := refreshFixture(t)
	var source string
	if err := f.deps.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id=?`, library).Scan(&source); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		root := filepath.Join(t.TempDir(), fmt.Sprint(i))
		if _, err := f.deps.DB.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root,root_identity,identity_confirmed) VALUES(?,?,?,?,?,1)`, fmt.Sprintf("capacity-%d", i), library, root, root, "capacity-root"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.deps.DB.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<4096) INSERT INTO inventory_strict_intents(source_id,relative_path,library_id,source_generation,root_incarnation,root_identity,root,configured_root,source_kind,follow_symlinks,mode,created_ms) SELECT s.id,'Folder-'||x,s.library_id,s.generation,s.incarnation,s.root_identity,s.root,s.configured_root,s.kind,s.follow_symlinks,'strict',1 FROM n JOIN library_sources s ON s.id='capacity-'||CAST((x-1)/500 AS INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.deps.Ingestion.QueueMode(context.Background(), library, source, "", nil); err != nil {
		t.Fatal(err)
	}
	var root string
	if err := f.deps.DB.QueryRow(`SELECT root FROM library_sources WHERE id=?`, source).Scan(&root); err != nil {
		t.Fatal(err)
	}
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"strict-global-cap","name":"Library","scopes":["library"]}`)
	body, _ := json.Marshal(PathsChangedRequest{Mode: "strict", Paths: []PathChange{{Path: filepath.Join(root, "Films"), Change: "added"}}})
	w := f.call("POST", "/v1/libraries/paths-changed", key.Secret, string(body))
	assertStrictRetry(t, w.Code, w.Header().Get("Retry-After"), w.Body.Bytes(), ingestion.StrictOverloaded)
	var intents, jobs int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM inventory_strict_intents`).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if intents != 4096 || jobs != 1 {
		t.Fatal("last-resort capacity answer dropped accepted work or invented a job")
	}
}
