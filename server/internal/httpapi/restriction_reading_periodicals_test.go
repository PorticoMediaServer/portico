package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// These are catalogue content reads, not administrator or title-free routes.
// Both registry entries exercise actual admitted magazine/newspaper membership.
func readingPeriodicalRestrictionCheck(t *testing.T) {
	for _, kind := range []string{"magazine", "newspaper"} {
		t.Run(kind, func(t *testing.T) { readingPeriodicalRestrictionFixture(t, kind) })
	}
}

func readingPeriodicalRestrictionFixture(t *testing.T, kind string) {
	t.Helper()
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	dates := []string{"1929-11", "1929-12"}
	if kind == "newspaper" {
		dates = []string{"1929-12-01", "1929-12-02"}
	}
	name := "Private restriction " + kind + " archive"
	paths := []string{
		readingTreeFile(t, r, name+"/Issue "+dates[0]+".epub"),
		readingTreeFile(t, r, name+"/Issue "+dates[1]+".epub"),
	}
	readingScan(t, r.f, r.library)
	items := []string{readingPathItem(t, r, paths[0]), readingPathItem(t, r, paths[1])}
	if items[0] == "" || items[1] == "" || items[0] == items[1] {
		t.Fatal("periodical fixture has no distinct admitted items")
	}
	for _, item := range items {
		readingSeriesOwnerField(t, r, item, "series", name)
		readingSeriesOwnerField(t, r, item, "publicationType", kind)
	}
	readingSeriesDrain(t, r)

	h := New(r.d)
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Periodical reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, profile := range profiles.Profiles {
		if !profile.Primary && profile.Name == "Periodical reader" {
			child = profile
		}
	}
	if child.ID == "" {
		t.Fatal("periodical fixture has no restricted-viewer profile")
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	token := selected.Session.AccessToken
	series := compactcatalog.ReadingSeriesID(r.library, name)
	groupsPath := func(library string) string {
		return "/v1/libraries/" + library + "/reading/periodicals?publicationType=" + kind
	}
	issuesPath := func(library, group string) string {
		return "/v1/libraries/" + library + "/reading/periodicals/" + group + "/issues?publicationType=" + kind
	}
	get := func(path string) *httptest.ResponseRecorder { return readingHTTP(t, h, "GET", path, token, "", nil) }

	// Mandatory non-vacuous controls: real IDs, sources, date precision and a
	// full group count must appear before restriction is changed.
	visibleIssues := get(issuesPath(r.library, series))
	var issuePage ReadingPeriodicalIssuesResponse
	if visibleIssues.Code != 200 || json.Unmarshal(visibleIssues.Body.Bytes(), &issuePage) != nil || len(issuePage.Items) != 2 {
		t.Fatal("visible admitted periodical issues", visibleIssues.Code, visibleIssues.Body.String())
	}
	markers := []string{series, name, paths[0], paths[1], items[0], items[1], dates[0], dates[1]}
	for index, issue := range issuePage.Items {
		want := 1 - index
		if issue.Item.ID != items[want] || issue.Item.SourceID == "" || issue.Item.LibraryID != r.library || string(issue.IssueDate) != dates[want] || issue.Item.Series == nil || issue.Item.Series.ID != series {
			t.Fatal("visible periodical fixture did not reach actual typed dated items", visibleIssues.Body.String())
		}
		markers = append(markers, issue.Item.SourceID)
	}
	visibleGroups := get(groupsPath(r.library))
	var groupPage ReadingPeriodicalGroupsResponse
	if visibleGroups.Code != 200 || json.Unmarshal(visibleGroups.Body.Bytes(), &groupPage) != nil || len(groupPage.Groups) != 1 {
		t.Fatal("visible admitted periodical group", visibleGroups.Code, visibleGroups.Body.String())
	}
	group := groupPage.Groups[0]
	if group.ID != series || group.Name != name || group.IssueCount != 2 || group.Latest.Item.ID != items[1] || group.Latest.Item.SourceID == "" || string(group.Latest.IssueDate) != dates[1] {
		t.Fatal("visible periodical group has no meaningful full membership", visibleGroups.Body.String())
	}
	for _, path := range []string{groupsPath(r.library), issuesPath(r.library, series)} {
		if response := readingHTTP(t, h, "GET", path, "", "", nil); response.Code != 401 {
			t.Fatal("anonymous periodical read", response.Code)
		}
	}
	assertPrivate := func(response *httptest.ResponseRecorder, hiddenLibrary bool) {
		t.Helper()
		for _, marker := range markers {
			if marker != "" && strings.Contains(response.Body.String(), marker) {
				t.Errorf("periodical refusal leaked a hidden marker %q: %s", marker, response.Body.String())
			}
		}
		if hiddenLibrary && strings.Contains(response.Body.String(), r.library) {
			t.Error("periodical refusal leaked hidden library", response.Body.String())
		}
		if response.Code == 401 || response.Code >= 500 {
			t.Errorf("valid periodical viewer answered %d: %s", response.Code, response.Body.String())
		}
	}
	assertSameRefusal := func(hidden, absent *httptest.ResponseRecorder, hiddenLibrary bool) {
		t.Helper()
		assertPrivate(hidden, hiddenLibrary)
		assertPrivate(absent, hiddenLibrary)
		if hidden.Code != 404 || absent.Code != 404 || bodyErrorCode(hidden.Body.String()) != "not_found" || bodyErrorCode(absent.Body.String()) != "not_found" {
			t.Errorf("hidden/absent periodical refusal differs: %d %s / %d %s", hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
		}
		if normalizedAnswer(hidden.Body.String(), r.library) != normalizedAnswer(absent.Body.String(), absentTwin(r.library)) {
			t.Errorf("hidden/absent periodical response shapes differ: %s / %s", hidden.Body.String(), absent.Body.String())
		}
	}

	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
		t.Fatal(err)
	}
	emptyGroups := get(groupsPath(r.library))
	var empty ReadingPeriodicalGroupsResponse
	assertPrivate(emptyGroups, false)
	if emptyGroups.Code != 200 || json.Unmarshal(emptyGroups.Body.Bytes(), &empty) != nil || len(empty.Groups) != 0 || empty.NextCursor != "" || empty.LibraryID != r.library || string(empty.PublicationType) != kind {
		t.Fatal("restricted periodical groups did not filter actual hidden membership", emptyGroups.Code, emptyGroups.Body.String())
	}
	absentSeries := strings.Repeat("a", 43)
	if absentSeries == series {
		absentSeries = strings.Repeat("b", 43)
	}
	assertSameRefusal(get(issuesPath(r.library, series)), get(issuesPath(r.library, absentSeries)), false)
	for _, item := range items {
		assertSameRefusal(get("/v1/items/"+item+"/reading/versions"), get("/v1/items/"+absentTwin(item)+"/reading/versions"), false)
	}

	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.f.id.EditDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
		t.Fatal(err)
	}
	assertSameRefusal(get(groupsPath(r.library)), get(groupsPath(absentTwin(r.library))), true)
	assertSameRefusal(get(issuesPath(r.library, series)), get(issuesPath(absentTwin(r.library), series)), true)
	for _, item := range items {
		assertSameRefusal(get("/v1/items/"+item+"/reading/versions"), get("/v1/items/"+absentTwin(item)+"/reading/versions"), true)
	}
}
