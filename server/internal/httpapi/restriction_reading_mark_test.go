package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Ineligible actors fail before any target lookup, profile-key read or receipt
// access. A nil transaction/dependencies deliberately make accidental access fail.
func TestReadingMarkRequiresNormalSelectedProfile(t *testing.T) {
	normal := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile", Role: "member"}}
	for _, role := range []string{"owner", "admin", "member"} {
		p := normal
		p.Role = role
		if !identity.CanUsePersonalState(p) {
			t.Fatalf("normal profile %s is ineligible", role)
		}
	}
	cases := map[string]identity.Principal{}
	for _, role := range []string{"guest", "account", "", "other"} {
		p := normal
		p.Role = role
		cases["role-"+role] = p
	}
	for _, grant := range []string{identity.GrantCastTarget, "other-restricted-grant"} {
		p := normal
		p.Grant = grant
		cases[grant] = p
	}
	p := normal
	p.Authority = ""
	cases["no-authority"] = p
	p = normal
	p.AccountID = ""
	cases["no-account"] = p
	p = normal
	p.ProfileID = ""
	cases["no-profile"] = p
	d := Dependencies{}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if identity.CanUsePersonalState(p) {
				t.Fatal("ineligible profile accepted")
			}
			var fence string
			if err := d.readingMarkAuthorize(context.Background(), p, "hidden-item", &fence)(nil); !errors.Is(err, identity.ErrForbidden) || fence != "" {
				t.Fatal("target authorization did not refuse before data access", err)
			}
			if _, err := d.readingMarkView(context.Background(), p, "hidden-item"); !errors.Is(err, identity.ErrForbidden) {
				t.Fatal("personal read did not refuse before profile data access", err)
			}
		})
	}
}

