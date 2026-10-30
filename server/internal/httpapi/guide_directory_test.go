package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

type guideDirectoryHTTPFixture struct {
	d             Dependencies
	store         *livechannels.Store
	mux           *http.ServeMux
	owner, member identity.Envelope
	a, b          livechannels.SourceInput
}

func TestGuideHTTPKeepsAuthorizedDirectoryMarkAcrossFiltering(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	for _, token := range []string{f.owner.AccessToken, f.member.AccessToken} {
		directory := f.directory(t, token, url.Values{"sourceId": {f.a.ID}, "search": {"Channel 00"}, "limit": {"1"}})
		if len(directory.Channels) != 1 || directory.Channels[0].Mark == nil {
			t.Fatal(directory)
		}
		q := url.Values{"kind": {"live-source"}, "sourceId": {f.a.ID}, "search": {"Channel 00"}, "programmes": {"none"}, "start": {"2026-09-05T12:00:00Z"}, "end": {"2026-09-05T13:00:00Z"}, "timezone": {"UTC"}, "limit": {"1"}}
		response := f.call(t, "GET", "/v1/guide?"+q.Encode(), token, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("guide: %d %s", response.Code, response.Body)
		}
		var envelope struct {
			Guide livechannels.Guide `json:"guide"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Guide.Channels) != 1 || !reflect.DeepEqual(envelope.Guide.Channels[0].Mark, directory.Channels[0].Mark) {
			t.Fatalf("guide lost full-source directory mark: %+v vs %+v", envelope.Guide.Channels, directory.Channels)
		}
	}
}

func TestGuideDirectoryHTTPNeighborScopeAndUnavailableRuntime(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	page := f.directory(t, f.owner.AccessToken, url.Values{"sort": {"number"}, "limit": {"1"}})
	anchor := page.Channels[0]
	q := url.Values{"sort": {"number"}, "limit": {"1"}, "anchorChannelId": {anchor.ID}, "anchorSourceId": {anchor.SourceID}, "anchorProvenance": {string(anchor.Provenance)}, "direction": {"next"}, "revision": {page.Revision}}
	// This fixture has no delivery runtime. It still resolves the authorized
	// anchor and its exact view revision, but never advertises an unplayable step.
	neighbor := f.directory(t, f.owner.AccessToken, q)
	if neighbor.Total != 63 || neighbor.Limit != 1 || neighbor.Revision != page.Revision || len(neighbor.Channels) != 0 || neighbor.Offset != 0 {
		t.Fatalf("unavailable runtime neighbor: %+v", neighbor)
	}
	q.Set("anchorChannelId", "unknown")
	q.Del("revision")
	unknown := f.call(t, "GET", "/v1/guide/channels?"+q.Encode(), f.owner.AccessToken, nil)
	q.Set("anchorChannelId", anchor.ID)
	q.Set("group", "Entertainment")
	filtered := f.call(t, "GET", "/v1/guide/channels?"+q.Encode(), f.owner.AccessToken, nil)
	private := f.directory(t, f.owner.AccessToken, url.Values{"sourceId": {f.b.ID}, "limit": {"1"}}).Channels[0]
	q.Del("group")
	q.Set("anchorChannelId", private.ID)
	q.Set("anchorSourceId", private.SourceID)
	forbidden := f.call(t, "GET", "/v1/guide/channels?"+q.Encode(), f.member.AccessToken, nil)
	if unknown.Code != 404 || filtered.Code != 404 || forbidden.Code != 404 || unknown.Body.String() != filtered.Body.String() || unknown.Body.String() != forbidden.Body.String() {
		t.Fatalf("anchor existence disclosed: unknown=%d %s filtered=%d %s forbidden=%d %s", unknown.Code, unknown.Body, filtered.Code, filtered.Body, forbidden.Code, forbidden.Body)
	}
	for _, change := range []func(url.Values){func(q url.Values) { q.Set("offset", "0") }, func(q url.Values) { q.Set("limit", "50") }, func(q url.Values) { q.Set("direction", "sideways") }, func(q url.Values) { q.Del("anchorSourceId") }} {
		copy := url.Values{}
		for k, v := range q {
			copy[k] = append([]string{}, v...)
		}
		change(copy)
		w := f.call(t, "GET", "/v1/guide/channels?"+copy.Encode(), f.owner.AccessToken, nil)
		if w.Code != 400 {
			t.Fatalf("invalid neighbor request admitted: %d %s", w.Code, w.Body)
		}
	}
}

func newGuideDirectoryHTTPFixture(t *testing.T) guideDirectoryHTTPFixture {
	t.Helper()
	d, owner, member := liveHTTPFixture(t)
	store, err := livechannels.New(d.DB)
	if err != nil {
		t.Fatal(err)
	}
	d.LiveChannels = store
	f := guideDirectoryHTTPFixture{d: d, store: store, mux: http.NewServeMux(), owner: owner, member: member}
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	d.mountRegistry(f.mux, registry)
	d.liveChannelRoutes(f.mux, store)
	var playlist, guide strings.Builder
	playlist.WriteString("#EXTM3U\n")
	guide.WriteString("<tv>")
	// Deliberately reverse publication order: sorting must happen before LIMIT.
	for i := 59; i >= 0; i-- {
		group := "Entertainment"
		if i%2 == 0 {
			group = "News"
		}
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=\"a%d\" tvg-chno=\"%d\" group-title=\"%s\",Channel %02d\nhttps://provider.invalid/a%d?secret=private-provider-value\n", i, i+2, group, i, i)
		fmt.Fprintf(&guide, `<programme channel="a%d" start="20260905120000 +0000" stop="20260905130000 +0000"><title>Private EPG %d</title></programme>`, i, i)
	}
	guide.WriteString("</tv>")
	f.a = livechannels.SourceInput{ID: strings.Repeat("aa", 24), RequestID: strings.Repeat("a1", 24), Name: "A provider", Playlist: playlist.String(), Guide: guide.String()}
	f.b = livechannels.SourceInput{ID: strings.Repeat("bb", 24), RequestID: strings.Repeat("b1", 24), Name: "B private provider", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"z\" tvg-chno=\"12\" group-title=\"Entertainment\",Zulu\nhttps://provider.invalid/z\n#EXTINF:-1 tvg-id=\"m\" tvg-chno=\"9\" group-title=\"News\",Middle\nhttps://provider.invalid/m\n#EXTINF:-1 tvg-id=\"a\" tvg-chno=\"1\" group-title=\"News\",Alpha\nhttps://provider.invalid/a\n"}
	for _, in := range []livechannels.SourceInput{f.a, f.b} {
		w := f.call(t, "POST", "/v1/admin/live-sources", owner.AccessToken, in)
		if w.Code != http.StatusOK {
			t.Fatalf("publish: %d %s", w.Code, w.Body.String())
		}
	}
	// Uploaded providers default to owner-only; make just A available to members.
	if _, err = d.DB.Exec(`INSERT INTO live_source_settings(source_id,viewer_access) VALUES(?,'server-members')`, f.a.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f guideDirectoryHTTPFixture) call(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Code == http.StatusOK && (r.URL.Path == "/v1/guide/channels" || r.URL.Path == "/v1/guide/sources") {
		tl11AssertChannelGuideSpecResponse(t, method, r.URL.Path, w)
	}
	return w
}

func (f guideDirectoryHTTPFixture) directory(t *testing.T, token string, q url.Values) livechannels.Directory {
	t.Helper()
	w := f.call(t, "GET", "/v1/guide/channels?"+q.Encode(), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("directory: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("viewer directory is cacheable")
	}
	var envelope struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		ServerID        string                 `json:"serverId"`
		Directory       livechannels.Directory `json:"directory"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ProtocolVersion != "1.0" || envelope.ServerID != f.d.Identity.ID() || len(envelope.Directory.Revision) != 64 || envelope.Directory.ViewerFence == "" {
		t.Fatalf("invalid envelope: %+v", envelope)
	}
	if strings.Contains(w.Body.String(), "provider.invalid") || strings.Contains(w.Body.String(), "private-provider-value") || strings.Contains(w.Body.String(), "Private EPG") {
		t.Fatal("directory leaked provider or EPG data")
	}
	for _, c := range envelope.Directory.Channels {
		if c.Programmes == nil || len(c.Programmes) != 0 {
			t.Fatalf("rows-only programme projection: %+v", c)
		}
	}
	return envelope.Directory
}

func TestGuideDirectoryHTTPPagingAndAuthorizedSourceSummary(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	first := f.directory(t, f.owner.AccessToken, url.Values{"limit": {"50"}, "sort": {"number"}})
	if first.Total != 63 || first.Offset != 0 || first.Limit != 50 || len(first.Channels) != 50 || first.Channels[0].Name != "Channel 00" || first.Channels[49].Name != "Channel 49" {
		t.Fatalf("numeric first page: %+v", first)
	}
	second := f.directory(t, f.owner.AccessToken, url.Values{"offset": {"50"}, "limit": {"50"}, "sort": {"number"}, "revision": {first.Revision}})
	if second.Total != 63 || second.Revision != first.Revision || len(second.Channels) != 13 {
		t.Fatalf("numeric next page: %+v", second)
	}
	names := []string{}
	for _, c := range second.Channels {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names[10:], []string{"Alpha", "Middle", "Zulu"}) {
		t.Fatalf("provider numeric ordering: %v", names)
	}
	byName := f.directory(t, f.owner.AccessToken, url.Values{"sort": {"name"}, "limit": {"1"}})
	if byName.Channels[0].Name != "Alpha" {
		t.Fatalf("global name sort: %+v", byName.Channels)
	}
	nameEnd := f.directory(t, f.owner.AccessToken, url.Values{"sort": {"name"}, "offset": {"60"}, "limit": {"3"}, "revision": {byName.Revision}})
	names = nil
	for _, c := range nameEnd.Channels {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{"Channel 59", "Middle", "Zulu"}) {
		t.Fatalf("global name page boundary: %v", names)
	}
	member := f.directory(t, f.member.AccessToken, url.Values{"limit": {"50"}})
	if member.Total != 60 || member.ViewerFence == first.ViewerFence {
		t.Fatalf("member projection: %+v", member)
	}
	private := f.directory(t, f.member.AccessToken, url.Values{"sourceId": {f.b.ID}})
	if private.Total != 0 || len(private.Channels) != 0 || len(private.Sources) != 0 {
		t.Fatal("private provider disclosed by filter")
	}
	for _, test := range []struct {
		token string
		count int
	}{{f.owner.AccessToken, 2}, {f.member.AccessToken, 1}} {
		w := f.call(t, "GET", "/v1/guide/sources", test.token, nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("summary: %d %s", w.Code, w.Body.String())
		}
		var summary livechannels.DirectorySummary
		if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
			t.Fatal(err)
		}
		if len(summary.Sources) != test.count || summary.ViewerFence == "" || len(summary.Revision) != 64 {
			t.Fatalf("source summary: %+v", summary)
		}
		if summary.Sources[0].ChannelCount != 60 || len(summary.Sources[0].Groups) != 2 || len(summary.Sources[0].GroupCounts) != 2 {
			t.Fatalf("summary aggregates: %+v", summary.Sources[0])
		}
		if test.count == 2 && (summary.Sources[1].ID != f.b.ID || summary.Sources[1].ChannelCount != 3 || summary.Sources[1].Position != 1) {
			t.Fatalf("provider beyond first channel page lost: %+v", summary.Sources)
		}
		for _, forbidden := range []string{"programmes", "Private EPG", "provider.invalid", "private-provider-value", "playlist", "locator"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatalf("summary disclosed %q", forbidden)
			}
		}
		if test.count == 1 && strings.Contains(w.Body.String(), "B private provider") {
			t.Fatal("member summary disclosed private source")
		}
	}
}

