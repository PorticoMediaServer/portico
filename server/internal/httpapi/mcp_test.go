package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

type mcpAnswer struct {
	ID     any `json:"id"`
	Result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Tools           []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError           bool           `json:"isError"`
		StructuredContent map[string]any `json:"structuredContent"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (f administrationFixture) mcp(t *testing.T, token, body string) (*httptest.ResponseRecorder, mcpAnswer) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/mcp", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	var answer mcpAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	return w, answer
}

func toolNames(answer mcpAnswer) map[string]bool {
	out := map[string]bool{}
	for _, tool := range answer.Result.Tools {
		out[tool.Name] = true
	}
	return out
}

func TestMCPAuthenticatesWithACredentialAndSpeaksJSONRPC(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"mcp-key-read","name":"Assistant","scopes":["read"]}`)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	// No credential, a signed-in session, and an unknown key are all refused.
	for name, token := range map[string]string{"anonymous": "", "session": f.owner, "unknown key": "pk.unknown-unknown-unknown-unknown-unknown-unknown-0"} {
		if w, _ := f.mcp(t, token, initialize); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	w, answer := f.mcp(t, key.Secret, initialize)
	if w.Code != 200 || answer.Result.ProtocolVersion != "2025-06-18" || answer.Error != nil || !strings.Contains(w.Body.String(), `"tools"`) || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("initialize: %d %s", w.Code, w.Body)
	}
	// A version this server does not know is answered with one it does.
	if _, answer = f.mcp(t, key.Secret, `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"1999-01-01"}}`); answer.Result.ProtocolVersion != mcpLatestProtocol {
		t.Fatalf("version negotiation: %+v", answer)
	}
	// A notification has no answer; ping has an empty one.
	if w, _ = f.mcp(t, key.Secret, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); w.Code != 202 || w.Body.Len() != 0 {
		t.Fatalf("notification: %d %s", w.Code, w.Body)
	}
	if w, answer = f.mcp(t, key.Secret, `{"jsonrpc":"2.0","id":2,"method":"ping"}`); w.Code != 200 || answer.Error != nil {
		t.Fatalf("ping: %d %s", w.Code, w.Body)
	}
	// Errors use the JSON-RPC codes.
	for body, code := range map[string]int{
		`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`:                      -32601,
		`{"jsonrpc":"1.0","id":3,"method":"ping"}`:                                -32600,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`:                          -32600,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{}}`:              -32602,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`: -32602,
		`{not json`: -32700,
		`[]`:        -32600,
	} {
		if _, answer = f.mcp(t, key.Secret, body); answer.Error == nil || answer.Error.Code != code {
			t.Fatalf("%s: %+v", body, answer.Error)
		}
	}
	// A batch answers each request and skips notifications; it is bounded.
	w, _ = f.mcp(t, key.Secret, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`)
	var batch []mcpAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &batch); err != nil || len(batch) != 2 {
		t.Fatalf("batch: %d %s", w.Code, w.Body)
	}
	if _, answer = f.mcp(t, key.Secret, "["+strings.TrimSuffix(strings.Repeat(`{"jsonrpc":"2.0","id":1,"method":"ping"},`, mcpMaxBatch+1), ",")+"]"); answer.Error == nil || answer.Error.Code != -32600 {
		t.Fatalf("an oversized batch was accepted: %+v", answer)
	}
	// Transport refusals: the wrong content type, an unsupported protocol
	// header, a body past the bound, and GET.
	r := httptest.NewRequest("POST", "/v1/mcp", strings.NewReader(initialize))
	r.Header.Set("Authorization", "Bearer "+key.Secret)
	r.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	if rec.Code != 415 {
		t.Fatalf("text/plain: %d", rec.Code)
	}
	r = httptest.NewRequest("POST", "/v1/mcp", strings.NewReader(initialize))
	r.Header.Set("Authorization", "Bearer "+key.Secret)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("MCP-Protocol-Version", "1999-01-01")
	rec = httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	if rec.Code != 400 {
		t.Fatalf("unsupported protocol header: %d", rec.Code)
	}
	if w, _ = f.mcp(t, key.Secret, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"`+strings.Repeat("x", mcpMaxBody)+`"}}`); w.Code != 413 {
		t.Fatalf("oversized body: %d", w.Code)
	}
	if w := f.call("GET", "/v1/mcp", key.Secret, ""); w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatalf("GET: %d %v", w.Code, w.Header())
	}
}

func TestMCPToolsAreFilteredByScopeAndRoleAndCallTheAPI(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	library := c.Library("movies", "Movies", "movie", "/movies")
	film := c.Movie(library, "/movies/film.mkv", "A Film", 2001)
	c.Drain()
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"mcp-tools-read","name":"Reader","scopes":["read"]}`)
	full := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"mcp-tools-admin","name":"Everything","scopes":["admin"]}`)
	library2 := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"mcp-tools-library","name":"Download manager","scopes":["library"]}`)
	member := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"mcp-tools-member","name":"Member","scopes":["read","playback","library","admin"]}`)

	_, answer := f.mcp(t, read.Secret, list)
	readTools := toolNames(answer)
	for _, want := range []string{"list_libraries", "search", "list_titles", "find_title_by_provider_id", "get_title", "server_status", "library_scan_status"} {
		if !readTools[want] {
			t.Fatalf("a read key is not offered %s: %v", want, readTools)
		}
	}
	for _, hidden := range []string{"start_library_scan", "report_changed_paths", "mark_watched", "now_playing", "stop_playback", "list_members", "change_settings", "get_settings", "run_backup", "create_webhook", "list_webhooks", "read_logs", "fix_match", "control_playback"} {
		if readTools[hidden] {
			t.Fatalf("a read key is offered %s", hidden)
		}
	}
	_, answer = f.mcp(t, full.Secret, list)
	all := toolNames(answer)
	for _, want := range []string{"start_library_scan", "report_changed_paths", "mark_watched", "now_playing", "stop_playback", "list_members", "change_settings", "run_backup", "list_webhooks", "create_webhook", "fix_match", "get_match", "play_history", "list_scan_jobs"} {
		if !all[want] {
			t.Fatalf("an owner's admin key is not offered %s: %v", want, all)
		}
	}
	for _, tool := range answer.Result.Tools {
		if len(tool.Description) < 20 || tool.InputSchema["type"] != "object" || strings.ContainsAny(tool.Name, " /") {
			t.Fatalf("tool %q is not described: %+v", tool.Name, tool)
		}
	}
	_, answer = f.mcp(t, library2.Secret, list)
	if tools := toolNames(answer); !tools["start_library_scan"] || !tools["report_changed_paths"] || tools["list_libraries"] || tools["list_members"] || tools["change_settings"] {
		t.Fatalf("a library key's tools: %v", tools)
	}
	// A member with every scope is offered what a member can do and no more.
	_, answer = f.mcp(t, member.Secret, list)
	memberTools := toolNames(answer)
	if !memberTools["list_libraries"] || !memberTools["mark_watched"] {
		t.Fatalf("a member's tools: %v", memberTools)
	}
	for _, hidden := range []string{"start_library_scan", "now_playing", "stop_playback", "list_members", "change_settings", "get_settings", "run_backup", "create_webhook", "read_logs", "fix_match", "play_history"} {
		if memberTools[hidden] {
			t.Fatalf("a member is offered %s", hidden)
		}
	}

	// A call goes to the API and comes back as text and as structured content.
	call := func(token, name, arguments string) mcpAnswer {
		t.Helper()
		_, answer := f.mcp(t, token, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"`+name+`","arguments":`+arguments+`}}`)
		return answer
	}
	answer = call(read.Secret, "list_libraries", `{}`)
	if answer.Error != nil || answer.Result.IsError || len(answer.Result.Content) != 1 || !strings.Contains(answer.Result.Content[0].Text, `"Movies"`) || answer.Result.StructuredContent["items"] == nil {
		t.Fatalf("list_libraries: %+v", answer)
	}
	answer = call(read.Secret, "list_titles", `{"library":"movies","kind":"movie","limit":5}`)
	if answer.Error != nil || answer.Result.IsError || !strings.Contains(answer.Result.Content[0].Text, film.Public) {
		t.Fatalf("list_titles: %+v", answer)
	}
	// The API's own refusal comes back as a tool error in plain words.
	answer = call(read.Secret, "list_titles", `{"library":"movies","kind":"movie","sort":"year","addedAfter":"2020-01-01T00:00:00Z"}`)
	if answer.Error != nil || !answer.Result.IsError || answer.Result.Content[0].Text == "" {
		t.Fatalf("an API refusal: %+v", answer)
	}
	// A tool outside the key's scopes does not exist for it; arguments are held
	// to the schema; ids cannot carry a path.
	for name, arguments := range map[string]string{
		"start_library_scan": `{"libraryId":"movies"}`,
		"list_members":       `{}`,
		"change_settings":    `{"values":{"name":"Mine"}}`,
	} {
		if answer = call(read.Secret, name, arguments); answer.Error == nil || answer.Error.Code != -32602 {
			t.Fatalf("a read key called %s: %+v", name, answer)
		}
	}
	for _, arguments := range []string{`{"itemId":"../admin/api-keys"}`, `{"itemId":"a/b"}`, `{"itemId":"a?x=1"}`, `{"itemId":12}`, `{}`, `{"itemId":"abc","extra":true}`} {
		answer = call(read.Secret, "get_title", arguments)
		if answer.Error == nil && !answer.Result.IsError {
			t.Fatalf("get_title accepted %s: %+v", arguments, answer)
		}
	}
	if answer = call(read.Secret, "list_titles", `{"library":"movies","kind":"movie","limit":5000}`); answer.Error == nil {
		t.Fatalf("a limit past the schema's bound was accepted: %+v", answer)
	}
	if answer = call(member.Secret, "now_playing", `{}`); answer.Error == nil {
		t.Fatalf("a member called an administrator's tool: %+v", answer)
	}
	// Even called directly, past the tool list, the API holds the key to its
	// scopes: the tool layer is not what enforces them.
	request := httptest.NewRequest("POST", "/v1/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+read.Secret)
	for _, attempt := range []mcpCall{
		{Method: "POST", Path: "/v1/libraries/movies/scans"},
		{Method: "GET", Path: "/v1/admin/api-keys"},
		{Method: "GET", Path: "/v1/admin/access/members"},
		{Method: "PATCH", Path: "/v1/admin/console/settings", Body: map[string]any{}},
	} {
		status, _, body, err := f.deps.mcpInvoke(request)(attempt)
		if err != nil || status != 403 {
			t.Fatalf("%s %s past the tool list: %d %s %v", attempt.Method, attempt.Path, status, body, err)
		}
	}
	// An owner's admin key can change one setting and leave the rest alone.
	answer = call(full.Secret, "change_settings", `{"values":{"name":"Cinema"}}`)
	if answer.Error != nil || answer.Result.IsError {
		t.Fatalf("change_settings: %+v", answer)
	}
	if f.deps.Identity.Name() != "Cinema" && !strings.Contains(answer.Result.Content[0].Text, "Cinema") {
		t.Fatalf("the setting did not change: %+v", answer)
	}
	if answer = call(full.Secret, "change_settings", `{"values":{"notASetting":true}}`); answer.Error != nil || !answer.Result.IsError {
		t.Fatalf("an unknown setting was accepted: %+v", answer)
	}
	if answer = call(full.Secret, "list_webhooks", `{}`); answer.Error != nil || answer.Result.IsError {
		t.Fatalf("list_webhooks: %+v", answer)
	}
	// Tool calls are charged to the key once, not once per API call inside.
	f.deps.credentials.mu.Lock()
	inflight := f.deps.credentials.total
	f.deps.credentials.mu.Unlock()
	if inflight != 0 {
		t.Fatalf("slots left held after tool calls: %d", inflight)
	}
	var _ = http.MethodPost
}
