package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Exercise the real HTTP producer and optionally retain its non-secret data
// bytes for a client-parser check. The response contains fixture preferences,
// never the fixture's sign-in token.
func TestApprovedPreferencesHTTPDefaultsAndNoPreference(t *testing.T) {
	f := preferenceHTTP(t)
	read := f.data(f.call("GET", "/v1/preferences?deviceClass=tablet", nil))
	effective := read["effective"].(map[string]any)
	if effective["playback.subtitleMode"] != "foreign" || effective["privacy.showActivityToMembers"] != false {
		t.Fatal(effective)
	}
	data := f.data(f.call("PATCH", "/v1/preferences", map[string]any{"scope": "profile-server", "deviceClass": "tablet", "expectedRevision": 1, "idempotencyKey": "no-preference", "values": map[string]any{"playback.preferredAudioLanguages": []string{}, "playback.preferredSubtitleLanguages": []string{}, "playback.subtitleColor": "yellow", "reading.theme": "sepia", "reading.textSize": 7}}))
	effective = data["effective"].(map[string]any)
	if len(effective["playback.preferredAudioLanguages"].([]any)) != 0 || len(effective["playback.preferredSubtitleLanguages"].([]any)) != 0 || effective["playback.subtitleColor"] != "yellow" || effective["reading.theme"] != "sepia" {
		t.Fatal(effective)
	}
	for _, entry := range []struct {
		key   string
		value any
	}{{"music.defaultSpeed", 1}, {"appearance.reduceMotion", true}, {"playback.preferredAudioLanguages", []string{"@original", "@original"}}, {"playback.preferredSubtitleLanguages", []string{"@original"}}} {
		bad := f.call("PATCH", "/v1/preferences", map[string]any{"scope": "profile-server", "deviceClass": "tablet", "expectedRevision": 2, "idempotencyKey": "negative-" + entry.key, "values": map[string]any{entry.key: entry.value}})
		if bad.Code != 400 {
			t.Fatal(entry, bad.Code, bad.Body.String())
		}
	}
	if out := os.Getenv("PORTICO_PREFERENCE_CONTRACT_FILE"); out != "" {
		bytes, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(out, append(bytes, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// This proves Settings persistence for the fifteen approved Reading rows. It
// does not claim a reader effect; the reader core owns those effects in Phase 8.
func TestReadingPreferencesSaveReloadAndClearAllFifteenRows(t *testing.T) {
	f := preferenceHTTP(t)
	initial := f.data(f.call("GET", "/v1/preferences?deviceClass=web", nil))
	defaults := initial["effective"].(map[string]any)
	choices := []struct {
		key   string
		value any
	}{
		{"reading.theme", "sepia"}, {"reading.font", "manrope"}, {"reading.textSize", float64(7)},
		{"reading.lineSpacing", "loose"}, {"reading.margins", "wide"}, {"reading.alignment", "left"},
		{"reading.layout", "scroll"}, {"reading.twoPages", false}, {"reading.tapToTurn", false},
		{"reading.hyphenation", false}, {"reading.highlightColour", "pink"}, {"reading.pdfView", "spread"},
		{"comics.fit", "height"}, {"comics.direction", "rtl"}, {"comics.firstPageAlone", false},
	}
	revision := int64(1)
	expected := map[string]any{}
	check := func(snapshot map[string]any) {
		t.Helper()
		effective := snapshot["effective"].(map[string]any)
		sources := snapshot["effectiveSource"].(map[string]any)
		for _, choice := range choices {
			want, stored := expected[choice.key]
			source := "profile-server"
			if !stored {
				want, source = defaults[choice.key], "default"
			}
			if !reflect.DeepEqual(effective[choice.key], want) || sources[choice.key] != source {
				t.Fatalf("%s: value/source %v/%v, want %v/%s", choice.key, effective[choice.key], sources[choice.key], want, source)
			}
		}
		for _, raw := range snapshot["documents"].([]any) {
			document := raw.(map[string]any)
			if document["scope"] == "profile-server" && document["revision"] != float64(revision) {
				t.Fatalf("revision %v, want %d", document["revision"], revision)
			}
		}
	}
	for _, clear := range []bool{false, true} {
		for _, choice := range choices {
			value, operation := choice.value, "save-"
			if clear {
				value, operation = nil, "clear-"
				delete(expected, choice.key)
			} else {
				expected[choice.key] = value
			}
			saved := f.data(f.call("PATCH", "/v1/preferences", map[string]any{
				"scope": "profile-server", "deviceClass": "web", "expectedRevision": revision,
				"idempotencyKey": operation + choice.key, "values": map[string]any{choice.key: value},
			}))
			revision++
			check(saved)
			check(f.data(f.call("GET", "/v1/preferences?deviceClass=web", nil)))
		}
	}
}

type preferenceFixture struct {
	db      *sql.DB
	ident   *identity.Service
	handler http.Handler
	token   string
	t       *testing.T
}

func preferenceHTTP(t *testing.T) preferenceFixture {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ident, e := identity.New(db, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	a := c.Library("a", "Allowed", "movie", "/a")
	c.Movie(a, "/a/first.mkv", "Harbor First", 2020)
	c.Movie(a, "/a/second.mkv", "Harbor Second", 2020)
	c.Drain()
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", tl6HostedRootPin(), tl6HostedRootID())
	if e != nil {
		t.Fatal(e)
	}
	policy := hosted.Policy{ServerID: ident.ID(), Revision: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "p", Role: "member", AllowedLibraries: []string{"a"}}}}
	raw, _ := json.Marshal(policy)
	if e = storeHTTPFixturePolicy(db, raw); e != nil {
		t.Fatal(e)
	}
	session, e := tl6IssueHostedFixture(t, db, ident, "member", "p", "member")
	if e != nil {
		t.Fatal(e)
	}
	return preferenceFixture{handler: New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control, Console: operations.New(db)}), token: session.AccessToken, t: t, db: db, ident: ident}
}

func (f preferenceFixture) call(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			f.t.Fatal(e)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f preferenceFixture) data(w *httptest.ResponseRecorder) map[string]any {
	f.t.Helper()
	if w.Code != 200 {
		f.t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		f.t.Fatal(e)
	}
	return envelope.Data
}

func TestPreferencesPublishRegistryAndPatchSemantics(t *testing.T) {
	f := preferenceHTTP(t)
	data := f.data(f.call("GET", "/v1/preferences?deviceClass=web", nil))
	registry, ok := data["registry"].(map[string]any)
	if !ok || registry["revision"] != operations.PreferenceRegistryRevision {
		t.Fatalf("registry missing: %+v", data)
	}
	fields, ok := registry["fields"].([]any)
	if !ok || len(fields) != len(operations.PreferenceRegistry()) {
		t.Fatalf("registry fields missing: %+v", registry)
	}
	first, _ := fields[0].(map[string]any)
	for _, key := range []string{"key", "type", "default", "scopes", "group", "labelKey"} {
		if _, present := first[key]; !present {
			t.Fatalf("registry field is incomplete: %+v", first)
		}
	}
	effective, ok := data["effective"].(map[string]any)
	if !ok || effective["search.rememberHistory"] != true || effective["playback.playedThresholdPercent"] != float64(95) {
		t.Fatalf("effective values missing: %+v", data)
	}
	if _, ok = data["clampedFields"].([]any); !ok {
		t.Fatalf("clampedFields missing: %+v", data)
	}
	// An unknown key is refused by name rather than dropped.
	w := f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 1, "idempotencyKey": "one", "scope": "profile-server", "deviceClass": "web", "values": map[string]any{"playback.warpDrive": true}})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "values.playback.warpDrive") {
		t.Fatalf("unknown key was not named: %d %s", w.Code, w.Body.String())
	}
	// A partial patch writes only the submitted keys and clamps out-of-range numbers.
	data = f.data(f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 1, "idempotencyKey": "two", "scope": "profile-server", "deviceClass": "web", "values": map[string]any{"privacy.pauseWatchHistory": true, "playback.startedThresholdPercent": 99}}))
	effective, _ = data["effective"].(map[string]any)
	if effective["privacy.pauseWatchHistory"] != true || effective["playback.startedThresholdPercent"] != float64(25) {
		t.Fatalf("patch did not apply: %+v", effective)
	}
	clamped, _ := data["clampedFields"].([]any)
	if len(clamped) != 1 || clamped[0] != "playback.startedThresholdPercent" {
		t.Fatalf("clamp not reported: %+v", clamped)
	}
	if effective["notifications.badges"] != true {
		t.Fatal("a partial patch cleared an untouched field")
	}
	// A stale expectedRevision conflicts rather than overwriting.
	if w = f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 1, "idempotencyKey": "three", "scope": "profile-server", "deviceClass": "web", "values": map[string]any{"music.gapless": false}}); w.Code != 409 {
		t.Fatalf("stale revision accepted: %d %s", w.Code, w.Body.String())
	}
	// A device-class field cannot be written into the profile document.
	if w = f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 2, "idempotencyKey": "four", "scope": "profile-server", "deviceClass": "web", "values": map[string]any{"appearance.cardSizePercent": 120}}); w.Code != 400 || !strings.Contains(w.Body.String(), "values.appearance.cardSizePercent") {
		t.Fatalf("scope violation accepted: %d %s", w.Code, w.Body.String())
	}
	data = f.data(f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 1, "idempotencyKey": "five", "scope": "profile-device-class", "deviceClass": "web", "values": map[string]any{"appearance.cardSizePercent": 120}}))
	effective, _ = data["effective"].(map[string]any)
	if effective["appearance.cardSizePercent"] != float64(120) {
		t.Fatalf("device scope did not apply: %+v", effective)
	}
}