func TestGuideDirectoryHTTPFiltersPreferencesAndRevisionFences(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	base := f.directory(t, f.owner.AccessToken, nil)
	for _, test := range []struct {
		q     url.Values
		total int
		first string
	}{
		{url.Values{"group": {"News"}, "sort": {"name"}}, 32, "Alpha"},
		{url.Values{"search": {"channel 0"}}, 10, "Channel 00"},
		{url.Values{"sourceId": {f.b.ID}, "group": {"News"}, "sort": {"number"}}, 2, "Alpha"},
		{url.Values{"sourceId": {strings.Repeat("ff", 24)}}, 0, ""},
	} {
		got := f.directory(t, f.owner.AccessToken, test.q)
		if got.Total != test.total || (test.total > 0 && got.Channels[0].Name != test.first) {
			t.Fatalf("filter %v: %+v", test.q, got)
		}
	}
	for i, c := range base.Channels[:2] {
		in := livechannels.PreferenceInput{RequestID: fmt.Sprintf("%048x", i+1), SourceID: c.SourceID, ChannelID: c.ID, Favorite: true, Hidden: i == 0}
		w := f.call(t, "POST", "/v1/channels/preferences", f.owner.AccessToken, in)
		if w.Code != 200 {
			t.Fatalf("preference: %d %s", w.Code, w.Body.String())
		}
	}
	w := f.call(t, "GET", "/v1/guide/channels?offset=50&revision="+base.Revision, f.owner.AccessToken, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "guide_refresh_required") {
		t.Fatalf("stale preference revision accepted: %d %s", w.Code, w.Body.String())
	}
	// A profile's own hidden mark is ignored: only the owner hides, for everyone.
	favorites := f.directory(t, f.owner.AccessToken, url.Values{"favorites": {"true"}})
	if favorites.Total != 2 || favorites.Channels[0].Hidden || !favorites.Channels[0].Favorite {
		t.Fatalf("visible favorites: %+v", favorites)
	}
	hidden := f.directory(t, f.owner.AccessToken, url.Values{"favorites": {"true"}, "includeHidden": {"true"}})
	if hidden.Total != 2 || hidden.Channels[0].Hidden || !hidden.Channels[0].Favorite {
		t.Fatalf("owner hidden favorites: %+v", hidden)
	}
	if got := f.directory(t, f.member.AccessToken, url.Values{"favorites": {"true"}}); got.Total != 0 {
		t.Fatal("owner preferences leaked to member")
	}
	if w = f.call(t, "GET", "/v1/guide/channels?includeHidden=true", f.member.AccessToken, nil); w.Code != 403 {
		t.Fatalf("member hidden channels admitted: %d", w.Code)
	}
	if w = f.call(t, "GET", "/v1/guide/sources?includeHidden=true", f.member.AccessToken, nil); w.Code != 403 {
		t.Fatalf("member hidden summary admitted: %d", w.Code)
	}
	for _, test := range []struct {
		query                     string
		channels, favorites, news int
	}{{"", 60, 2, 30}, {"?includeHidden=true", 60, 2, 30}} { // a profile's own hidden mark no longer filters
		w = f.call(t, "GET", "/v1/guide/sources"+test.query, f.owner.AccessToken, nil)
		var summary livechannels.DirectorySummary
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &summary) != nil || len(summary.Sources) != 2 {
			t.Fatalf("preference summary: %d %s", w.Code, w.Body.String())
		}
		source := summary.Sources[0]
		if source.ChannelCount != test.channels || source.FavoriteCount != test.favorites || source.RecordAvailable || source.UnavailableReasons["channel_runtime_unavailable"] != test.channels {
			t.Fatalf("summary preference or runtime aggregates: %+v", source)
		}
		for _, group := range source.GroupCounts {
			if group.Name == "News" && group.Count != test.news {
				t.Fatalf("hidden group count: %+v", source.GroupCounts)
			}
		}
	}
	current := f.directory(t, f.owner.AccessToken, nil)
	// Republishing must invalidate the continuation and old tuning generation.
	old := current.Channels[0]
	in := f.a
	in.ExpectedRevision, in.RequestID, in.Name = 1, strings.Repeat("a2", 24), "A refreshed provider"
	if w = f.call(t, "POST", "/v1/admin/live-sources", f.owner.AccessToken, in); w.Code != 200 {
		t.Fatalf("replacement: %d %s", w.Code, w.Body.String())
	}
	if w = f.call(t, "GET", "/v1/guide/channels?offset=50&revision="+current.Revision, f.owner.AccessToken, nil); w.Code != 409 {
		t.Fatalf("stale source revision accepted: %d %s", w.Code, w.Body.String())
	}
	updated := f.directory(t, f.owner.AccessToken, nil)
	tx, err := f.d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = f.store.InputTx(context.Background(), tx, old.SourceID, old.ID, old.Generation); !errors.Is(err, livechannels.ErrConflict) {
		t.Fatalf("old tuning generation accepted: %v", err)
	}
	c := updated.Channels[0]
	input, err := f.store.InputTx(context.Background(), tx, c.SourceID, c.ID, c.Generation)
	if err != nil || input.SourceID != c.SourceID || input.ChannelID != c.ID || input.Generation != c.Generation || !strings.HasPrefix(input.Locator, "https://provider.invalid/") {
		t.Fatalf("directory tuning handle cannot resolve: %+v %v", input, err)
	}
}

