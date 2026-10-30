package httpapi

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestViewerRestrictionCacheUsesAuthorityQualifiedProfile(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	if _, err := d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('profile',13,0)`); err != nil {
		t.Fatal(err)
	}
	d.restrictions = newRestrictionCache()
	local, err := d.Identity.Authenticate(owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/bootstrap", nil)
	policy, _, err := d.viewerRestrictions(r, local)
	if err != nil || policy.MaximumAge == nil || *policy.MaximumAge != 13 {
		t.Fatalf("local profile restrictions: %+v %v", policy, err)
	}
	hosted := identity.Principal{Viewer: identity.Viewer{Authority: "hosted", AccountID: "other-account", ProfileID: "profile", Role: "member"}}
	policy, _, err = d.viewerRestrictions(r, hosted)
	if !errors.Is(err, identity.ErrUnauthorized) || policy.Active() {
		t.Fatalf("retired authority borrowed the local cache entry: %+v %v", policy, err)
	}
}
