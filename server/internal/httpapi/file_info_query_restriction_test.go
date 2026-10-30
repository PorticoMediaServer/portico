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

// The FileInfo query route is one private handle namespace shared by the ready
// lanes and opening preparation. These checks are its restriction inventory
// entries and use the real fixture, identity families, profiles, runtime and
// store: a catalogue-id probe cannot mint or replay a genuine handle.
func TestFileInfoQueryRestriction(t *testing.T) { fileInfoQueryRestrictionCheck(t) }

func fileInfoQueryRestrictionCheck(t *testing.T) {
	t.Helper()
	f := newFileInfoWindowsFixture(t)
	f.d.Access = AccessArea{Access: access.New(f.base.db)}
	f.h = New(f.d)
	public := f.base.item.Public
	f.base.c.Attributes(f.base.item.ID, "contentRating", "R")
	f.base.c.Drain()
	// The accepted opening names the pre-edit item head. Re-mint it against the
	// hidden item's current head so the owner's real window stays genuine.
	f.mint(t)

	ctx := context.Background()
	owner, err := f.d.Identity.Authenticate(f.base.token)
	if err != nil {
		t.Fatal(err)
	}
	// A second family for the same account and profile is a different private
	// caller even though it presents the same person.
	other, err := f.d.Identity.Issue(owner.AccountID, owner.ProfileID, owner.Authority, owner.Role, owner.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	otherPrincipal, err := f.d.Identity.Authenticate(other.AccessToken)
	if err != nil || other.AccessToken == f.base.token || otherPrincipal.AccountID != owner.AccountID || otherPrincipal.ProfileID != owner.ProfileID {
		t.Fatalf("second family is not the same account/profile: %+v %v", otherPrincipal, err)
	}
	profiles, err := f.d.Identity.CreateDirectProfile(ctx, f.base.token, "Restricted FileInfo profile", "mint")
	if err != nil {
		t.Fatal(err)
	}
	profileID := ""
	for _, profile := range profiles.Profiles {
		if !profile.Primary && profile.Name == "Restricted FileInfo profile" {
			profileID = profile.ID
		}
	}
	if profileID == "" || profileID == owner.ProfileID {
		t.Fatal("missing independent profile")
	}
	profileToken, err := f.d.Identity.Issue(owner.AccountID, profileID, "local", "member", owner.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	member, err := f.d.Identity.CreateDirectMember(ctx, f.base.token, identity.DirectMemberCreate{
		Username: "file-info-member", Name: "FileInfo member", Password: "long-member-password", AllowedLibraries: []string{"movies"},
	})
	if err != nil {
		t.Fatal(err)
	}
	memberToken, err := f.d.Identity.Issue(member.ID, member.PrimaryProfileID, "local", "member", owner.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	setRestrictions := func(actor, profile, rating string) {
		t.Helper()
		policy, err := f.d.Identity.ProfileRestrictions(ctx, actor, profile)
		if err != nil {
			t.Fatalf("read profile restrictions: %v", err)
		}
		if _, err = f.d.Identity.SetProfileRestrictions(ctx, actor, profile, identity.ProfileRestrictionEdit{
			ExpectedRevision: policy.Revision, RatingSystem: "mpaa", MaximumAgeRating: rating, AllowUnrated: false,
		}); err != nil {
			t.Fatalf("set profile restrictions: %v", err)
		}
	}
	setRestrictions(f.base.token, profileID, "PG")
	// People owns managed-member policy edits; a member cannot widen its own
	// restrictions through the direct-profile owner service.
	managedPath := "/v1/admin/access/members/" + member.ID + "/profiles"
	managed := editorRequest(t, f.h, http.MethodGet, managedPath, f.base.token, "", nil)
	if managed.Code != http.StatusOK {
		t.Fatal("managed profiles", managed.Code, managed.Body.String())
	}
	var memberProfiles identity.MemberProfiles
	if err = json.Unmarshal(managed.Body.Bytes(), &memberProfiles); err != nil {
		t.Fatal(err)
	}
	var managedRevision int64
	for _, p := range memberProfiles.Items {
		if p.ID == member.PrimaryProfileID {
			managedRevision = p.Restrictions.Revision
		}
	}
	if managedRevision < 1 {
		t.Fatal("missing managed profile revision")
	}
	edit, _ := json.Marshal(identity.ProfileRestrictionEdit{ExpectedRevision: managedRevision, RatingSystem: "mpaa", MaximumAgeRating: "PG", AllowUnrated: false})
	managed = editorRequest(t, f.h, http.MethodPut, managedPath+"/"+member.PrimaryProfileID+"/restrictions", f.base.token, "application/json", edit)
	if managed.Code != http.StatusOK {
		t.Fatal("managed restrictions", managed.Code, managed.Body.String())
	}

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

	// The same opening the owner already holds is a real ready window. Its
	// private page names the exact file and the handle that polls it.
	files := f.lane(t, fileInfoLanePath(public, "/file-info", f.opening), nil)
	if files.Code != http.StatusOK {
		t.Fatalf("owner window %d %s", files.Code, files.Body.String())
	}
	page := fileInfoBody[struct {
		Kind   string `json:"kind"`
		Window struct {
			QueryID string `json:"queryId"`
		} `json:"window"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}](t, files)
	if page.Kind != "file-info-files" || !validFileInfoQueryID(page.Window.QueryID) || len(page.Files) != 1 || page.Files[0].Path != f.path {
		t.Fatalf("owner window lost its private content: %s", files.Body.String())
	}
	queryID := page.Window.QueryID
	path := "/v1/file-info-queries/" + queryID
	secrets := []string{queryID, public, f.path, "Film", `"file-info-files"`, `"fileInfoOpening"`}
	private := func(w *httptest.ResponseRecorder) {
		t.Helper()
		for _, secret := range secrets {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("refusal disclosed private FileInfo data %q: %s", secret, w.Body.String())
			}
		}
		if w.Header().Get("Location") != "" {
			t.Fatal("refusal disclosed a private poll location")
		}
	}
	stale := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusConflict || errorCode(t, w) != "stale_continuation" {
			t.Fatalf("expected typed stale: %d %s", w.Code, w.Body.String())
		}
		private(w)
	}
	deleted := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 || w.Header().Get("Location") != "" {
			t.Fatalf("expected opaque idempotent cancellation: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
	ownerReady := func() {
		t.Helper()
		w := call(http.MethodGet, path, f.base.token)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"file-info-files"`) || !strings.Contains(w.Body.String(), f.path) {
			t.Fatalf("owner lost the handle: %d %s", w.Code, w.Body.String())
		}
	}

	// A restricted viewer cannot open the title at all, so the checks below are
	// not merely a different-family refusal on a title they could otherwise read.
	for name, token := range map[string]string{"restricted-profile": profileToken.AccessToken, "restricted-account": memberToken.AccessToken} {
		t.Logf("restricted FileInfo caller: %s", name)
		if w := call(http.MethodGet, "/v1/items/"+public, token); w.Code != http.StatusNotFound {
			t.Fatalf("%s can still see the restricted title: %d %s", name, w.Code, w.Body.String())
		}
		// A path naming a hidden item is concealed before even a real foreign
		// opening is parsed; the opaque query endpoint stays uniformly stale.
		if w := call(http.MethodGet, fileInfoLanePath(public, "/file-info", f.opening), token); w.Code != http.StatusNotFound {
			t.Fatalf("hidden opening was not concealed: %d %s", w.Code, w.Body.String())
		} else {
			private(w)
		}
		stale(call(http.MethodGet, path, token))
		for range 2 {
			deleted(call(http.MethodDelete, path, token))
			ownerReady()
		}
	}

	// A same-account/profile foreign family is private from the handle too.
	stale(call(http.MethodGet, path, other.AccessToken))
	for range 2 {
		deleted(call(http.MethodDelete, path, other.AccessToken))
		ownerReady()
	}

	// Hidden catalogue ids never become a readable handle. The hidden id and an
	// absent handle answer identically, and neither answer echoes the id.
	for _, token := range []string{f.base.token, profileToken.AccessToken, memberToken.AccessToken, other.AccessToken} {
		for _, candidate := range []string{public, "absent_query_handle"} {
			hidden := call(http.MethodGet, "/v1/file-info-queries/"+candidate, token)
			absent := call(http.MethodGet, "/v1/file-info-queries/absent_query_handle", token)
			if hidden.Code != http.StatusBadRequest || hidden.Code != absent.Code || errorCode(t, hidden) != errorCode(t, absent) {
				t.Fatalf("hidden/absent handle differ: %d %s / %d %s", hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
			}
			private(hidden)
			cancelled := call(http.MethodDelete, "/v1/file-info-queries/"+candidate, token)
			cancelledAbsent := call(http.MethodDelete, "/v1/file-info-queries/absent_query_handle", token)
			if cancelled.Code != http.StatusBadRequest || cancelled.Code != cancelledAbsent.Code || errorCode(t, cancelled) != errorCode(t, cancelledAbsent) {
				t.Fatalf("hidden/absent cancel differ: %d %s / %d %s", cancelled.Code, cancelled.Body.String(), cancelledAbsent.Code, cancelledAbsent.Body.String())
			}
			private(cancelled)
		}
	}
	ownerReady()

	// A malformed lane request must not disclose a hidden catalogue identity
	// by failing input validation before its live visibility admission.
	for _, token := range []string{profileToken.AccessToken, memberToken.AccessToken} {
		for _, suffix := range []string{"/file-info", "/file-info?unexpected=1", "/file-info?limit=0", "/file-info?opening=x&opening=y", "/file-info/groups", "/file-info/groups/members?group=matrix", "/file-info/groups/head-tracks?group=matrix&lane=audio", "/file-info/tracks?occurrence=matrix&lane=audio", "/file-info/sources", "/file-info/disc-titles?occurrence=matrix"} {
			hidden := call(http.MethodGet, "/v1/items/"+public+suffix, token)
			absent := call(http.MethodGet, "/v1/items/"+strings.Repeat("A", 22)+suffix, token)
			if hidden.Code != http.StatusNotFound || absent.Code != hidden.Code || errorCode(t, hidden) != errorCode(t, absent) {
				t.Fatalf("hidden malformed lane differs from absent: %s hidden%d/%s absent%d/%s", suffix, hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
			}
		}
	}

	// Owner cancellation is private and idempotent; the retired handle is stale.
	for range 2 {
		deleted(call(http.MethodDelete, path, f.base.token))
	}
	stale(call(http.MethodGet, path, f.base.token))

	// Revoking the live policy of an own real opening invalidates its pending
	// poll before it can answer 200 or even 202, and retires the handle.
	revokedProfiles, err := f.d.Identity.CreateDirectProfile(ctx, f.base.token, "Revoked FileInfo profile", "blue")
	if err != nil {
		t.Fatal(err)
	}
	revokedProfileID := ""
	for _, profile := range revokedProfiles.Profiles {
		if !profile.Primary && profile.Name == "Revoked FileInfo profile" {
			revokedProfileID = profile.ID
		}
	}
	if revokedProfileID == "" {
		t.Fatal("missing revocation profile")
	}
	revokedToken, err := f.d.Identity.Issue(owner.AccountID, revokedProfileID, "local", "member", owner.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	f.base.c.Exec(`UPDATE catalog_entities SET info_source_blockers=1 WHERE id=?`, f.base.item.ID)
	defer func() {
		f.base.c.Exec(`UPDATE catalog_entities SET info_source_blockers=0 WHERE id=?`, f.base.item.ID)
		f.base.c.Drain()
	}()
	f.base.c.Drain()
	detail := call(http.MethodGet, "/v1/items/"+public+"/detail?related=none", revokedToken.AccessToken)
	if detail.Code != http.StatusAccepted {
		t.Fatalf("own opening did not prepare: %d %s", detail.Code, detail.Body.String())
	}
	openingPath := detail.Header().Get("Location")
	if !strings.HasPrefix(openingPath, "/v1/file-info-queries/open.") || detail.Header().Get("Retry-After") == "" {
		t.Fatalf("own opening preparation headers: %v", detail.Header())
	}
	if pending := call(http.MethodGet, openingPath, revokedToken.AccessToken); pending.Code != http.StatusAccepted {
		t.Fatalf("own opening poll before revocation: %d %s", pending.Code, pending.Body.String())
	}
	setRestrictions(f.base.token, revokedProfileID, "PG")
	stale(call(http.MethodGet, openingPath, revokedToken.AccessToken))
	if w := call(http.MethodGet, "/v1/items/"+public, revokedToken.AccessToken); w.Code != http.StatusNotFound {
		t.Fatalf("revoked profile still sees the title: %d %s", w.Code, w.Body.String())
	}

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
			if w.Code != http.StatusUnauthorized || errorCode(t, w) != "unauthorized" || strings.Contains(w.Body.String(), queryID) {
				t.Fatalf("logged-out FileInfo query access: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if w := call(http.MethodDelete, "/v1/file-info-queries/not-a-handle", f.base.token); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed cancel: %d %s", w.Code, w.Body.String())
	}
}