func TestGuideDirectoryHTTPPolicyChangesFenceMemberPages(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	before := f.directory(t, f.member.AccessToken, url.Values{"limit": {"1"}})
	if _, err := f.d.DB.Exec(`UPDATE live_source_settings SET viewer_access='owner-only' WHERE source_id=?`, f.a.ID); err != nil {
		t.Fatal(err)
	}
	w := f.call(t, "GET", "/v1/guide/channels?limit=1&offset=1&revision="+before.Revision, f.member.AccessToken, nil)
	if w.Code != 409 || strings.Contains(w.Body.String(), "Channel") {
		t.Fatalf("revoked provider continuation disclosed rows: %d %s", w.Code, w.Body.String())
	}
	after := f.directory(t, f.member.AccessToken, url.Values{"limit": {"1"}})
	if after.Total != 0 || len(after.Channels) != 0 || after.ViewerFence == before.ViewerFence || after.Revision == before.Revision {
		t.Fatalf("revoked policy projection: %+v", after)
	}
	w = f.call(t, "GET", "/v1/guide/sources", f.member.AccessToken, nil)
	var summary livechannels.DirectorySummary
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &summary) != nil || len(summary.Sources) != 0 {
		t.Fatalf("revoked source summary: %d %s", w.Code, w.Body.String())
	}
}

