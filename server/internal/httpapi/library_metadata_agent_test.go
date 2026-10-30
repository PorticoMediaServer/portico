package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/playback"

	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// A library can be created local-only, and its metadata source is read and
// changed through /v1/libraries/{id}/metadata/agent under a revision.
func TestLibraryMetadataAgentRoutes(t *testing.T) {
	f := newV1Fixture(t, 0)
	host, err := hosted.New(f.db, f.id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.handler = New(Dependencies{DB: f.db, Identity: f.id, Catalog: f.cat, Ingestion: ingestion.New(f.db, f.cat, assets.Probe{}), Playback: playback.New(f.db), Hosted: host, PlaybackV1: f.v1, Metadata: metadata.New(f.db, "")})
	var created struct {
		ID string `json:"id"`
	}
	f.call("POST", "/v1/libraries", nil, map[string]string{"name": "Home videos", "kind": "movie", "path": t.TempDir(), "metadataAgent": "local"}, 201, &created)
	var agent metadata.LibraryAgent
	f.call("GET", "/v1/libraries/"+created.ID+"/metadata/agent", nil, nil, 200, &agent)
	if agent.Agent != "local" || len(agent.Agents) != 2 || agent.Agents[0].ID != "online" || agent.Agents[1].Name != "Local metadata only" {
		t.Fatalf("created agent %+v", agent)
	}
	if w := f.raw("PUT", "/v1/libraries/"+created.ID+"/metadata/agent", f.owner.AccessToken, nil, map[string]any{"expectedRevision": agent.Revision + 1, "agent": "online"}); w.Code != 409 || v1Code(w) != "metadata_conflict" {
		t.Fatalf("stale agent change %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("PUT", "/v1/libraries/"+created.ID+"/metadata/agent", f.owner.AccessToken, nil, map[string]any{"expectedRevision": agent.Revision, "agent": "elsewhere"}); w.Code != 400 || v1Code(w) != "invalid_metadata_source" {
		t.Fatalf("unknown agent %d %s", w.Code, w.Body.String())
	}
	w := f.call("PUT", "/v1/libraries/"+created.ID+"/metadata/agent", nil, map[string]any{"expectedRevision": agent.Revision, "agent": "online"}, 200, &agent)
	if agent.Agent != "online" {
		t.Fatalf("changed agent %s", w.Body.String())
	}
	var screen map[string]any
	f.call("GET", "/v1/libraries/"+created.ID+"/metadata/screen", nil, nil, 200, &screen)
	if screen["agent"] != "online" || screen["enabled"] != true {
		raw, _ := json.Marshal(screen)
		t.Fatalf("screen policy after switching online: %s", raw)
	}
	if w := f.raw("POST", "/v1/libraries", f.owner.AccessToken, nil, map[string]string{"name": "Bad", "kind": "movie", "path": t.TempDir(), "metadataAgent": "elsewhere"}); w.Code != 400 {
		t.Fatalf("unknown agent at create %d %s", w.Code, w.Body.String())
	}
}

func agentTestFixture(t *testing.T) *v1Fixture {
	t.Helper()
	f := newV1Fixture(t, 0)
	host, err := hosted.New(f.db, f.id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.handler = New(Dependencies{DB: f.db, Identity: f.id, Catalog: f.cat, Ingestion: ingestion.New(f.db, f.cat, assets.Probe{}), Playback: playback.New(f.db), Hosted: host, PlaybackV1: f.v1, Metadata: metadata.New(f.db, "")})
	return f
}

var wantScreenLanguages = []string{"en", "ja", "fr", "de", "es", "it", "pt", "nl", "sv", "no", "da", "fi", "pl", "cs", "sk", "hu", "ro", "el", "tr", "ru", "uk", "ar", "he", "hi", "th", "vi", "ko", "zh"}

// The pre-create listing publishes each agent's languages: online first with
// the 28 codes in order and defaultLanguage en, local with an empty list and
// no defaultLanguage. Music offers no language choice. Unknown kinds are 400
// and members get the same refusal as the other owner routes.
func TestLibraryKindMetadataAgentsListLanguages(t *testing.T) {
	f := agentTestFixture(t)
	var got struct {
		LibraryKind string                        `json:"libraryKind"`
		Agents      []metadata.LibraryAgentOption `json:"agents"`
	}
	f.call("GET", "/v1/library-kinds/movie/metadata-agents", nil, nil, 200, &got)
	if got.LibraryKind != "movie" || len(got.Agents) != 2 || got.Agents[0].ID != "online" || got.Agents[1].ID != "local" {
		t.Fatalf("movie agents %+v", got)
	}
	if len(got.Agents[0].Languages) != len(wantScreenLanguages) {
		t.Fatalf("movie online languages %v", got.Agents[0].Languages)
	}
	for i, want := range wantScreenLanguages {
		if got.Agents[0].Languages[i] != want {
			t.Fatalf("movie online languages %v", got.Agents[0].Languages)
		}
	}
	if got.Agents[0].DefaultLanguage != "en" {
		t.Fatalf("movie online defaultLanguage %q", got.Agents[0].DefaultLanguage)
	}
	if len(got.Agents[1].Languages) != 0 || got.Agents[1].DefaultLanguage != "" {
		t.Fatalf("movie local languages %+v", got.Agents[1])
	}
	// languages is always present (an empty array, never null) and
	// defaultLanguage is present exactly when languages is non-empty.
	var raw struct {
		Agents []map[string]any `json:"agents"`
	}
	f.call("GET", "/v1/library-kinds/movie/metadata-agents", nil, nil, 200, &raw)
	online, local := raw.Agents[0], raw.Agents[1]
	if langs, ok := online["languages"].([]any); !ok || len(langs) != len(wantScreenLanguages) {
		t.Fatalf("movie online raw languages %v", online["languages"])
	}
	if online["defaultLanguage"] != "en" {
		t.Fatalf("movie online raw defaultLanguage %v", online["defaultLanguage"])
	}
	if langs, ok := local["languages"].([]any); !ok || len(langs) != 0 {
		t.Fatalf("movie local raw languages missing or not empty: %v", local)
	}
	if _, ok := local["defaultLanguage"]; ok {
		t.Fatalf("movie local raw defaultLanguage present: %v", local)
	}
	var music struct {
		Agents []metadata.LibraryAgentOption `json:"agents"`
	}
	f.call("GET", "/v1/library-kinds/music/metadata-agents", nil, nil, 200, &music)
	for _, agent := range music.Agents {
		if len(agent.Languages) != 0 || agent.DefaultLanguage != "" {
			t.Fatalf("music agent %+v offers a language", agent)
		}
	}
	if w := f.raw("GET", "/v1/library-kinds/novel/metadata-agents", f.owner.AccessToken, nil, nil); w.Code != 400 || v1Code(w) != "invalid_request" {
		t.Fatalf("unknown kind %d %s", w.Code, w.Body.String())
	}
	member, _ := f.member()
	ownerRefusal := f.raw("GET", "/v1/libraries/"+f.library+"/metadata/agent", member, nil, nil)
	candidate := f.raw("GET", "/v1/library-kinds/movie/metadata-agents", member, nil, nil)
	if candidate.Code != ownerRefusal.Code || v1Code(candidate) != v1Code(ownerRefusal) {
		t.Fatalf("member refusal %d %s, want the same as the agent route %d %s", candidate.Code, candidate.Body.String(), ownerRefusal.Code, ownerRefusal.Body.String())
	}
}

// POST /v1/libraries accepts a listed metadata language and records it on
// both the screen policy and the TMDB provider policy.
func TestCreateLibraryWithMetadataLanguage(t *testing.T) {
	f := agentTestFixture(t)
	var created struct {
		ID string `json:"id"`
	}
	f.call("POST", "/v1/libraries", nil, map[string]string{"name": "Films", "kind": "movie", "path": t.TempDir(), "metadataLanguage": "ja"}, 201, &created)
	var agent metadata.LibraryAgent
	f.call("GET", "/v1/libraries/"+created.ID+"/metadata/agent", nil, nil, 200, &agent)
	if agent.Language != "ja" {
		t.Fatalf("agent language %q", agent.Language)
	}
	var screenLang, tmdbLang string
	if err := f.db.QueryRow(`SELECT language FROM screen_metadata_policies WHERE library_id=?`, created.ID).Scan(&screenLang); err != nil || screenLang != "ja" {
		t.Fatalf("screen policy language %q %v", screenLang, err)
	}
	if err := f.db.QueryRow(`SELECT language FROM metadata_provider_policies WHERE library_id=? AND provider='tmdb'`, created.ID).Scan(&tmdbLang); err != nil || tmdbLang != "ja" {
		t.Fatalf("tmdb policy language %q %v", tmdbLang, err)
	}
}

// An unoffered language is rejected with invalid_metadata_language and leaves
// no library behind.
func TestCreateLibraryRejectsUnofferedLanguage(t *testing.T) {
	f := agentTestFixture(t)
	count := func() int {
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM libraries`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, body := range []map[string]string{
		{"name": "Bad code", "kind": "movie", "path": t.TempDir(), "metadataLanguage": "xx"},
		{"name": "Regional", "kind": "movie", "path": t.TempDir(), "metadataLanguage": "en-US"},
		{"name": "Local lang", "kind": "movie", "path": t.TempDir(), "metadataAgent": "local", "metadataLanguage": "ja"},
		{"name": "Music lang", "kind": "music", "path": t.TempDir(), "metadataLanguage": "ja"},
	} {
		before := count()
		w := f.raw("POST", "/v1/libraries", f.owner.AccessToken, nil, body)
		if w.Code != 400 || v1Code(w) != "invalid_metadata_language" {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body.String())
		}
		if after := count(); after != before {
			t.Fatalf("%v: library row left behind (%d -> %d)", body, before, after)
		}
	}
}

// A library created without a language reports the stored default as is.
func TestLibraryAgentReportsStoredLanguage(t *testing.T) {
	f := agentTestFixture(t)
	var created struct {
		ID string `json:"id"`
	}
	f.call("POST", "/v1/libraries", nil, map[string]string{"name": "Plain", "kind": "movie", "path": t.TempDir()}, 201, &created)
	var stored string
	if err := f.db.QueryRow(`SELECT language FROM screen_metadata_policies WHERE library_id=?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var agent metadata.LibraryAgent
	f.call("GET", "/v1/libraries/"+created.ID+"/metadata/agent", nil, nil, 200, &agent)
	if agent.Language != stored {
		t.Fatalf("agent language %q, stored %q", agent.Language, stored)
	}
	if !strings.HasPrefix(stored, "en") || stored == "" {
		t.Fatalf("unexpected stored default %q", stored)
	}
}