func TestSearchHistoryRecordsAndRespectsThePreference(t *testing.T) {
	f := preferenceHTTP(t)
	// Type-ahead requests are never remembered; only a committed search (record=1) is.
	if w := f.call("GET", "/v1/search?q=Harb&group=movies", nil); w.Code != 200 {
		t.Fatalf("search failed: %d %s", w.Code, w.Body.String())
	}
	if w := f.call("GET", "/v1/search?q=Harbor&group=movies&record=1", nil); w.Code != 200 {
		t.Fatalf("search failed: %d %s", w.Code, w.Body.String())
	}
	w := f.call("GET", "/v1/search/history", nil)
	if w.Code != 200 {
		t.Fatalf("history failed: %d %s", w.Code, w.Body.String())
	}
	var page catalog.SearchHistoryPage
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if !page.Remembered || len(page.Entries) != 1 || page.Entries[0].Query != "Harbor" {
		t.Fatalf("search was not recorded: %+v", page)
	}
	f.data(f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 1, "idempotencyKey": "forget", "scope": "profile-server", "deviceClass": "web", "values": map[string]any{"search.rememberHistory": false}}))
	if w = f.call("GET", "/v1/search?q=Second&group=movies", nil); w.Code != 200 {
		t.Fatalf("search failed: %d %s", w.Code, w.Body.String())
	}
	w = f.call("GET", "/v1/search/history", nil)
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if page.Remembered || len(page.Entries) != 0 {
		t.Fatalf("history returned while remembering is off: %+v", page)
	}
	if w = f.call("DELETE", "/v1/search/history", nil); w.Code != 200 {
		t.Fatalf("clear failed: %d %s", w.Code, w.Body.String())
	}
	f.data(f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 2, "idempotencyKey": "remember", "scope": "profile-server", "deviceClass": "web", "values": map[string]any{"search.rememberHistory": nil}}))
	w = f.call("GET", "/v1/search/history", nil)
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if !page.Remembered || len(page.Entries) != 0 {
		t.Fatalf("cleared history came back: %+v", page)
	}
}
