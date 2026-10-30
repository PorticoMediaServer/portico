package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
)

// A refresh is a real scan request, not a report of "nothing changed" and not
// a fictional job. These probes use the live router and its database.

func refreshFixture(t *testing.T) (administrationFixture, string) {
	t.Helper()
	f := administrationFixtureNew(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "Films"), 0700); err != nil {
		t.Fatal(err)
	}
	library, err := f.deps.Catalog.Create("Refresh", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Ingestion = ingestion.New(f.deps.DB, f.deps.Catalog, assets.Probe{})
	f.handler = New(f.deps)
	return f, library.ID
}

func refreshBody(t *testing.T, library string) string {
	t.Helper()
	raw, err := json.Marshal(IntegrationRefreshRequest{LibraryID: library})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func queuedJobs(t *testing.T, f administrationFixture, library string) int {
	t.Helper()
	var count int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE library_id=? AND status IN('queued','running','paused')`, library).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIntegrationRefreshAnswers202WithTheRealQueuedJob(t *testing.T) {
	f, library := refreshFixture(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"refresh-key","name":"Refresh","scopes":["library"]}`)
	// An omitted libraryId is every enabled, non-private library.
	w := f.call("POST", "/v1/integrations/refresh", key.Secret, `{}`)
	if w.Code != 202 {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	var out ingestion.PathReport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Deferred || len(out.Paths) != 0 || len(out.Jobs) != 1 {
		t.Fatalf("refresh answer must be one queued job and no paths: %s", w.Body)
	}
	job := out.Jobs[0]
	if job.ID == "" || job.Status != "queued" || job.ScanMode != "incremental" || job.SourceID == "" {
		t.Fatalf("refresh invented or misreported its job: %+v", job)
	}
	var owner string
	if err := f.deps.DB.QueryRow(`SELECT library_id FROM jobs WHERE id=?`, job.ID).Scan(&owner); err != nil {
		t.Fatalf("the reported job is not a real row: %v", err)
	}
	if owner != library {
		t.Fatalf("job belongs to %q, refresh named %q", owner, library)
	}
	if got := queuedJobs(t, f, library); got != 1 {
		t.Fatalf("queued jobs for the refreshed library: %d", got)
	}
}

func TestIntegrationRefreshDefersBehindTheSourceSlotWithoutInventingAJob(t *testing.T) {
	f, library := refreshFixture(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"refresh-deferred","name":"Refresh","scopes":["library"]}`)
	existing, err := f.deps.Ingestion.Queue(library)
	if err != nil {
		t.Fatal(err)
	}
	w := f.call("POST", "/v1/integrations/refresh", key.Secret, refreshBody(t, library))
	if w.Code != 202 {
		t.Fatalf("busy refresh: %d %s", w.Code, w.Body)
	}
	var out ingestion.PathReport
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Deferred || len(out.Jobs) != 0 || len(out.Paths) != 0 {
		t.Fatalf("a busy source must defer without a second job: %s", w.Body)
	}
	if strings.Contains(w.Body.String(), existing.ID) {
		t.Fatalf("the deferred answer claims the existing job: %s", w.Body)
	}
	// The accepted refresh survives the source slot: cancelling the real job
	// lets the next genuine request queue it.
	if err = f.deps.Ingestion.Control(context.Background(), existing.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	if w = f.call("POST", "/v1/integrations/refresh", key.Secret, refreshBody(t, library)); w.Code != 202 {
		t.Fatalf("refresh after cancel: %d %s", w.Code, w.Body)
	}
	out = ingestion.PathReport{}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Deferred || len(out.Jobs) != 1 || out.Jobs[0].ID == "" || out.Jobs[0].ID == existing.ID {
		t.Fatalf("the deferred refresh did not become a real, new job: %s", w.Body)
	}
	if got := queuedJobs(t, f, library); got != 1 {
		t.Fatalf("queued jobs after the deferred refresh: %d", got)
	}
}

func TestIntegrationRefreshKeepsOwnerAndLibraryBoundaries(t *testing.T) {
	f, library := refreshFixture(t)
	otherRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherLibrary, err := f.deps.Catalog.Create("Other", "movie", otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	body := refreshBody(t, library)
	for _, caller := range []struct {
		token string
		code  int
	}{{"", 401}, {f.member, 403}, {f.admin, 403}} {
		if w := f.call("POST", "/v1/integrations/refresh", caller.token, body); w.Code != caller.code {
			t.Fatalf("auth %d: %d %s", caller.code, w.Code, w.Body)
		}
	}
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"refresh-read","name":"Reader","scopes":["read","playback"]}`)
	if w := f.call("POST", "/v1/integrations/refresh", read.Secret, body); w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
		t.Fatalf("read-only key: %d %s", w.Code, w.Body)
	}
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"refresh-library","name":"Library","scopes":["library"]}`)
	if w := f.call("POST", "/v1/integrations/refresh", key.Secret, body); w.Code != 202 {
		t.Fatalf("library key: %d %s", w.Code, w.Body)
	}
	if got := queuedJobs(t, f, library); got != 1 {
		t.Fatalf("named library jobs: %d", got)
	}
	if got := queuedJobs(t, f, otherLibrary.ID); got != 0 {
		t.Fatalf("a refresh of one library queued another: %d", got)
	}
	if w := f.call("POST", "/v1/integrations/refresh", key.Secret, refreshBody(t, "lib_absent")); w.Code != 404 {
		t.Fatalf("unknown library: %d %s", w.Code, w.Body)
	}
}

func TestCompatibilityLibraryRefreshDelegatesToTheNativeRoute(t *testing.T) {
	f, library := refreshFixture(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"refresh-compat","name":"Compat","scopes":["library"]}`)
	if _, err := f.deps.DB.Exec(`INSERT INTO server_settings(singleton,"integrations.compatibilityEnabled") VALUES(1,1) ON CONFLICT(singleton) DO UPDATE SET "integrations.compatibilityEnabled"=1`); err != nil {
		t.Fatal(err)
	}
	w := compatibilityCall(f, "POST", "/Library/Refresh", key.Secret, "{}")
	if w.Code != 200 || strings.Contains(w.Body.String(), "untargeted refresh is unavailable") {
		t.Fatalf("compatibility refresh still refuses: %d %s", w.Code, w.Body)
	}
	var out ingestion.PathReport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].ID == "" || len(out.Paths) != 0 {
		t.Fatalf("compatibility refresh did not return the native job: %s", w.Body)
	}
	var owner string
	if err := f.deps.DB.QueryRow(`SELECT library_id FROM jobs WHERE id=?`, out.Jobs[0].ID).Scan(&owner); err != nil {
		t.Fatalf("compatibility refresh reported a job that does not exist: %v", err)
	}
	if owner != library {
		t.Fatalf("compatibility job belongs to %q, expected %q", owner, library)
	}
	// Repeated no-path refreshes are accepted and visible, never a refusal.
	w = compatibilityCall(f, "POST", "/Library/Refresh", key.Secret, "{}")
	if w.Code != 200 {
		t.Fatalf("repeated compatibility refresh: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Deferred || len(out.Jobs) != 0 {
		t.Fatalf("a busy source must answer deferred: %s", w.Body)
	}
}

func TestIntegrationRefreshAcceptsAnExistingLibraryWithoutEnablingSources(t *testing.T) {
	f, library := refreshFixture(t)
	if _, err := f.deps.DB.Exec(`UPDATE library_sources SET enabled=0 WHERE library_id=?`, library); err != nil {
		t.Fatal(err)
	}
	w := f.call("POST", "/v1/integrations/refresh", f.owner, refreshBody(t, library))
	if w.Code != 202 {
		t.Fatalf("disabled-source library refresh: %d %s", w.Code, w.Body)
	}
	var out ingestion.PathReport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Deferred || len(out.Jobs) != 0 || len(out.Paths) != 0 {
		t.Fatalf("invented work for a disabled source: %s", w.Body)
	}
	var enabled, pending int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM library_sources WHERE library_id=? AND enabled=1`, library).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM inventory_strict_intents WHERE library_id=?`, library).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || pending != 0 || queuedJobs(t, f, library) != 0 {
		t.Fatal("refresh changed disabled-source custody")
	}
}
