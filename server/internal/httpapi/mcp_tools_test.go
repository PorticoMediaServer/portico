package httpapi

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Every tool is wired: an owner's key is offered each one whose feature is set
// up, its schema names what its routes need, and a call reaches the route it
// declares (an answer from the API, never a refusal of the arguments).
func TestMCPEveryToolIsOfferedAndDescribed(t *testing.T) {
	f := administrationFixtureNew(t)
	full := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"mcp-every-tool","name":"Everything","scopes":["admin"]}`)
	_, answer := f.mcp(t, full.Secret, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	offered := toolNames(answer)
	// Live TV is not set up in this fixture, so its guide is not served.
	notSetUp := map[string]bool{"tv_guide": true}
	placeholder := regexp.MustCompile(`\{([a-zA-Z]+)\}`)
	seen := map[string]bool{}
	for _, tool := range mcpTools() {
		if seen[tool.Name] {
			t.Fatalf("two tools are called %s", tool.Name)
		}
		seen[tool.Name] = true
		if offered[tool.Name] == notSetUp[tool.Name] {
			t.Fatalf("%s: offered %v to an owner's admin key", tool.Name, offered[tool.Name])
		}
		if len(tool.Routes) == 0 || tool.Run == nil {
			t.Fatalf("%s calls nothing", tool.Name)
		}
		for _, name := range tool.Required {
			if _, ok := tool.Properties[name]; !ok {
				t.Fatalf("%s requires %q but does not describe it", tool.Name, name)
			}
		}
		for _, route := range tool.Routes {
			if !strings.HasPrefix(route.Path, "/v1/") {
				t.Fatalf("%s calls %s outside the API", tool.Name, route.Path)
			}
			if len(placeholder.FindAllString(route.Path, -1)) > 0 && len(tool.Required) == 0 {
				t.Fatalf("%s needs an id for %s but requires none", tool.Name, route.Path)
			}
		}
	}
	if len(offered) != len(mcpTools())-len(notSetUp) {
		t.Fatalf("offered %d tools of %d", len(offered), len(mcpTools()))
	}
}

func TestMCPEveryToolReachesItsRoute(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "Shows", "tv", "/tv")
	film := c.Movie(movies, "/movies/film.mkv", "A Film", 2001)
	show := c.Show(tv, "A Show", 2010)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/A Show/Season 1/e1.mkv")
	c.Drain()
	full := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"mcp-every-route","name":"Everything","scopes":["admin"]}`)
	ids := strings.NewReplacer("FILM", film.Public, "SHOW", show.Public, "SEASON", season.Public, "EPISODE", episode.Public)
	call := func(name, arguments string) (mcpAnswer, string) {
		t.Helper()
		_, answer := f.mcp(t, full.Secret, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"`+name+`","arguments":`+ids.Replace(arguments)+`}}`)
		if answer.Error != nil {
			t.Fatalf("%s %s: the call was refused: %+v", name, arguments, answer.Error)
		}
		if len(answer.Result.Content) != 1 || answer.Result.Content[0].Text == "" {
			t.Fatalf("%s: no answer: %+v", name, answer)
		}
		return answer, answer.Result.Content[0].Text
	}
	ok := func(name, arguments string, contains ...string) string {
		t.Helper()
		answer, text := call(name, arguments)
		if answer.Result.IsError {
			t.Fatalf("%s %s: %s", name, arguments, text)
		}
		for _, want := range contains {
			if !strings.Contains(text, ids.Replace(want)) {
				t.Fatalf("%s: %q not in %s", name, want, text)
			}
		}
		return text
	}
	// The API answered, and refused in plain words: the object does not exist,
	// or the service behind it is not part of this fixture.
	refused := func(name, arguments, because string) {
		t.Helper()
		answer, text := call(name, arguments)
		if !answer.Result.IsError || !strings.Contains(text, because) {
			t.Fatalf("%s %s: %v %s", name, arguments, answer.Result.IsError, text)
		}
	}

	// Looking at what is here.
	ok("list_libraries", `{}`, `"Movies"`, `"Shows"`)
	ok("search", `{"q":"Film"}`)
	ok("list_titles", `{"library":"movies","kind":"movie"}`, "FILM")
	ok("find_title_by_provider_id", `{"provider":"tmdb","providerId":"603"}`, `"items":[]`)
	title := ok("get_title", `{"itemId":"FILM"}`, "A Film", `"languages"`)
	if strings.Contains(title, `"files"`) || strings.Contains(title, "/movies/film.mkv") {
		t.Fatal("get_title must use the page projection without lazy file information", title)
	}
	ok("list_seasons", `{"showId":"SHOW"}`, "SEASON")
	ok("list_episodes", `{"seasonId":"SEASON"}`, "EPISODE")
	ok("mark_watched", `{"itemIds":["FILM"],"watched":true}`)
	ok("list_notifications", `{}`)
	// Scans and attention.
	ok("library_scan_status", `{"libraryId":"movies"}`, `"libraryId":"movies"`)
	ok("list_scan_jobs", `{}`)
	ok("list_unmatched_files", `{"libraryId":"tv"}`)
	for name, arguments := range map[string]string{
		"list_needs_attention": `{}`, "library_needs_attention": `{"libraryId":"movies"}`, "count_needs_attention": `{}`,
		"get_match": `{"kind":"item","id":"FILM"}`, "search_match_candidates": `{"kind":"item","id":"FILM","query":"A Film"}`,
		"fix_match": `{"kind":"item","id":"FILM","candidateId":"candidate","provider":"tmdb"}`,
	} {
		refused(name, arguments, "Metadata is temporarily unavailable")
	}
	// Playback and people.
	ok("now_playing", `{}`, `"items":[]`)
	refused("stop_playback", `{"sessionId":"nosuch"}`, "not available")
	refused("control_playback", `{"sessionId":"nosuch","action":"pause"}`, "not available")
	refused("control_member_playback", `{"sessionId":"nosuch","action":"seek","positionMs":1000}`, "not available")
	ok("list_members", `{}`, `"username":"member"`)
	ok("play_history", `{"period":"7d","timeZone":"America/Halifax","until":"2026-10-22T12:00:00Z"}`)
	// The server.
	ok("server_status", `{}`, `"ready":true`)
	refused("server_health", `{"panel":"storage"}`, "could not be completed")
	ok("storage_usage", `{}`, `"categories"`)
	ok("read_logs", `{"limit":5}`)
	ok("get_settings", `{}`)
	refused("list_backups", `{}`, "temporarily unavailable")
	refused("run_backup", `{}`, "temporarily unavailable")
	// Recordings.
	refused("list_recordings", `{}`, "Recordings are temporarily unavailable")
	refused("schedule_recording", `{"sourceId":"source","channelId":"channel","generation":"1","programmeId":"programme"}`, "Recordings are temporarily unavailable")
	refused("cancel_recording", `{"recordingId":"nosuch"}`, "Recordings are temporarily unavailable")

	// A webhook from start to end, through the tools alone.
	ok("list_webhooks", `{}`, `"title.added"`)
	created := ok("create_webhook", `{"name":"Hook","url":"https://example.com/hook","events":["title.added"]}`, `"secretHint"`)
	var hook struct {
		Webhook struct {
			ID string `json:"id"`
		} `json:"webhook"`
	}
	if json.Unmarshal([]byte(created), &hook) != nil || hook.Webhook.ID == "" {
		t.Fatalf("create_webhook: %s", created)
	}
	id := `"webhookId":"` + hook.Webhook.ID + `"`
	ok("update_webhook", `{`+id+`,"name":"Renamed","events":["title.added","title.removed"]}`, `"Renamed"`, `"title.removed"`)
	ok("test_webhook", `{`+id+`}`)
	ok("list_webhook_deliveries", `{`+id+`}`)
	ok("delete_webhook", `{`+id+`}`)
	refused("update_webhook", `{`+id+`,"name":"Again"}`, "not available")
	refused("list_webhook_deliveries", `{`+id+`}`, "not available")
}
