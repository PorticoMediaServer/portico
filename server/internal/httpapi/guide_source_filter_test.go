package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// NEW-22: channel sources are first-class. The guide accepts sourceId and
// returns only that source's channels, paging before dropping, with each
// channel naming its source. An unknown sourceId is an empty page, not an
// error. Saving a source with logos enqueues a pending logo job in the same
// transaction (via the live_sources triggers).
func TestGuideSourceIDFilterAndLogoJobs(t *testing.T) {
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
	call := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(payload))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	sourceA := strings.Repeat("aa", 24)
	sourceB := strings.Repeat("bb", 24)
	inA := livechannels.SourceInput{
		ID:        sourceA,
		RequestID: strings.Repeat("a1", 24),
		Name:      "Antenna A",
		Playlist: "#EXTM3U\n" +
			"#EXTINF:-1 tvg-id=\"a-one\" tvg-logo=\"https://img.invalid/a-one.png\",A One\nhttps://fixture.invalid/a-one\n" +
			"#EXTINF:-1 tvg-id=\"a-two\" tvg-logo=\"https://img.invalid/a-two.png\",A Two\nhttps://fixture.invalid/a-two\n",
		Guide: `<tv>` +
			`<programme channel="a-one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>A One Show</title></programme>` +
			`<programme channel="a-two" start="20260905120000 +0000" stop="20260905130000 +0000"><title>A Two Show</title></programme>` +
			`</tv>`,
	}
	inB := livechannels.SourceInput{
		ID:        sourceB,
		RequestID: strings.Repeat("b1", 24),
		Name:      "Antenna B",
		Playlist: "#EXTM3U\n" +
			"#EXTINF:-1 tvg-id=\"b-one\" tvg-logo=\"https://img.invalid/b-one.png\",B One\nhttps://fixture.invalid/b-one\n",
		Guide: `<tv><programme channel="b-one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>B One Show</title></programme></tv>`,
	}
	if w := call("POST", "/v1/admin/live-sources", owner.AccessToken, inA); w.Code != 200 {
		t.Fatalf("save A: %d %s", w.Code, w.Body.String())
	}
	if w := call("POST", "/v1/admin/live-sources", owner.AccessToken, inB); w.Code != 200 {
		t.Fatalf("save B: %d %s", w.Code, w.Body.String())
	}
	// A new source with logos gets a pending logo job.
	for _, id := range []string{sourceA, sourceB} {
		var state string
		if e = d.DB.QueryRow(`SELECT state FROM live_logo_jobs WHERE source_id=?`, id).Scan(&state); e != nil {
			t.Fatalf("logo job for %s: %v", id, e)
		}
		if state != "pending" {
			t.Fatalf("logo job for %s is %q, want pending", id, state)
		}
	}
	guide := func(params url.Values) (int, livechannels.Guide) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/guide?"+params.Encode(), nil)
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var body struct {
			Guide livechannels.Guide `json:"guide"`
		}
		if w.Code != 200 {
			return w.Code, livechannels.Guide{}
		}
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatalf("guide decode: %v %s", e, w.Body.String())
		}
		return w.Code, body.Guide
	}
	base := url.Values{
		"kind":     {"live-source"},
		"start":    {"2026-09-05T12:00:00Z"},
		"end":      {"2026-09-05T14:00:00Z"},
		"timezone": {"UTC"},
		"limit":    {"30"},
	}
	// sourceId=A returns only A's channels, each naming its source.
	filtered := url.Values{}
	for k, v := range base {
		filtered[k] = v
	}
	filtered.Set("sourceId", sourceA)
	code, g := guide(filtered)
	if code != 200 {
		t.Fatalf("filtered guide: %d", code)
	}
	if len(g.Channels) != 2 {
		t.Fatalf("sourceId filter returned %d channels, want 2: %+v", len(g.Channels), g.Channels)
	}
	for _, c := range g.Channels {
		if c.SourceID != sourceA {
			t.Fatalf("channel %q names source %q, want %q", c.ID, c.SourceID, sourceA)
		}
	}
	// Paging applies to the filtered set, never by dropping rows from a page.
	paged := url.Values{}
	for k, v := range base {
		paged[k] = v
	}
	paged.Set("sourceId", sourceA)
	paged.Set("limit", "1")
	code, first := guide(paged)
	if code != 200 || len(first.Channels) != 1 || first.Channels[0].SourceID != sourceA {
		t.Fatalf("paged first: %d %+v", code, first.Channels)
	}
	if first.NextCursor == "" {
		t.Fatal("first page of two filtered channels has no cursor")
	}
	paged.Set("cursor", first.NextCursor)
	code, second := guide(paged)
	if code != 200 || len(second.Channels) != 1 || second.Channels[0].SourceID != sourceA {
		t.Fatalf("paged second: %d %+v", code, second.Channels)
	}
	if first.Channels[0].ID == second.Channels[0].ID {
		t.Fatalf("paging repeated channel %q", first.Channels[0].ID)
	}
	// An unknown sourceId is an empty page, not an error.
	unknown := url.Values{}
	for k, v := range base {
		unknown[k] = v
	}
	unknown.Set("sourceId", strings.Repeat("ff", 24))
	code, empty := guide(unknown)
	if code != 200 {
		t.Fatalf("unknown sourceId: %d", code)
	}
	if len(empty.Channels) != 0 {
		t.Fatalf("unknown sourceId returned %d channels", len(empty.Channels))
	}
	if empty.NextCursor != "" {
		t.Fatalf("unknown sourceId returned a cursor %q", empty.NextCursor)
	}
}
