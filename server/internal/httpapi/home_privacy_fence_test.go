package httpapi

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// H-11: clients rebuild their whole shell on the privacy fence and only refresh on the
// viewer fence. A library added or removed is not a privacy change; who the viewer is and
// what they may see are.
func TestHomePrivacyFenceIgnoresTheLibraryListButFollowsEveryPrivacyInput(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	c.Library("movies", "Movies", "movie", "/movies")
	owner := identity.Principal{Viewer: identity.Viewer{AccountID: "owner", ProfileID: "owner-profile", Authority: "local", Role: "owner"}, Epoch: 1}
	member := identity.Principal{Viewer: identity.Viewer{AccountID: "member", ProfileID: "member-profile", Authority: "local", Role: "member"}, Epoch: 1}
	read := func(p identity.Principal) ([]string, string, string) {
		t.Helper()
		ids, fence, privacy, err := f.deps.homeScopeFences(httptest.NewRequest("GET", "/v1/home", nil), p)
		if err != nil {
			t.Fatal(err)
		}
		if fence == "" || privacy == "" || fence == privacy {
			t.Fatalf("fences must be present and distinct: %q %q", fence, privacy)
		}
		return ids, fence, privacy
	}
	_, baseFence, basePrivacy := read(owner)

	c.Library("music", "Music", "music", "/music")
	ids, added, addedPrivacy := read(owner)
	if !slices.Contains(ids, "music") {
		t.Fatalf("the new library is in scope: %v", ids)
	}
	if added == baseFence || addedPrivacy != basePrivacy {
		t.Fatalf("adding a library changes the viewer fence only: fence %v privacy %v", added != baseFence, addedPrivacy == basePrivacy)
	}
	// Removing a library retires it first (catalog.RemoveLibrary); a retired library leaves scope.
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id=?`, "music")
	if _, removed, removedPrivacy := read(owner); removed != baseFence || removedPrivacy != basePrivacy {
		t.Fatalf("removing it returns both fences to before: fence %v privacy %v", removed == baseFence, removedPrivacy == basePrivacy)
	}

	changed := func(name string, p identity.Principal, baseFence, basePrivacy string) {
		t.Helper()
		if _, fence, privacy := read(p); fence == baseFence || privacy == basePrivacy {
			t.Fatalf("%s must change both fences: fence %v privacy %v", name, fence != baseFence, privacy != basePrivacy)
		}
	}
	// Another real viewer on the same server.
	admin := identity.Principal{Viewer: identity.Viewer{AccountID: "admin", ProfileID: "admin-profile", Authority: "local", Role: "admin"}, Epoch: 1}
	changed("another viewer", admin, baseFence, basePrivacy)
	// A security event bumps the account epoch (a password change, a device revoked).
	c.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id='owner'`)
	epoch := owner
	epoch.Epoch++
	changed("the account epoch", epoch, baseFence, basePrivacy)

	_, memberFence, memberPrivacy := read(member)

	// The content fence: a new limit on what the member may see.
	current, err := f.deps.Access.Access.Limits(context.Background(), nil, "member")
	if err != nil {
		t.Fatal(err)
	}
	limits := access.DefaultLimits()
	limits.MaxContentRating = "PG"
	if _, err := f.deps.Access.Access.SetLimits(context.Background(), nil, "member", access.LimitsChange{ExpectedRevision: current.Revision, OperationID: "privacy-fence-limit", Limits: limits}); err != nil {
		t.Fatal(err)
	}
	changed("a content limit", member, memberFence, memberPrivacy)
}
