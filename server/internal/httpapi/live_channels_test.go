package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestLiveOwnerSourcePreviewPublishGuideAndMemberDenial(t *testing.T) {
	d, owner, member := liveHTTPFixture(t)
	store, e := livechannels.New(d.DB)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	d.LiveChannels = store
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	d.mountRegistry(mux, registry)
	d.liveChannelRoutes(mux, store)
	call := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(payload))
		if token != "" {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	in := livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Local schedule", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://fixture.invalid/provider?secret=do-not-expose\n", Guide: `<tv><programme channel="one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>Test programme</title></programme></tv>`}
	w := call("POST", "/v1/admin/live-sources/preview", owner.AccessToken, in)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	d.DB.QueryRow(`SELECT count(*) FROM live_sources`).Scan(&n)
	if n != 0 {
		t.Fatal("preview mutated source")
	}
	w = call("POST", "/v1/admin/live-sources", owner.AccessToken, in)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	q := url.Values{"kind": {"live-source"}, "start": {"2026-09-05T12:00:00Z"}, "end": {"2026-09-05T14:00:00Z"}, "timezone": {"UTC"}, "limit": {"30"}}
	path := "/v1/guide?" + q.Encode()
	// Anonymous viewers are refused; authenticated members receive a filtered
	// empty guide when every source is owner-only, not a blanket endpoint denial.
	w = call("GET", path, "", nil)
	if w.Code != 401 && w.Code != 403 {
		t.Fatal("anonymous guide admitted", w.Code)
	}
	w = call("GET", path, member.AccessToken, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Test programme") || strings.Contains(w.Body.String(), "Local schedule") {
		t.Fatal("owner-only source disclosed", w.Code, w.Body.String())
	}
	w = call("GET", path, owner.AccessToken, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Test programme") || strings.Contains(w.Body.String(), "do-not-expose") || strings.Contains(w.Body.String(), "fixture.invalid") {
		t.Fatal("guide projection", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("guide cached")
	}
	in.Guide = "bad <xml secret-provider-url"
	w = call("POST", "/v1/admin/live-sources/preview", owner.AccessToken, in)
	if w.Code != 400 || strings.Contains(w.Body.String(), "secret-provider-url") {
		t.Fatal("raw parser diagnostics escaped")
	}
}
func TestLiveRoutesRejectUnknownBodyAndQuery(t *testing.T) {
	d, owner, _ := liveHTTPFixture(t)
	store, _ := livechannels.New(d.DB)
	mux := http.NewServeMux()
	d.LiveChannels = store
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	d.mountRegistry(mux, registry)
	d.liveChannelRoutes(mux, store)
	for _, v := range []struct{ method, path, body string }{{"POST", "/v1/admin/live-sources/preview", `{"unexpected":"secret"}`}, {"GET", "/v1/guide?kind=live-source&kind=library-channel", ""}, {"GET", "/v1/admin/live-sources?private=secret", ""}} {
		r := httptest.NewRequest(v.method, v.path, strings.NewReader(v.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal("invalid request disclosure", w.Code, w.Body.String())
		}
	}
}

func liveHTTPFixture(t *testing.T) (Dependencies, identity.Envelope, identity.Envelope) {
	t.Helper()
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('live-owner','owner',x'00','owner-profile',1);INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('live-member','member',x'00','member-profile',1)`); e != nil {
		t.Fatal(e)
	}
	owner, e := ident.Issue("live-owner", "owner-profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	member, e := ident.Issue("live-member", "member-profile", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	return Dependencies{DB: db, Identity: ident}, owner, member
}

type liveRevokingReader struct {
	source *bytes.Reader
	revoke func()
	once   bool
}

func (r *liveRevokingReader) Read(p []byte) (int, error) {
	if !r.once {
		r.once = true
		r.revoke()
	}
	return r.source.Read(p)
}
func TestLivePreviewRechecksRevocationAfterRequestWork(t *testing.T) {
	d, owner, _ := liveHTTPFixture(t)
	store, _ := livechannels.New(d.DB)
	mux := http.NewServeMux()
	d.LiveChannels = store
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	d.mountRegistry(mux, registry)
	d.liveChannelRoutes(mux, store)
	in := livechannels.SourceInput{Name: "Secret source name", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",Private title\nhttps://fixture.invalid/stream\n"}
	body, _ := json.Marshal(in)
	reader := &liveRevokingReader{source: bytes.NewReader(body), revoke: func() {
		if _, e := d.DB.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE account_id='live-owner'`); e != nil {
			t.Fatal(e)
		}
	}}
	r := httptest.NewRequest("POST", "/v1/admin/live-sources/preview", reader)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 || strings.Contains(w.Body.String(), "Private title") {
		t.Fatal("late revocation disclosure", w.Code, w.Body.String())
	}
}

// CD-06: the remote-source preview tells the owner exactly which LAN root to
// confirm, in the published error envelope, and accepts that root back.
func TestLiveRemotePreviewAsksForLANConfirmation(t *testing.T) {
	d, owner, _ := liveHTTPFixture(t)
	store, e := livechannels.New(d.DB)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	d.LiveChannels = store
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	d.mountRegistry(mux, registry)
	d.liveChannelRoutes(mux, store)
	tuner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discover.json":
			_, _ = w.Write([]byte(`{"TunerCount":1}`))
		case "/lineup.json":
			_, _ = w.Write([]byte(`[{"GuideNumber":"2.1","GuideName":"Two","URL":"` + "http://" + r.Host + `/auto/v2.1"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer tuner.Close()
	draft := map[string]any{"id": strings.Repeat("ab", 24), "expectedRevision": 0, "name": "Antenna", "kind": "hdhomerun", "locator": tuner.URL + "/", "guideUrl": "", "username": "", "password": "", "confirmedLanRoots": []string{}, "mappings": []any{}, "refreshMs": 3600000, "ownerLimit": 0, "useDiscoveredCapacity": true, "viewerAccess": "owner-only"}
	preview := func() *httptest.ResponseRecorder {
		payload, _ := json.Marshal(draft)
		r := httptest.NewRequest("POST", "/v1/admin/live-sources/remote/preview", bytes.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	w := preview()
	if w.Code != 422 {
		t.Fatalf("unconfirmed LAN tuner: %d %s", w.Code, w.Body.String())
	}
	if e := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); e != nil {
		t.Fatal(e)
	}
	var refusal struct {
		Error struct {
			Code    string `json:"code"`
			Current struct {
				ConfirmRoots []string `json:"confirmRoots"`
			} `json:"current"`
		} `json:"error"`
	}
	if json.Unmarshal(w.Body.Bytes(), &refusal) != nil || refusal.Error.Code != "source_lan_confirmation_required" || len(refusal.Error.Current.ConfirmRoots) != 1 || refusal.Error.Current.ConfirmRoots[0] != tuner.URL+"/" {
		t.Fatalf("refusal: %s", w.Body.String())
	}
	draft["confirmedLanRoots"] = refusal.Error.Current.ConfirmRoots
	if w = preview(); w.Code != 200 {
		t.Fatalf("confirmed LAN tuner: %d %s", w.Code, w.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/admin/live-sources/remote/preview", w)
}
