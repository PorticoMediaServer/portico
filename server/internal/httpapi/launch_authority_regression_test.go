package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func recoveryRequest(f administrationFixture, peer, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = peer
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func mustRefreshBody(t *testing.T, w *httptest.ResponseRecorder) []byte {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("refresh replay: %d %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestLaunchAdminLibraryVisibilityAndManagement(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	a := c.Library("allowed", "Allowed", "movie", "/allowed")
	b := c.Library("future", "Future", "movie", "/future")
	item := c.Movie(b, "/future/film.mp4", "Future Film", 2001)
	c.Movie(a, "/allowed/film.mp4", "Allowed Film", 2000)
	c.Drain()
	if _, err := f.deps.DB.Exec(`UPDATE direct_memberships SET allowed_libraries='["allowed"]' WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []struct {
		token string
		count int
	}{{f.owner, 2}, {f.admin, 2}, {f.member, 1}} {
		w := f.call("GET", "/v1/libraries", actor.token, "")
		if w.Code != 200 {
			t.Fatalf("libraries: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Items) != actor.count {
			t.Fatalf("library count want %d: %s %v", actor.count, w.Body.String(), err)
		}
	}
	for _, path := range []string{"/v1/admin/libraries", "/v1/admin/libraries/allowed", "/v1/admin/library-settings", "/v1/admin/libraries/future/settings", "/v1/items/" + item.Public + "/detail", "/v1/items/" + item.Public + "/metadata/manual"} {
		if w := f.call("GET", path, f.admin, ""); w.Code != 200 {
			t.Fatalf("admin %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := f.call("GET", "/v1/items/"+item.Public+"/detail", f.member, ""); w.Code != 404 {
		t.Fatalf("member read ungranted item: %d", w.Code)
	}
	for _, path := range []string{"/v1/admin/libraries", "/v1/admin/library-settings", "/v1/items/" + item.Public + "/metadata/manual"} {
		if w := f.call("GET", path, f.member, ""); w.Code != 403 {
			t.Fatalf("member administration %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := f.call("GET", "/v1/items/"+item.Public+"/metadata/manual", f.admin, "")
	var manual catalog.ManualMetadata
	if err := json.Unmarshal(w.Body.Bytes(), &manual); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"expectedRevision": manual.Revision, "title": "Administrator edit"})
	if w = f.call("PATCH", "/v1/items/"+item.Public+"/metadata/manual", f.admin, string(body)); w.Code != 200 {
		t.Fatalf("admin metadata write: %d %s", w.Code, w.Body.String())
	}
	if w = f.call("PUT", "/v1/admin/libraries/order", f.admin, `{"libraryIds":["future","allowed"]}`); w.Code != 200 {
		t.Fatalf("admin library write: %d %s", w.Code, w.Body.String())
	}
	var settings administration.LibraryDocument
	w = f.call("GET", "/v1/admin/libraries/future/settings", f.admin, "")
	if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(administration.Change[administration.LibrarySettings]{ExpectedRevision: settings.Revision, OperationID: "launch-admin-settings", Settings: settings.Settings})
	if w = f.call("PUT", "/v1/admin/libraries/future/settings", f.admin, string(raw)); w.Code != 200 {
		t.Fatalf("admin library settings: %d %s", w.Code, w.Body.String())
	}
	// These operations still require the security owner, even with library access.
	for _, path := range []string{"/v1/admin/maintenance/settings", "/v1/admin/connectivity/policy", "/v1/setup/state", "/v1/admin/backups", "/v1/admin/metadata/settings"} {
		if w = f.call("GET", path, f.admin, ""); w.Code != 403 {
			t.Fatalf("admin owner-only %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestLaunchLibraryAuthorityRechecksLiveTierInTransaction(t *testing.T) {
	for _, change := range []string{`UPDATE direct_memberships SET role='member' WHERE account_id='admin'`, `UPDATE direct_memberships SET disabled=1 WHERE account_id='admin'`, `UPDATE authorization_session_families SET revoked=1 WHERE account_id='admin'`} {
		t.Run(change, func(t *testing.T) {
			f := administrationFixtureNew(t)
			p, err := f.deps.Identity.Authenticate(f.admin)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.deps.DB.Exec(change); err != nil {
				t.Fatal(err)
			}
			tx, err := f.deps.DB.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = f.deps.libraryAdministrationAuthorize(context.Background(), tx, p, ""); !errors.Is(err, identity.ErrUnauthorized) && !errors.Is(err, identity.ErrForbidden) {
				t.Fatalf("stale admin library authority accepted: %v", err)
			}
			if err = f.deps.manualMetadataAuthorize(context.Background(), tx, p); !errors.Is(err, identity.ErrUnauthorized) && !errors.Is(err, identity.ErrForbidden) {
				t.Fatalf("stale admin metadata authority accepted: %v", err)
			}
		})
	}
	// A viewing session of an administrative account never borrows its tier.
	f := administrationFixtureNew(t)
	narrow, err := f.deps.Identity.Issue("admin", "admin-profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.call("GET", "/v1/admin/libraries", narrow.AccessToken, ""); w.Code != 403 {
		t.Fatalf("narrow admin session: %d %s", w.Code, w.Body.String())
	}
}

func TestLaunchAdminVisibilityRetainsProfileAndRecordingPrivacy(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	a := c.Library("shared", "Shared", "movie", "/shared-root")
	b := c.Library("recordings", "Private Recording Library", "movie", "/private-recording-root")
	shared := c.Movie(a, "/shared-root/film.mp4", "Shared film", 2000)
	recording := c.Movie(b, "/private-recording-root/secret.mp4", "Private recording", 2001)
	c.Drain()
	if _, err := f.deps.DB.Exec(`INSERT INTO dvr_private_libraries VALUES('recordings','local','member','member-profile');
 INSERT INTO jobs(id,library_id,status,error,created_at) VALUES('zz-private-scan','recordings','failed','private scanner detail','2026-10-18'),('shared-scan','shared','failed','','2026-10-17')`); err != nil {
		t.Fatal(err)
	}
	f.deps.Ingestion = ingestion.New(f.deps.DB, f.deps.Catalog, assets.Probe{})
	f.handler = New(f.deps)
	lists := []string{"/v1/admin/libraries?limit=1", "/v1/admin/jobs?limit=1", "/v1/admin/library-settings?limit=1"}
	privateMarkers := []string{"recordings", "Private Recording Library", "private-recording-root", "zz-private-scan", "private scanner detail", "secret.mp4"}
	for _, path := range lists {
		w := f.call("GET", path, f.admin, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "shared") {
			t.Fatalf("admin allowed listing %s: %d %s", path, w.Code, w.Body.String())
		}
		for _, marker := range privateMarkers {
			if strings.Contains(w.Body.String(), marker) {
				t.Fatalf("private admin listing %s disclosed %q: %s", path, marker, w.Body.String())
			}
		}
		// The owner's existing configuration view is intentional and is retained.
		w = f.call("GET", strings.ReplaceAll(path, "?limit=1", ""), f.owner, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "recordings") {
			t.Fatalf("owner configuration view changed %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	libraryPaths := func(id, job string) []string {
		return []string{"/v1/admin/libraries/" + id, "/v1/admin/libraries/" + id + "/settings", "/v1/admin/libraries/" + id + "/inventory", "/v1/admin/libraries/" + id + "/inventory-config", "/v1/admin/jobs?libraryId=" + id, "/v1/admin/ingestion/jobs/" + job}
	}
	for _, path := range libraryPaths("shared", "shared-scan") {
		if w := f.call("GET", path, f.admin, ""); w.Code != 200 {
			t.Fatalf("admin allowed resource %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range libraryPaths("recordings", "zz-private-scan") {
		w := f.call("GET", path, f.admin, "")
		if w.Code != 404 {
			t.Fatalf("private admin resource %s: %d %s", path, w.Code, w.Body.String())
		}
		for _, marker := range privateMarkers {
			if strings.Contains(w.Body.String(), marker) {
				t.Fatalf("private refusal disclosed %q: %s", marker, w.Body.String())
			}
		}
	}
	for _, token := range []string{f.owner, f.admin} {
		for _, suffix := range []string{"/detail", "/metadata/manual"} {
			if w := f.call("GET", "/v1/items/"+recording.Public+suffix, token, ""); w.Code != 404 {
				t.Fatalf("administrator read another profile's recording %s: %d %s", suffix, w.Code, w.Body.String())
			}
		}
		if w := f.call("GET", "/v1/libraries", token, ""); w.Code != 200 || strings.Contains(w.Body.String(), `"id":"recordings"`) {
			t.Fatalf("private viewer library disclosed: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := f.deps.DB.Exec(`UPDATE direct_profiles SET allowed_libraries='[]',revision=revision+1 WHERE id='admin-profile'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range lists {
		w := f.call("GET", path, f.admin, "")
		if w.Code != 200 {
			t.Fatalf("narrowed listing %s: %d %s", path, w.Code, w.Body.String())
		}
		for _, marker := range append(privateMarkers, "shared", "Shared film", "shared-root", "shared-scan") {
			if strings.Contains(w.Body.String(), marker) {
				t.Fatalf("narrowed admin listing %s disclosed %q: %s", path, marker, w.Body.String())
			}
		}
	}
	for _, path := range append(libraryPaths("shared", "shared-scan"), "/v1/items/"+shared.Public+"/detail", "/v1/items/"+shared.Public+"/metadata/manual") {
		if w := f.call("GET", path, f.admin, ""); w.Code != 404 {
			t.Fatalf("narrowed admin resource %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, q := range []struct{ method, path, body string }{
		{"PUT", "/v1/admin/libraries/shared/settings", `{"expectedRevision":1,"operationId":"denied-admin-settings","settings":{}}`},
		{"POST", "/v1/admin/libraries/shared/sources", `{"expectedRevision":1,"name":"Denied","path":"/denied"}`},
		{"DELETE", "/v1/admin/ingestion/jobs/shared-scan", ``},
		{"PUT", "/v1/admin/libraries/order", `{"libraryIds":["shared"]}`},
	} {
		if w := f.call(q.method, q.path, f.admin, q.body); w.Code != 404 {
			t.Fatalf("narrowed admin mutation %s: %d %s", q.path, w.Code, w.Body.String())
		}
	}
}
