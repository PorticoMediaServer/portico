package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// These checks are also the four global query routes' restriction inventory
// entries. Catalogue-id probes alone cannot exercise a real private handle.
func TestFacetQueryRestriction(t *testing.T) { facetQueryRestrictionCheck(t) }
func TestGenreQueryRestriction(t *testing.T) { genreQueryRestrictionCheck(t) }

func facetQueryRestrictionCheck(t *testing.T) { queryHandleRestrictionCheck(t, false) }
func genreQueryRestrictionCheck(t *testing.T) { queryHandleRestrictionCheck(t, true) }

func TestQueryHandleRestrictionInventory(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	inventory := restrictionRouteInventory(t, registry, nil)
	for _, prefix := range []string{"/v1/facet-queries/{id}", "/v1/genre-queries/{id}"} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			pattern := method + " " + prefix
			if _, ok := inventory[pattern]; !ok {
				t.Fatalf("private query route not registered: %s", pattern)
			}
			if method == http.MethodGet {
				route, ok := restrictionRoutes()[pattern]
				if !ok || route.check == nil || route.reason != "" {
					t.Fatalf("%s needs its real-handle restriction check", pattern)
				}
			} else {
				route, ok := restrictionWriteRoutes()[pattern]
				if !ok || route.check == nil || route.reason != "" {
					t.Fatalf("%s needs its real-handle cancellation check", pattern)
				}
			}
		}
	}
}