func TestGuideDirectoryHTTPRejectsInvalidQueriesAndAnonymousViewers(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	for _, path := range []string{"/v1/guide/channels", "/v1/guide/sources"} {
		if w := f.call(t, "GET", path, "", nil); w.Code != 401 && w.Code != 403 {
			t.Fatalf("anonymous directory: %d %s", w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"unknown=private", "limit=1&limit=2", "limit=0", "limit=51", "limit=01", "offset=-1", "offset=9999999", "sort=invalid", "kind=invalid", "favorites=1", "includeHidden=yes", "timezone=not-a-zone", "search=" + strings.Repeat("x", 121), "group=" + strings.Repeat("x", 121), "sourceId=" + strings.Repeat("x", 257), "revision=" + strings.Repeat("x", 65)} {
		w := f.call(t, "GET", "/v1/guide/channels?"+query, f.owner.AccessToken, nil)
		if w.Code != 400 {
			t.Fatalf("invalid directory query %q: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"limit=1", "sourceId=private", "timezone=UTC&timezone=UTC", "includeHidden=1", "timezone=not-a-zone"} {
		w := f.call(t, "GET", "/v1/guide/sources?"+query, f.owner.AccessToken, nil)
		if w.Code != 400 {
			t.Fatalf("invalid summary query %q: %d %s", query, w.Code, w.Body.String())
		}
	}
}

func TestGuideFeatureHTTPFencesAndTypedAirings(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	query := url.Values{"start": {"2026-09-05T12:00:00Z"}, "end": {"2026-09-06T12:00:00Z"}, "search": {"Private EPG"}, "limit": {"7"}}
	w := f.call(t, "GET", "/v1/guide/airings?"+query.Encode(), f.member.AccessToken, nil)
	if w.Code != 200 {
		t.Fatalf("airings %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Airings livechannels.AiringsPage `json:"airings"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	if len(envelope.Airings.Groups) != 7 || envelope.Airings.NextCursor == "" {
		t.Fatalf("airing page %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-provider-value") || strings.Contains(w.Body.String(), f.b.ID) {
		t.Fatal("source facts leaked", w.Body.String())
	}
	query.Set("cursor", envelope.Airings.NextCursor)
	w = f.call(t, "GET", "/v1/guide/airings?"+query.Encode(), f.member.AccessToken, nil)
	if w.Code != 200 {
		t.Fatal("cursor", w.Code, w.Body.String())
	}
	p, e := f.d.Identity.Authenticate(f.member.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,allow_live_tv) VALUES(?,0) ON CONFLICT(profile_id) DO UPDATE SET allow_live_tv=0,revision=revision+1`, p.ProfileID); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/v1/guide/airings?" + query.Encode(), "/v1/guide/on-now", "/v1/guide/now-next", "/v1/guide/reminders"} {
		w = f.call(t, "GET", path, f.member.AccessToken, nil)
		if w.Code == 200 || strings.Contains(w.Body.String(), "Private EPG") || strings.Contains(w.Body.String(), "Channel 01") {
			t.Fatalf("Live TV refusal %s %d %s", path, w.Code, w.Body.String())
		}
	}
	// The owner is not a substitute for a profile's recording grant. Directory
	// capability remains false even when capture runtime is configured later.
	for _, path := range []string{"/v1/guide/on-now?limit=0", "/v1/guide/airings?start=nope", "/v1/guide/now-next?offset=1"} {
		w = f.call(t, "GET", path, f.owner.AccessToken, nil)
		if w.Code != 400 {
			t.Fatalf("invalid query %s %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestGuideReminderHTTPPersistsOnlyForItsProfile(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	input := f.a
	input.ExpectedRevision = 1
	input.RequestID = strings.Repeat("cf", 24)
	input.Guide = fmt.Sprintf(`<tv><programme channel="a0" start="%s" stop="%s"><title>Reminder program</title></programme></tv>`, now.Add(time.Hour).Format("20060102150405 -0700"), now.Add(2*time.Hour).Format("20060102150405 -0700"))
	w := f.call(t, "POST", "/v1/admin/live-sources", f.owner.AccessToken, input)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	q := url.Values{"search": {"Reminder program"}, "start": {now.Format(time.RFC3339)}, "end": {now.Add(24 * time.Hour).Format(time.RFC3339)}}
	w = f.call(t, "GET", "/v1/guide/airings?"+q.Encode(), f.owner.AccessToken, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page struct {
		Airings livechannels.AiringsPage `json:"airings"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || len(page.Airings.Groups) != 1 {
		t.Fatalf("airing %s %v", w.Body.String(), e)
	}
	v := page.Airings.Groups[0].Airings[0]
	body := livechannels.ReminderInput{SourceID: v.Channel.SourceID, ChannelID: v.Channel.ID, Generation: v.Channel.Generation, ProgrammeID: v.Programme.ID, Kind: v.Channel.Provenance}
	w = f.call(t, "PUT", "/v1/guide/reminders", f.owner.AccessToken, body)
	if w.Code != 200 {
		t.Fatal("save reminder", w.Code, w.Body.String())
	}
	var saved struct {
		Result livechannels.Reminder `json:"result"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &saved); e != nil || saved.Result.ID == "" {
		t.Fatal(w.Body.String(), e)
	}
	w = f.call(t, "GET", "/v1/guide/reminders", f.owner.AccessToken, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Reminder program") {
		t.Fatal("stored reminder", w.Code, w.Body.String())
	}
	w = f.call(t, "GET", "/v1/guide/reminders", f.member.AccessToken, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), saved.Result.ID) {
		t.Fatal("profile leak", w.Code, w.Body.String())
	}
	w = f.call(t, "DELETE", "/v1/guide/reminders/"+saved.Result.ID, f.owner.AccessToken, map[string]int{"expectedRevision": 1})
	if w.Code != 200 {
		t.Fatal("delete reminder", w.Code, w.Body.String())
	}
}
