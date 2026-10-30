package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/contract"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func completedLocalFoundationFixture(t *testing.T) (Dependencies, identity.Envelope) {
	t.Helper()
	state := t.TempDir()
	db, e := persistence.Open(filepath.Join(state, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	id, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	secret, e := id.BrowserSetupToken(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	setup, e := id.SetupWithOptions(context.Background(), identity.SetupOptions{RequestID: "77777777-7777-4777-8777-777777777777", SetupToken: secret, AuthMode: "local", Username: "owner", Password: "Local-password1!", Name: "Fixture"}, true)
	if e != nil {
		t.Fatal(e)
	}
	control, e := hosted.New(db, id, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	return Dependencies{DB: db, Identity: id, Hosted: control, Catalog: catalog.New(db), Origins: []string{"http://127.0.0.1:11111"}}, setup.Envelope
}

func TestFoundationContractAndReplay(t *testing.T) {
	d, viewer := completedLocalFoundationFixture(t)
	settleCompactCatalogue(t, d.DB)
	h := New(d)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/server", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var response ServerDocument
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != d.Identity.ID() || response.API.Major != 1 {
		t.Fatal("identity or version mismatch")
	}
	authCapabilities := httptest.NewRecorder()
	h.ServeHTTP(authCapabilities, httptest.NewRequest("GET", "/v1/auth/capabilities", nil))
	if authCapabilities.Code != 200 {
		t.Fatalf("sign-in capabilities: %d %s", authCapabilities.Code, authCapabilities.Body.String())
	}
	var signInMethods struct {
		ServerID string   `json:"serverId"`
		Methods  []string `json:"methods"`
	}
	if err := json.Unmarshal(authCapabilities.Body.Bytes(), &signInMethods); err != nil || signInMethods.ServerID != d.Identity.ID() || len(signInMethods.Methods) == 0 {
		t.Fatalf("sign-in capabilities document: %+v %v", signInMethods, err)
	}
	authenticated := httptest.NewRequest("GET", "/v1/capabilities", nil)
	authenticated.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	capabilities := httptest.NewRecorder()
	h.ServeHTTP(capabilities, authenticated)
	if capabilities.Code != 200 {
		t.Fatalf("viewer capabilities: %d %s", capabilities.Code, capabilities.Body.String())
	}
	ratingsRequest := httptest.NewRequest("GET", "/v1/rating-systems", nil)
	ratingsRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	ratings := httptest.NewRecorder()
	h.ServeHTTP(ratings, ratingsRequest)
	if ratings.Code != 200 {
		t.Fatalf("viewer rating systems: %d %s", ratings.Code, ratings.Body.String())
	}
	assertSpecResponse(t, "GET", "/v1/rating-systems", ratings)
	// The list is the build's: the validator it names is answered 304 again.
	again := httptest.NewRequest("GET", "/v1/rating-systems", nil)
	again.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	again.Header.Set("If-None-Match", ratings.Header().Get("ETag"))
	held := httptest.NewRecorder()
	h.ServeHTTP(held, again)
	if ratings.Header().Get("ETag") == "" || held.Code != 304 || held.Body.Len() != 0 {
		t.Fatalf("a held rating list: %d %q %s", held.Code, ratings.Header().Get("ETag"), held.Body.String())
	}
	restrictionsRequest := httptest.NewRequest("GET", "/v1/direct/profiles/"+viewer.Viewer.ProfileID+"/restrictions", nil)
	restrictionsRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	restrictions := httptest.NewRecorder()
	h.ServeHTTP(restrictions, restrictionsRequest)
	if restrictions.Code != 200 {
		t.Fatalf("owner restrictions: %d %s", restrictions.Code, restrictions.Body.String())
	}
	pinRecoveryRequest := httptest.NewRequest("GET", "/v1/direct/pin-recovery", nil)
	pinRecoveryRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	pinRecovery := httptest.NewRecorder()
	h.ServeHTTP(pinRecovery, pinRecoveryRequest)
	if pinRecovery.Code != 200 {
		t.Fatalf("owner PIN recovery methods: %d %s", pinRecovery.Code, pinRecovery.Body.String())
	}
	avatarsRequest := httptest.NewRequest("GET", "/v1/direct/profiles/avatars", nil)
	avatarsRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	avatars := httptest.NewRecorder()
	h.ServeHTTP(avatars, avatarsRequest)
	if avatars.Code != 200 || !strings.Contains(avatars.Body.String(), `"items":[]`) {
		t.Fatalf("profile avatar list: %d %s", avatars.Code, avatars.Body.String())
	}
	registrationRequest := httptest.NewRequest("GET", "/v1/direct/registration-policy", nil)
	registrationRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	registration := httptest.NewRecorder()
	h.ServeHTTP(registration, registrationRequest)
	if registration.Code != 200 {
		t.Fatalf("owner registration policy: %d %s", registration.Code, registration.Body.String())
	}
	directRequest := httptest.NewRequest("GET", "/v1/direct", nil)
	directRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	direct := httptest.NewRecorder()
	h.ServeHTTP(direct, directRequest)
	if direct.Code != 200 {
		t.Fatalf("owner direct account: %d %s", direct.Code, direct.Body.String())
	}
	devicesRequest := httptest.NewRequest("GET", "/v1/devices", nil)
	devicesRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	devices := httptest.NewRecorder()
	h.ServeHTTP(devices, devicesRequest)
	if devices.Code != 200 {
		t.Fatalf("owner devices: %d %s", devices.Code, devices.Body.String())
	}
	sessionsRequest := httptest.NewRequest("GET", "/v1/direct/sessions", nil)
	sessionsRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	sessions := httptest.NewRecorder()
	h.ServeHTTP(sessions, sessionsRequest)
	if sessions.Code != 200 {
		t.Fatalf("owner session list: %d %s", sessions.Code, sessions.Body.String())
	}
	var sessionDocument DirectSessionsDocument
	if err := json.Unmarshal(sessions.Body.Bytes(), &sessionDocument); err != nil || len(sessionDocument.Items) == 0 {
		t.Fatalf("session document: %+v %v", sessionDocument, err)
	}
	var ratingDocument RatingSystemsDocument
	if err := json.Unmarshal(ratings.Body.Bytes(), &ratingDocument); err != nil || len(ratingDocument.Items) == 0 {
		t.Fatalf("rating systems document: %+v %v", ratingDocument, err)
	}
	bootstrapRequest := httptest.NewRequest("GET", "/v1/bootstrap", nil)
	bootstrapRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	bootstrap := httptest.NewRecorder()
	h.ServeHTTP(bootstrap, bootstrapRequest)
	if bootstrap.Code != 200 {
		t.Fatalf("viewer bootstrap: %d %s", bootstrap.Code, bootstrap.Body.String())
	}
	var startup BootstrapDocument
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &startup); err != nil || startup.Server.ID != d.Identity.ID() || startup.Me.Viewer.ProfileID != viewer.Viewer.ProfileID || startup.Preferences.DeviceClass != "web" || startup.HomeLayout.Revision < 1 {
		t.Fatalf("bootstrap document: %+v %v", startup, err)
	}
	conditionalBootstrap := httptest.NewRequest("GET", "/v1/bootstrap", nil)
	conditionalBootstrap.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	conditionalBootstrap.Header.Set("If-None-Match", bootstrap.Header().Get("ETag"))
	conditionalBootstrapResponse := httptest.NewRecorder()
	h.ServeHTTP(conditionalBootstrapResponse, conditionalBootstrap)
	if conditionalBootstrapResponse.Code != 304 {
		t.Fatalf("conditional bootstrap: %d", conditionalBootstrapResponse.Code)
	}
	invalidClass := httptest.NewRequest("GET", "/v1/bootstrap?deviceClass=watch", nil)
	invalidClass.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	invalidClassResponse := httptest.NewRecorder()
	h.ServeHTTP(invalidClassResponse, invalidClass)
	if invalidClassResponse.Code != 400 {
		t.Fatalf("invalid device class: %d", invalidClassResponse.Code)
	}
	var document CapabilitiesDocument
	if err := json.Unmarshal(capabilities.Body.Bytes(), &document); err != nil || document.API.Level != 1 || document.Features["feedback"] != "enabled" || document.Limits["selectorIdsMax"] != 500 || document.Limits["pageSizeMax"] != catalog.BrowseMaximumLimit {
		t.Fatalf("capability document: %+v %v", document, err)
	}
	anonymous := httptest.NewRecorder()
	h.ServeHTTP(anonymous, httptest.NewRequest("GET", "/v1/capabilities", nil))
	if anonymous.Code != 401 {
		t.Fatalf("anonymous viewer capabilities: %d", anonymous.Code)
	}
	if anonymousRatings := logoutHTTPRequest(h, "GET", "/v1/rating-systems", ""); anonymousRatings.Code != 401 {
		t.Fatalf("anonymous rating systems: %d", anonymousRatings.Code)
	}
	anonymousBootstrap := httptest.NewRecorder()
	h.ServeHTTP(anonymousBootstrap, httptest.NewRequest("GET", "/v1/bootstrap", nil))
	if anonymousBootstrap.Code != 401 {
		t.Fatalf("anonymous bootstrap: %d", anonymousBootstrap.Code)
	}
	eventsRequest := httptest.NewRequest("GET", "/v1/events?waitMs=0", nil)
	eventsRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	events := httptest.NewRecorder()
	h.ServeHTTP(events, eventsRequest)
	if events.Code != 200 {
		t.Fatalf("viewer events: %d %s", events.Code, events.Body.String())
	}
	updatesRequest := httptest.NewRequest("GET", "/v1/admin/updates", nil)
	updatesRequest.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	updates := httptest.NewRecorder()
	h.ServeHTTP(updates, updatesRequest)
	if updates.Code != 200 {
		t.Fatalf("owner updates: %d %s", updates.Code, updates.Body.String())
	}
	var updateDocument struct {
		State string `json:"state"`
	}
	// The default release feed is configured, but this fixture has no fetcher.
	if err := json.Unmarshal(updates.Body.Bytes(), &updateDocument); err != nil || updateDocument.State != "unavailable" {
		t.Fatalf("owner updates document: %+v %v", updateDocument, err)
	}
	conditionalUpdates := httptest.NewRequest("GET", "/v1/admin/updates", nil)
	conditionalUpdates.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	conditionalUpdates.Header.Set("If-None-Match", updates.Header().Get("ETag"))
	unchangedUpdates := httptest.NewRecorder()
	h.ServeHTTP(unchangedUpdates, conditionalUpdates)
	if unchangedUpdates.Code != 304 {
		t.Fatalf("unchanged update check: %d %s", unchangedUpdates.Code, unchangedUpdates.Body.String())
	}
	path := "../../testdata/contracts/get-server.json"
	if os.Getenv("PORTICO_CONTRACT_UPDATE") == "1" {
		if err := contract.Record(path, "GetServerResponse", "GET", "/v1/server", nil, w); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-auth-capabilities.json", "GetAuthCapabilitiesResponse", "GET", "/v1/auth/capabilities", nil, authCapabilities); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-capabilities.json", "GetCapabilitiesResponse", "GET", "/v1/capabilities", nil, capabilities, "viewer"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-events.json", "GetEventsResponse", "GET", "/v1/events?waitMs=0", nil, events, "viewer"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-rating-systems.json", "GetRatingSystemsResponse", "GET", "/v1/rating-systems", nil, ratings, "viewer"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-direct-profile-restrictions.json", "GetDirectProfileRestrictionsResponse", "GET", "/v1/direct/profiles/"+viewer.Viewer.ProfileID+"/restrictions", nil, restrictions, "owner"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-direct-pin-recovery.json", "GetDirectPinRecoveryResponse", "GET", "/v1/direct/pin-recovery", nil, pinRecovery, "owner"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-direct-profile-avatars.json", "GetDirectProfileAvatarsResponse", "GET", "/v1/direct/profiles/avatars", nil, avatars, "viewer"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-direct-registration-policy.json", "GetDirectRegistrationPolicyResponse", "GET", "/v1/direct/registration-policy", nil, registration, "owner"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-devices.json", "GetDevicesResponse", "GET", "/v1/devices", nil, devices, "viewer"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-direct-sessions.json", "GetDirectSessionsResponse", "GET", "/v1/direct/sessions", nil, sessions, "owner"); err != nil {
			t.Fatal(err)
		}
		if err := contract.Record("../../testdata/contracts/get-admin-updates.json", "GetAdminUpdatesResponse", "GET", "/v1/admin/updates", nil, updates, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if value := os.Getenv("PORTICO_CONTRACT_UPDATE"); value == "1" || value == "get-bootstrap" {
		if err := contract.Record("../../testdata/contracts/get-bootstrap.json", "GetBootstrapResponse", "GET", "/v1/bootstrap", nil, bootstrap, "viewer"); err != nil {
			t.Fatal(err)
		}
	}
	if value := os.Getenv("PORTICO_CONTRACT_UPDATE"); value == "1" || value == "get-direct-account" {
		if err := contract.Record("../../testdata/contracts/get-direct-account.json", "GetDirectAccountResponse", "GET", "/v1/direct", nil, direct, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	fixtures, err := filepath.Glob("../../testdata/contracts/*.json")
	if err != nil || len(fixtures) == 0 {
		t.Fatal("no fixtures", err)
	}
	// The restrictions PUT retires the owner family. Replay it after the
	// remaining owner-authenticated fixtures.
	sort.SliceStable(fixtures, func(i, j int) bool {
		last := "put-direct-profile-restrictions.json"
		if filepath.Base(fixtures[i]) == last {
			return false
		}
		if filepath.Base(fixtures[j]) == last {
			return true
		}
		return fixtures[i] < fixtures[j]
	})
	for _, path := range fixtures {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fixture contract.Fixture
		if err = json.Unmarshal(raw, &fixture); err != nil {
			t.Fatal(err)
		}
		if fixture.Auth != "" && fixture.Auth != "viewer" {
			continue // Recorded and replayed by its own route family's test (e.g. playback v1, auth "device").
		}
		// Invitation preview needs its single-use code; PIN reset revokes the
		// current family. Both have separate HTTP authority and response tests.
		if fixture.Schema == "PreviewInvitationResponse" || fixture.Schema == "PostDirectProfilePinResetResponse" {
			continue
		}
		replay := httptest.NewRecorder()
		replayed := httptest.NewRequest(fixture.Method, fixture.Path, bytes.NewReader(fixture.Request))
		if fixture.Auth == "viewer" || fixture.Auth == "owner" {
			replayed.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
		}
		if len(fixture.Request) > 0 {
			replayed.Header.Set("Content-Type", "application/json")
		}
		h.ServeHTTP(replay, replayed)
		if replay.Code != fixture.Status {
			t.Fatalf("%s replay: %d", path, replay.Code)
		}
	}
	q := httptest.NewRequest("GET", "/v1/server", nil)
	q.Header.Set("If-None-Match", w.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	h.ServeHTTP(conditional, q)
	if conditional.Code != 304 {
		t.Fatal("conditional server read", conditional.Code)
	}
}

func TestCapabilitiesFollowCurrentProfileSwitches(t *testing.T) {
	d, viewer := logoutHTTPFixture(t)
	// Compose Downloads so the profile switch is tested as not_permitted
	// (available) rather than unavailable (not composed). Precedence is
	// unavailable > disabled_by_owner > not_permitted > enabled.
	var err error
	d.Downloads, err = downloads.New(downloads.Options{DB: d.DB})
	if err != nil {
		t.Fatal(err)
	}
	h := New(d)
	read := func() (CapabilitiesDocument, string) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/capabilities", nil)
		r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("capabilities: %d %s", w.Code, w.Body.String())
		}
		var out CapabilitiesDocument
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, w.Header().Get("ETag")
	}
	before, oldTag := read()
	if before.Features["watch_together"] != "enabled" || before.Features["dvr"] != "unavailable" || before.Features["playback_v1"] != "enabled" {
		t.Fatal("unconfigured feature states", before.Features)
	}
	if _, err := d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,allow_downloads,allow_watch_together) VALUES('profile',0,0)`); err != nil {
		t.Fatal(err)
	}
	after, newTag := read()
	if after.Features["downloads"] != "not_permitted" || after.Features["watch_together"] != "not_permitted" || newTag == oldTag {
		t.Fatal("profile switches did not change capabilities and ETag", after.Features)
	}
}
func TestFoundationRouteAccessRules(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range registry.Definitions() {
		if route.Access == "" || route.Cost == "" || route.Lane == "" {
			t.Fatal("incomplete route", route.ID)
		}
	}
}

func TestTypedMaintenanceSettingsRevisionAndAuthority(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	request := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/admin/maintenance/settings", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	read := request("GET", "")
	if read.Code != 200 {
		t.Fatalf("read maintenance: %d %s", read.Code, read.Body.String())
	}
	if os.Getenv("PORTICO_CONTRACT_UPDATE") == "1" {
		if err := contract.Record("../../testdata/contracts/get-admin-maintenance-settings.json", "GetAdminMaintenanceSettingsResponse", "GET", "/v1/admin/maintenance/settings", nil, read, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	var doc administration.MaintenanceDocument
	if err := json.Unmarshal(read.Body.Bytes(), &doc); err != nil || doc.Revision < 1 || len(doc.Cadences) == 0 {
		t.Fatalf("maintenance document: %+v %v", doc, err)
	}
	change := administration.Change[administration.MaintenanceSettingsDocument]{ExpectedRevision: doc.Revision, OperationID: "maintenance-typed-1", Settings: doc.Settings}
	body, err := json.Marshal(change)
	if err != nil {
		t.Fatal(err)
	}
	saved := request("PUT", string(body))
	if saved.Code != 200 {
		t.Fatalf("save maintenance: %d %s", saved.Code, saved.Body.String())
	}
	if os.Getenv("PORTICO_CONTRACT_UPDATE") == "1" {
		if err := contract.Record("../../testdata/contracts/put-admin-maintenance-settings.json", "PutAdminMaintenanceSettingsResponse", "PUT", "/v1/admin/maintenance/settings", body, saved, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if err = json.Unmarshal(saved.Body.Bytes(), &doc); err != nil || doc.Revision <= change.ExpectedRevision {
		t.Fatalf("saved revision: %+v %v", doc, err)
	}
	if replay := request("PUT", string(body)); replay.Code != 200 {
		t.Fatalf("operation replay: %d %s", replay.Code, replay.Body.String())
	}
	change.OperationID = "maintenance-typed-2"
	body, _ = json.Marshal(change)
	if stale := request("PUT", string(body)); stale.Code != 412 {
		t.Fatalf("stale revision: %d %s", stale.Code, stale.Body.String())
	}
	if anonymous := logoutHTTPRequest(h, "GET", "/v1/admin/maintenance/settings", ""); anonymous.Code != 401 {
		t.Fatalf("anonymous maintenance read: %d", anonymous.Code)
	}
}

func TestDirectSessionsRegistryRequiresAccountManage(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/sessions", owner.AccessToken); w.Code != 200 {
		t.Fatalf("owner sessions: %d %s", w.Code, w.Body.String())
	}
	account, err := d.Identity.CreateDirectProfile(context.Background(), owner.AccessToken, "Child", "blue")
	if err != nil {
		t.Fatal(err)
	}
	viewing, err := d.Identity.SelectDirectProfile(context.Background(), owner.AccessToken, account.Profiles[len(account.Profiles)-1].ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/sessions", viewing.Session.AccessToken); w.Code != 403 {
		t.Fatalf("viewing family managed sessions: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "DELETE", "/v1/direct/sessions/"+owner.SessionFamilyID, viewing.Session.AccessToken); w.Code != 403 {
		t.Fatalf("viewing family revoked a session: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/sessions", ""); w.Code != 401 {
		t.Fatalf("anonymous sessions: %d %s", w.Code, w.Body.String())
	}
	other, err := d.Identity.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	if w := logoutHTTPRequest(h, "DELETE", "/v1/direct/sessions/"+other.SessionFamilyID, owner.AccessToken); w.Code != 204 {
		t.Fatalf("owner sign-out: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/sessions", other.AccessToken); w.Code != 401 {
		t.Fatalf("signed-out family retained access: %d %s", w.Code, w.Body.String())
	}
}

func TestTypedProfileRestrictionsAuthorityRevisionAndContract(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	account, err := d.Identity.CreateDirectProfile(context.Background(), owner.AccessToken, "Child", "blue")
	if err != nil {
		t.Fatal(err)
	}
	childID := account.Profiles[len(account.Profiles)-1].ID
	child, err := d.Identity.SelectDirectProfile(context.Background(), owner.AccessToken, childID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/direct/profiles/" + childID + "/restrictions"
	if w := logoutHTTPRequest(h, "GET", path, child.Session.AccessToken); w.Code != 200 {
		t.Fatalf("child own restrictions: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/profiles/profile/restrictions", child.Session.AccessToken); w.Code != 403 {
		t.Fatalf("child read another profile: %d %s", w.Code, w.Body.String())
	}
	put := func(target, token string, edit identity.ProfileRestrictionEdit) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(edit)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("PUT", target, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	edit := identity.ProfileRestrictionEdit{ExpectedRevision: 1, RatingSystem: "mpaa", MaximumAgeRating: "PG-13", AllowUnrated: false, BlockedLabels: []string{}, AllowDownloads: true, AllowLiveTV: true, AllowDVR: true, AllowWatchTogether: true}
	if w := put(path, child.Session.AccessToken, edit); w.Code != 403 {
		t.Fatalf("child changed restrictions: %d %s", w.Code, w.Body.String())
	}
	if w := put(path, owner.AccessToken, edit); w.Code != 200 {
		t.Fatalf("owner changed restrictions without step-up: %d %s", w.Code, w.Body.String())
	}
	if w := put(path, owner.AccessToken, edit); w.Code != 412 {
		t.Fatalf("stale restrictions: %d %s", w.Code, w.Body.String())
	}
	// The change applies live: the child's device stays signed in and reads
	// the new document on its next request.
	if w := logoutHTTPRequest(h, "GET", path, child.Session.AccessToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"maximumAge":13`) {
		t.Fatalf("the child's session after a restriction change: %d %s", w.Code, w.Body.String())
	}
	primary := put("/v1/direct/profiles/profile/restrictions", owner.AccessToken, edit)
	if primary.Code != 200 {
		t.Fatalf("primary restrictions: %d %s", primary.Code, primary.Body.String())
	}
	if os.Getenv("PORTICO_CONTRACT_UPDATE") == "1" {
		body, _ := json.Marshal(edit)
		if err := contract.Record("../../testdata/contracts/put-direct-profile-restrictions.json", "PutDirectProfileRestrictionsResponse", "PUT", "/v1/direct/profiles/profile/restrictions", body, primary, "owner"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTypedPINRecoveryAndResetAuthorityAndContract(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	account, err := d.Identity.CreateDirectProfile(context.Background(), owner.AccessToken, "Child", "blue")
	if err != nil {
		t.Fatal(err)
	}
	childID := account.Profiles[len(account.Profiles)-1].ID
	child, err := d.Identity.SelectDirectProfile(context.Background(), owner.AccessToken, childID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/pin-recovery", child.Session.AccessToken); w.Code != 403 {
		t.Fatalf("child PIN recovery methods: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/profiles/avatars", child.Session.AccessToken); w.Code != 200 {
		t.Fatalf("child profile avatar list: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/profiles/avatars", ""); w.Code != 401 {
		t.Fatalf("anonymous profile avatar list: %d %s", w.Code, w.Body.String())
	}
	reset := func(path, token, pin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"pin":`+pin+`}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/v1/direct/profiles/" + childID + "/pin-reset"
	if w := reset(path, child.Session.AccessToken, `"1234"`); w.Code != 403 {
		t.Fatalf("child PIN reset: %d %s", w.Code, w.Body.String())
	}
	if w := reset(path, owner.AccessToken, `"1234"`); w.Code != 200 {
		t.Fatalf("owner PIN reset without step-up: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/profiles/"+childID+"/restrictions", child.Session.AccessToken); w.Code != 401 {
		t.Fatalf("old child family survived PIN reset: %d %s", w.Code, w.Body.String())
	}
	if w := reset(path, owner.AccessToken, `"not-a-pin"`); w.Code != 400 {
		t.Fatalf("invalid PIN: %d %s", w.Code, w.Body.String())
	}
	primary := reset("/v1/direct/profiles/profile/pin-reset", owner.AccessToken, `""`)
	if primary.Code != 200 {
		t.Fatalf("primary PIN reset: %d %s", primary.Code, primary.Body.String())
	}
	if value := os.Getenv("PORTICO_CONTRACT_UPDATE"); value == "1" || value == "post-direct-profile-pin-reset" {
		if err := contract.Record("../../testdata/contracts/post-direct-profile-pin-reset.json", "PostDirectProfilePinResetResponse", "POST", "/v1/direct/profiles/profile/pin-reset", []byte(`{"pin":""}`), primary, "owner"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTypedRegistrationPolicyIsAtomicAndOwnerOnly(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	account, err := d.Identity.CreateDirectProfile(context.Background(), owner.AccessToken, "Child", "blue")
	if err != nil {
		t.Fatal(err)
	}
	childID := account.Profiles[len(account.Profiles)-1].ID
	child, err := d.Identity.SelectDirectProfile(context.Background(), owner.AccessToken, childID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/direct/registration-policy"
	if w := logoutHTTPRequest(h, "GET", path, child.Session.AccessToken); w.Code != 403 {
		t.Fatalf("child registration policy: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", path, ""); w.Code != 401 {
		t.Fatalf("anonymous registration policy: %d %s", w.Code, w.Body.String())
	}
	put := func(body, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("PUT", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := put(`{"selfRegistration":"open","deviceApproval":"invalid"}`, owner.AccessToken); w.Code != 400 {
		t.Fatalf("invalid policy: %d %s", w.Code, w.Body.String())
	}
	if d.Identity.SelfRegistration() != identity.SelfRegistrationOff {
		t.Fatal("invalid device choice partially opened self-registration")
	}
	if w := put(`{"selfRegistration":"open"}`, child.Session.AccessToken); w.Code != 403 {
		t.Fatalf("child changed policy: %d %s", w.Code, w.Body.String())
	}
	body := `{"selfRegistration":"open"}`
	changed := put(body, owner.AccessToken)
	if changed.Code != 200 || !strings.Contains(changed.Body.String(), `"selfRegistration":"open"`) {
		t.Fatalf("owner changed policy: %d %s", changed.Code, changed.Body.String())
	}
	if d.Identity.SelfRegistration() != identity.SelfRegistrationOpen {
		t.Fatal("atomic policy write did not save both choices")
	}
	if w := logoutHTTPRequest(h, "GET", path, owner.AccessToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"selfRegistration":"open"`) {
		t.Fatalf("read saved policy: %d %s", w.Code, w.Body.String())
	}
	if os.Getenv("PORTICO_CONTRACT_UPDATE") == "1" {
		if err := contract.Record("../../testdata/contracts/put-direct-registration-policy.json", "PutDirectRegistrationPolicyResponse", "PUT", path, []byte(body), changed, "owner"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTypedSignOutEverywhereNeedsNoPassword(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	account, err := d.Identity.CreateDirectProfile(context.Background(), owner.AccessToken, "Child", "blue")
	if err != nil {
		t.Fatal(err)
	}
	childID := account.Profiles[len(account.Profiles)-1].ID
	child, err := d.Identity.SelectDirectProfile(context.Background(), owner.AccessToken, childID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/direct/sessions/sign-out-everywhere"
	if w := logoutHTTPRequest(h, "POST", path, child.Session.AccessToken); w.Code != 403 {
		t.Fatalf("child sign-out-everywhere: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "POST", path, ""); w.Code != 401 {
		t.Fatalf("anonymous sign-out-everywhere: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "DELETE", "/v1/direct/account-session", owner.AccessToken); w.Code != 404 {
		t.Fatalf("retired account-session route: %d %s", w.Code, w.Body.String())
	}
	withBody := httptest.NewRequest("POST", path, strings.NewReader(`{"currentPassword":"unneeded"}`))
	withBody.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	withBody.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, withBody)
	if w.Code != 400 {
		t.Fatalf("unexpected sign-out body: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "POST", path, owner.AccessToken); w.Code != 204 {
		t.Fatalf("owner sign-out-everywhere without step-up: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/sessions", owner.AccessToken); w.Code != 401 {
		t.Fatalf("old owner family survived sign-out: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct/profiles/"+childID+"/restrictions", child.Session.AccessToken); w.Code != 401 {
		t.Fatalf("old child family survived sign-out: %d %s", w.Code, w.Body.String())
	}
}

func TestTypedDirectAccountReadPreservesViewingAuthority(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	account, err := d.Identity.CreateDirectProfile(context.Background(), owner.AccessToken, "Child", "blue")
	if err != nil {
		t.Fatal(err)
	}
	childID := account.Profiles[len(account.Profiles)-1].ID
	child, err := d.Identity.SelectDirectProfile(context.Background(), owner.AccessToken, childID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/direct", ""); w.Code != 401 {
		t.Fatalf("anonymous direct account: %d %s", w.Code, w.Body.String())
	}
	w := logoutHTTPRequest(h, "GET", "/v1/direct", child.Session.AccessToken)
	if w.Code != 200 {
		t.Fatalf("child direct account: %d %s", w.Code, w.Body.String())
	}
	var snapshot identity.DirectSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || snapshot.CanManage || len(snapshot.Profiles) != 2 {
		t.Fatalf("viewing authority widened: %+v, %v", snapshot, err)
	}
	if !strings.Contains(w.Body.String(), `"allowedLibraries":[]`) {
		t.Fatalf("null account libraries: %s", w.Body.String())
	}
}