// Both registry entries retain non-vacuous real-target reads/writes, then
// compare concealed title/library refusals with a truly absent target.
func readingMarkRestrictionCheck(t *testing.T) {
	for _, format := range []ReadingFormat{"pdf", "cbz"} {
		t.Run(string(format), func(t *testing.T) {
			kind := "book"
			if format == "cbz" {
				kind = "comic"
			}
			r := newReadingFixture(t, kind, string(format))
			h := New(r.d)
			ctx := context.Background()
			owner := readingOwnerLogin(t, r)
			profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Mark reader", "blue")
			if err != nil {
				t.Fatal(err)
			}
			var child identity.DirectProfile
			for _, p := range profiles.Profiles {
				if !p.Primary && p.Name == "Mark reader" {
					child = p
				}
			}
			if child.ID == "" {
				t.Fatal("no real child profile")
			}
			selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
			if err != nil {
				t.Fatal(err)
			}
			token := selected.Session.AccessToken
			path := func(id string) string { return "/v1/items/" + id + "/reading/mark" }
			get := readingHTTP(t, h, "GET", path(r.item), token, "", nil)
			var initial ReadingMarkResponse
			if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &initial) != nil || initial.ItemID != r.item || initial.ServerID != r.f.id.ID() || initial.ViewerFence == "" || initial.Read {
				t.Fatal("real initial mark", get.Code, get.Body.String())
			}
			opened := readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{r.item, r.source})
			var manifest ReadingSessionResponse
			if opened.Code != 200 || json.Unmarshal(opened.Body.Bytes(), &manifest) != nil || manifest.SessionID == "" {
				t.Fatal("actual Reading session", opened.Code, opened.Body.String())
			}
			page := int64(0)
			saved := readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+manifest.SessionID+"/progress", token, manifest.Revision, ReadingProgressRequest{Location: ReadingLocation{Format: format, PageIndex: &page, Progression: .25}})
			if saved.Code != 200 {
				t.Fatal("actual saved location", saved.Code, saved.Body.String())
			}
			before := readingRestrictionProgress(t, r)
			if before == "null" || before == "[]" {
				t.Fatal("saved-location control was vacuous")
			}
			yes := true
			body := ReadingMarkRequest{OperationID: "matrix-mark-read", ExpectedRevision: initial.Revision, Read: &yes}
			put := readingHTTP(t, h, "PUT", path(r.item), token, "", body)
			var marked ReadingMarkResponse
			if put.Code != 200 || json.Unmarshal(put.Body.Bytes(), &marked) != nil || !marked.Read || marked.ItemID != r.item || marked.OperationID != body.OperationID || marked.Revision <= initial.Revision {
				t.Fatal("real explicit read mark", put.Code, put.Body.String())
			}
			replay := readingHTTP(t, h, "PUT", path(r.item), token, "", body)
			var replayed ReadingMarkResponse
			if replay.Code != 200 || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.Revision != marked.Revision || !replayed.Read {
				t.Fatal("immutable operation replay", replay.Code, replay.Body.String())
			}
			no := false
			later := readingHTTP(t, h, "PUT", path(r.item), token, "", ReadingMarkRequest{OperationID: "matrix-mark-unread", ExpectedRevision: marked.Revision, Read: &no})
			var unmarked ReadingMarkResponse
			if later.Code != 200 || json.Unmarshal(later.Body.Bytes(), &unmarked) != nil || unmarked.Read || unmarked.Revision <= marked.Revision {
				t.Fatal("later explicit unread", later.Code, later.Body.String())
			}
			old := readingHTTP(t, h, "PUT", path(r.item), token, "", body)
			var oldReceipt ReadingMarkResponse
			if old.Code != 200 || json.Unmarshal(old.Body.Bytes(), &oldReceipt) != nil || !oldReceipt.Read || oldReceipt.Revision != marked.Revision || oldReceipt.OperationID != body.OperationID {
				t.Fatal("old receipt changed after later intent", old.Code, old.Body.String())
			}
			latest := readingHTTP(t, h, "GET", path(r.item), token, "", nil)
			var currentMark ReadingMarkResponse
			if latest.Code != 200 || json.Unmarshal(latest.Body.Bytes(), &currentMark) != nil || currentMark.Read || currentMark.Revision != unmarked.Revision {
				t.Fatal("replay replaced later explicit choice", latest.Code, latest.Body.String())
			}
			if readingRestrictionProgress(t, r) != before {
				t.Fatal("explicit mark changed saved Reading location")
			}
			other := readingHTTP(t, h, "GET", path(r.item), r.f.owner.AccessToken, "", nil)
			var otherMark ReadingMarkResponse
			if other.Code != 200 || json.Unmarshal(other.Body.Bytes(), &otherMark) != nil || otherMark.Read {
				t.Fatal("mark escaped its exact profile", other.Code, other.Body.String())
			}
			for _, method := range []string{"GET", "PUT"} {
				if w := readingHTTP(t, h, method, path(r.item), "", "", body); w.Code != 401 {
					t.Fatal("anonymous mark", w.Code, w.Body.String())
				}
			}
			refuse := func() {
				t.Helper()
				for _, method := range []string{"GET", "PUT"} {
					hidden := readingHTTP(t, h, method, path(r.item), token, "", body)
					absent := readingHTTP(t, h, method, path(absentTwin(r.item)), token, "", body)
					if hidden.Code != 404 || absent.Code != 404 || normalizedAnswer(hidden.Body.String(), r.item) != normalizedAnswer(absent.Body.String(), absentTwin(r.item)) {
						t.Fatal("hidden/absent mark differs", method, hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
					}
					for _, marker := range []string{r.item, r.source, r.library, r.path} {
						if marker != "" && strings.Contains(hidden.Body.String(), marker) {
							t.Fatal("hidden mark leaked target", hidden.Body.String())
						}
					}
				}
				if readingRestrictionProgress(t, r) != before {
					t.Fatal("refused mark changed saved Reading location")
				}
			}
			if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
				t.Fatal(err)
			}
			refuse()
			if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = r.f.id.EditDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
				t.Fatal(err)
			}
			refuse()
		})
	}
}