func queryHandleRestrictionCheck(t *testing.T, genre bool) {
	t.Helper()
	f := newTL11AdmissionFixture(t, "movie")
	f.c.Attributes(f.item.ID, "contentRating", "R")
	f.c.Genres(f.item.ID, "fixture", "Private genre")
	f.c.Drain()
	// The real shared Store starts its workers, but BASE Run stays unstarted:
	// both real runtimes must defer until their source is ready. Cleanup closes
	// the Store (and joins its workers) before the fixture closes the database.
	composeHTTPFacetFixture(t, &f.d)
	f.d.Access = AccessArea{Access: access.New(f.db)}
	f.h = New(f.d)
	ctx := context.Background()
	owner, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.d.Identity.Issue(owner.AccountID, owner.ProfileID, owner.Authority, owner.Role, owner.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	otherPrincipal, err := f.d.Identity.Authenticate(other.AccessToken)
	if err != nil || otherPrincipal.AccountID != owner.AccountID || otherPrincipal.ProfileID != owner.ProfileID {
		t.Fatalf("second family is not the same account/profile: %+v %v", otherPrincipal, err)
	}
	profiles, err := f.d.Identity.CreateDirectProfile(ctx, f.token, "Restricted profile", "mint")
	if err != nil {
		t.Fatal(err)
	}
	profileID := ""
	for _, p := range profiles.Profiles {
		if p.Name == "Restricted profile" {
			profileID = p.ID
		}
	}
	if profileID == "" || profileID == owner.ProfileID {
		t.Fatal("missing independent profile")
	}
	member, err := f.d.Identity.CreateDirectMember(ctx, f.token, identity.DirectMemberCreate{
		Username: "query-member", Name: "Query member", Password: "long-member-password", AllowedLibraries: []string{"movies"},
	})
	if err != nil {
		t.Fatal(err)
	}
	setRestrictions := func(account, profile, rating string) {
		t.Helper()
		if account == owner.AccountID {
			policy, err := f.d.Identity.ProfileRestrictions(ctx, f.token, profile)
			if err != nil {
				t.Fatalf("read own profile restrictions: %v", err)
			}
			if _, err = f.d.Identity.SetProfileRestrictions(ctx, f.token, profile, identity.ProfileRestrictionEdit{
				ExpectedRevision: policy.Revision, RatingSystem: "mpaa", MaximumAgeRating: rating, AllowUnrated: false,
			}); err != nil {
				t.Fatalf("set own profile restrictions: %v", err)
			}
			return
		}
		// People owns cross-account policy edits. The direct-profile service is
		// intentionally scoped to the caller's account and would return 404 here.
		ownerJSON := func(method, path string, body any) *httptest.ResponseRecorder {
			t.Helper()
			raw := ""
			if body != nil {
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				raw = string(encoded)
			}
			r := httptest.NewRequest(method, path, strings.NewReader(raw))
			r.Header.Set("Authorization", "Bearer "+f.token)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("owner policy %s %s: %d %s", method, path, w.Code, w.Body)
			}
			return w
		}
		path := "/v1/admin/access/members/" + account + "/profiles"
		w := ownerJSON(http.MethodGet, path, nil)
		var profiles identity.MemberProfiles
		if err := json.Unmarshal(w.Body.Bytes(), &profiles); err != nil {
			t.Fatal(err)
		}
		var revision int64
		for _, p := range profiles.Items {
			if p.ID == profile {
				revision = p.Restrictions.Revision
			}
		}
		if revision < 1 {
			t.Fatal("missing managed member profile", profile)
		}
		ownerJSON(http.MethodPut, path+"/"+profile+"/restrictions", identity.ProfileRestrictionEdit{
			ExpectedRevision: revision, RatingSystem: "mpaa", MaximumAgeRating: rating, AllowUnrated: false,
		})
	}
	issueRestricted := func(account, profile string) string {
		t.Helper()
		setRestrictions(account, profile, "PG")
		envelope, err := f.d.Identity.Issue(account, profile, "local", "member", owner.Epoch)
		if err != nil {
			t.Fatal(err)
		}
		return envelope.AccessToken
	}
	profileToken := issueRestricted(owner.AccountID, profileID)
	memberToken := issueRestricted(member.ID, member.PrimaryProfileID)
	call := func(method, path, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{profileToken, memberToken} {
		if w := call(http.MethodGet, "/v1/items/"+f.item.Public, token); w.Code != http.StatusNotFound {
			t.Fatalf("fixture title is not hidden from restricted viewer: %d %s", w.Code, w.Body)
		}
	}
	if w := call(http.MethodGet, "/v1/items/"+f.item.Public, f.token); w.Code != http.StatusOK {
		t.Fatalf("fixture title is not visible to owner: %d %s", w.Code, w.Body)
	}

	initial, prefix, kind := "/v1/libraries/movies/facets?field=genre&limit=7", "/v1/facet-queries/", "facet-preparing"
	otherInitial, otherPrefix, otherKind := "/v1/search/genres?limit=7", "/v1/genre-queries/", "genre-preparing"
	if genre {
		initial, otherInitial = otherInitial, initial
		prefix, otherPrefix = otherPrefix, prefix
		kind, otherKind = otherKind, kind
	}
	pending := func(w *httptest.ResponseRecorder, prefix, kind, id string) string {
		t.Helper()
		var p struct {
			Kind         string `json:"kind"`
			State        string `json:"state"`
			QueryID      string `json:"queryId"`
			RetryAfterMs int    `json:"retryAfterMs"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != http.StatusAccepted || p.Kind != kind || p.State != "preparing" || p.QueryID == "" || id != "" && p.QueryID != id || p.RetryAfterMs < 1 {
			t.Fatalf("expected real stable preparation %q: %d %s (%v)", id, w.Code, w.Body, err)
		}
		if w.Header().Get("Location") != prefix+p.QueryID || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Retry-After") == "" {
			t.Fatalf("private preparation headers: %v", w.Header())
		}
		return p.QueryID
	}
	id := pending(call(http.MethodGet, initial, f.token), prefix, kind, "")
	privateIDs := []string{id}
	path := prefix + id
	ownerPending := func() {
		t.Helper()
		pending(call(http.MethodGet, path, f.token), prefix, kind, id)
	}
	stale := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusConflict || errorCode(t, w) != "stale_continuation" {
			t.Fatalf("expected typed stale: %d %s", w.Code, w.Body)
		}
		for _, secret := range append([]string{f.item.Public, "Film", "Private genre", `"values"`, `"genres"`, `"preparation"`}, privateIDs...) {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("refusal disclosed private query/catalogue data %q: %s", secret, w.Body)
			}
		}
		if w.Header().Get("Location") != "" {
			t.Fatal("refusal disclosed private poll location")
		}
	}
	deleted := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 || w.Header().Get("Location") != "" {
			t.Fatalf("expected opaque idempotent cancellation: %d %v %s", w.Code, w.Header(), w.Body)
		}
	}
	for name, token := range map[string]string{"same-profile-family": other.AccessToken, "restricted-profile": profileToken, "restricted-account": memberToken} {
		t.Logf("foreign query caller: %s", name)
		stale(call(http.MethodGet, path, token))
		for range 2 {
			deleted(call(http.MethodDelete, path, token))
			ownerPending() // A foreign DELETE must not retire the owner's lease.
		}
	}
	// Hidden catalogue ids and absent ids have the same query-route answer.
	for _, token := range []string{profileToken, memberToken} {
		for _, queryID := range []string{f.item.Public, "absent_query_handle"} {
			stale(call(http.MethodGet, prefix+queryID, token))
			deleted(call(http.MethodDelete, prefix+queryID, token))
		}
	}
	ownerPending()

	// Both directions use real handles sharing one Store. Neither namespace's
	// GET or DELETE may consume the other runtime's handle, even for its owner.
	otherID := pending(call(http.MethodGet, otherInitial, f.token), otherPrefix, otherKind, "")
	privateIDs = append(privateIDs, otherID)
	stale(call(http.MethodGet, otherPrefix+id, f.token))
	deleted(call(http.MethodDelete, otherPrefix+id, f.token))
	ownerPending()
	stale(call(http.MethodGet, prefix+otherID, f.token))
	deleted(call(http.MethodDelete, prefix+otherID, f.token))
	pending(call(http.MethodGet, otherPrefix+otherID, f.token), otherPrefix, otherKind, otherID)
	deleted(call(http.MethodDelete, otherPrefix+otherID, f.token))
	stale(call(http.MethodGet, otherPrefix+otherID, f.token))

	loggedOut, err := f.d.Identity.Issue(owner.AccountID, owner.ProfileID, owner.Authority, owner.Role, owner.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.d.Identity.LogoutToken(ctx, loggedOut.AccessToken); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", loggedOut.AccessToken} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			w := call(method, path, token)
			if w.Code != http.StatusUnauthorized || errorCode(t, w) != "unauthorized" || strings.Contains(w.Body.String(), id) {
				t.Fatalf("logged-out query access: %d %s", w.Code, w.Body)
			}
		}
	}
	ownerPending()

	// Change live restrictions while a genuine query is still preparing. Poll
	// must revalidate authority before returning even a preparing descriptor.
	restrictedID := pending(call(http.MethodGet, initial, memberToken), prefix, kind, "")
	privateIDs = append(privateIDs, restrictedID)
	setRestrictions(member.ID, member.PrimaryProfileID, "G")
	stale(call(http.MethodGet, prefix+restrictedID, memberToken))
	ownerPending()
	for range 2 {
		deleted(call(http.MethodDelete, path, f.token))
		stale(call(http.MethodGet, path, f.token))
	}
}
