package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

type searchPrivacyFixture struct {
	*tl11AdmissionFixture
	kids identity.Envelope
}

func newSearchPrivacyFixture(t *testing.T) *searchPrivacyFixture {
	t.Helper()
	f := newTL11AdmissionFixture(t, "movie")
	ctx := context.Background()
	snapshot, err := f.d.Identity.CreateDirectProfile(ctx, f.token, "Kids", "mint")
	if err != nil {
		t.Fatal(err)
	}
	var kid string
	for _, profile := range snapshot.Profiles {
		if profile.Name == "Kids" {
			kid = profile.ID
		}
	}
	parent, err := f.d.Identity.Authenticate(f.token)
	if err != nil || kid == "" {
		t.Fatal("profile setup failed", err)
	}
	if _, err = f.db.Exec(`UPDATE direct_profiles SET allowed_libraries='["movies"]',revision=revision+1 WHERE id=?`, kid); err != nil {
		t.Fatal(err)
	}
	kids, err := f.d.Identity.Issue(parent.AccountID, kid, "local", "member", parent.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	f.d.LiveChannels, err = livechannels.New(f.db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.d.LiveChannels.Save(ctx, f.d.liveAuthority(parent), livechannels.SourceInput{
		ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "News", TunerCount: 1,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"news\",Northwind News\nhttps://provider.invalid/news\n#EXTINF:-1 tvg-id=\"news24\",Northwind News 24\nhttps://provider.invalid/news24\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	movies := f.c.Handle("movies")
	for _, title := range []string{"Northwind Film", "Northwind Family"} {
		film := f.c.Movie(movies, "/movies/"+title+".mkv", title, 2026)
		f.c.Attributes(film.ID, "contentRating", "G")
	}
	adult := f.c.Movie(movies, "/movies/adult.mkv", "Northwind Adult", 2026)
	f.c.Attributes(adult.ID, "contentRating", "R")
	private := f.c.Library("private", "Private", "movie", "/private")
	privateFilm := f.c.Movie(private, "/private/film.mkv", "Northwind Private", 2026)
	f.c.Attributes(privateFilm.ID, "contentRating", "G")
	f.c.Drain()
	if _, err = f.d.Catalog.ClassifyPendingRatings(ctx); err != nil {
		t.Fatal(err)
	}
	f.c.Drain()
	f.h = New(f.d)
	return &searchPrivacyFixture{tl11AdmissionFixture: f, kids: kids}
}

func (f *searchPrivacyFixture) restrictLiveTV(allow bool) {
	f.t.Helper()
	ctx := context.Background()
	current, err := f.d.Identity.ProfileRestrictions(ctx, f.token, f.kids.Viewer.ProfileID)
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = f.d.Identity.SetProfileRestrictions(ctx, f.token, f.kids.Viewer.ProfileID, identity.ProfileRestrictionEdit{
		ExpectedRevision: current.Revision, RatingSystem: "mpaa", MaximumAgeRating: "PG", AllowUnrated: false,
		AllowLiveTV: allow, AllowDownloads: true, AllowDVR: false, AllowWatchTogether: true,
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func readSearchPrivacyResponse(t *testing.T, h http.Handler, path, token string) catalog.SearchEnvelope {
	t.Helper()
	w := logoutHTTPRequest(h, "GET", path, token)
	if w.Code != http.StatusOK {
		t.Fatalf("search status %d: %s", w.Code, w.Body.String())
	}
	var out catalog.SearchEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertHTTPWithholdsLiveTV(t *testing.T, out catalog.SearchEnvelope) {
	t.Helper()
	for _, group := range out.Groups {
		if group.ID == "live-tv" || group.EntityKind == "channel" {
			t.Errorf("denied Live TV group: %+v", group)
		}
	}
	for _, id := range out.Query.Groups {
		if id == "live-tv" {
			t.Error("denied Live TV query group")
		}
	}
	for _, capability := range out.Capabilities.Groups {
		if capability.ID == "live-tv" {
			t.Errorf("denied Live TV capability: %+v", capability)
		}
	}
}

func TestSearchLiveTVPrivacyHTTPAllMixedAndExplicit(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	f.restrictLiveTV(false)
	for _, suffix := range []string{"", "&groups=movies,live-tv", "&groups=live-tv", "&group=movies"} {
		out := readSearchPrivacyResponse(t, f.h, "/v1/search?q=Northwind"+suffix, f.kids.AccessToken)
		assertHTTPWithholdsLiveTV(t, out)
		if suffix != "&groups=live-tv" && (len(out.Groups) == 0 || len(out.Groups[0].Items) != 2 || out.Groups[0].TotalCount != 2) {
			t.Errorf("permitted nonlive search lost: %+v", out.Groups)
		}
		for _, group := range out.Groups {
			for _, entry := range group.Items {
				if strings.Contains(entry.Title, "Adult") || strings.Contains(entry.Title, "Private") {
					t.Error("content or library restriction bypassed")
				}
			}
		}
	}
	for _, suffix := range []string{"&group=live-tv", "&group=live-tv&cursor=old", "&liveTV=true", "&allowLiveTv=true"} {
		w := logoutHTTPRequest(f.h, "GET", "/v1/search?q=Northwind"+suffix, f.kids.AccessToken)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "Northwind News") {
			t.Errorf("explicit/client authority: %d %s", w.Code, w.Body.String())
		}
	}
	if w := logoutHTTPRequest(f.h, "GET", "/v1/search?q=Northwind&libraryIds=private", f.kids.AccessToken); w.Code != http.StatusBadRequest {
		t.Errorf("library selector widened authority: %d", w.Code)
	}
	for _, suffix := range []string{"", "&group=live-tv", "&groups=movies,live-tv"} {
		if w := logoutHTTPRequest(f.h, "GET", "/v1/search?q=Northwind"+suffix, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous search: %d", w.Code)
		}
	}
	if w := logoutHTTPRequest(f.h, "GET", "/v1/guide", f.kids.AccessToken); w.Code != http.StatusForbidden {
		t.Errorf("guide denial changed: %d", w.Code)
	}
	// The same configured server still searches its active lineup for an allowed profile.
	out := readSearchPrivacyResponse(t, f.h, "/v1/search?q=Northwind&group=live-tv", f.token)
	if len(out.Groups) != 1 || out.Groups[0].TotalCount != 2 || !out.Groups[0].TotalCountExact || len(out.Groups[0].Items) != 2 {
		t.Fatalf("allowed profile lost channels: %+v", out.Groups)
	}
	liveAvailable := false
	for _, capability := range out.Capabilities.Groups {
		if capability.ID == "live-tv" {
			liveAvailable = capability.Available
		}
	}
	if !liveAvailable {
		t.Error("allowed profile lost Live TV capability")
	}
}

func TestSearchLiveTVPrivacyHTTPRevocationExpiresCursors(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	f.restrictLiveTV(true)
	paths := []string{"/v1/search?q=Northwind&group=live-tv&limit=1", "/v1/search?q=Northwind&group=movies&limit=1"}
	cursors := make([]string, len(paths))
	for i, path := range paths {
		out := readSearchPrivacyResponse(t, f.h, path, f.kids.AccessToken)
		cursors[i] = out.Groups[0].NextCursor
		if cursors[i] == "" {
			t.Fatal("fixture did not produce a continuation")
		}
		readSearchPrivacyResponse(t, f.h, path+"&cursor="+url.QueryEscape(cursors[i]), f.kids.AccessToken)
	}
	f.restrictLiveTV(false)
	for i, path := range paths {
		w := logoutHTTPRequest(f.h, "GET", path+"&cursor="+url.QueryEscape(cursors[i]), f.kids.AccessToken)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusConflict {
			t.Errorf("revoked continuation: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Northwind News") {
			t.Error("revoked cursor disclosed a channel")
		}
	}
	out := readSearchPrivacyResponse(t, f.h, "/v1/search?q=Northwind", f.kids.AccessToken)
	assertHTTPWithholdsLiveTV(t, out)
}

func TestSearchLiveTVPrivacyHTTPCurrentPolicyAndFailure(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	f.restrictLiveTV(true)
	before := readSearchPrivacyResponse(t, f.h, "/v1/search?q=Northwind", f.kids.AccessToken)
	// Deliberately bypass publication/cache invalidation to prove the feature
	// decision reads the current restriction, rather than a principal or content cache.
	if _, err := f.db.Exec(`UPDATE profile_restrictions SET allow_live_tv=0 WHERE profile_id=?`, f.kids.Viewer.ProfileID); err != nil {
		t.Fatal(err)
	}
	out := readSearchPrivacyResponse(t, f.h, "/v1/search?q=Northwind", f.kids.AccessToken)
	assertHTTPWithholdsLiveTV(t, out)
	if out.Scope.ViewerFence == before.Scope.ViewerFence {
		t.Error("current feature denial did not change the response fence")
	}
	// Corrupt policy is an authority failure, never an unrestricted default.
	if _, err := f.db.Exec(`UPDATE profile_restrictions SET blocked_labels='{"invalid":true}' WHERE profile_id=?`, f.kids.Viewer.ProfileID); err != nil {
		t.Fatal(err)
	}
	w := logoutHTTPRequest(f.h, "GET", "/v1/search?q=Northwind", f.kids.AccessToken)
	if w.Code < 400 || strings.Contains(w.Body.String(), "Northwind News") {
		t.Errorf("policy resolution failed open: %d %s", w.Code, w.Body.String())
	}
}
