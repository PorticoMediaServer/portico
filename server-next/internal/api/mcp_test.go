package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func rpc(t *testing.T, s *Server, key, method string, params any) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	if method == "notifications/initialized" {
		delete(body, "id")
	}
	var out map[string]any
	w := call(t, s, "POST", "/v1/mcp", key, body, &out)
	return w.Code, out
}

func toolCall(t *testing.T, s *Server, key, tool string, args map[string]any) (text string, failed bool) {
	t.Helper()
	code, out := rpc(t, s, key, "tools/call", map[string]any{"name": tool, "arguments": args})
	result, _ := out["result"].(map[string]any)
	if code != 200 || result == nil {
		t.Fatalf("calling %s answered %d: %v", tool, code, out)
	}
	content := result["content"].([]any)[0].(map[string]any)
	return content["text"].(string), result["isError"] == true
}

// An assistant gets only the tools to look, see what is playing and mark watched (and, with an
// administrator key, read the server's status), whatever the key may do elsewhere; each acts
// as the key, within what its profile may see. "Assistants" off closes the address.
func TestAssistant(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	var admin KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: "Assistant", Scopes: []string{"admin"}}, &admin)
	if code, _ := rpc(t, s, admin.Secret, "initialize", map[string]any{}); code != 404 {
		t.Fatalf("with Assistants off the address answered %d, want 404", code)
	}
	on, off := true, false
	status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{Assistants: &on})
	if code, out := rpc(t, s, admin.Secret, "initialize", map[string]any{"protocolVersion": "2025-06-18"}); code != 200 || out["result"] == nil {
		t.Fatalf("initialize answered %d: %v", code, out)
	}
	if code, _ := rpc(t, s, admin.Secret, "notifications/initialized", nil); code != 202 {
		t.Errorf("a notification answered %d, want 202", code)
	}
	_, list := rpc(t, s, admin.Secret, "tools/list", nil)
	var names []string
	for _, tool := range list["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	want := []string{"get_title", "list_children", "list_libraries", "list_titles", "mark_watched", "now_playing", "server_status"}
	if !slices.Equal(names, want) {
		t.Errorf("an administrator key's assistant has tools %v, want only %v", names, want)
	}
	if code, _ := rpc(t, s, owner, "tools/list", nil); code != 403 {
		t.Errorf("a signed-in app at the assistant address answered %d, want 403", code)
	}

	max := addMember(t, s, owner, "max", "member")
	shows := []string{th.libs["shows"].PublicID}
	status(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner, MemberEdit{AllLibraries: &off, Libraries: &shows})
	maxSession := signInAs(t, s, "max", testPassword, 6).AccessToken
	var reader, player KeyCreated
	call(t, s, "POST", "/v1/me/tokens", maxSession, KeyCreate{Name: "Look", Scopes: []string{"read"}}, &reader)
	call(t, s, "POST", "/v1/me/tokens", maxSession, KeyCreate{Name: "Mark", Scopes: []string{"read", "playback"}}, &player)

	if text, failed := toolCall(t, s, player.Secret, "get_title", map[string]any{"id": th.film}); !failed {
		t.Errorf("the assistant read a film the member may not see: %s", text)
	}
	if _, failed := toolCall(t, s, reader.Secret, "mark_watched", map[string]any{"id": th.eps[0], "watched": true}); !failed {
		t.Error("a read-only key's assistant marked a title watched")
	}
	if text, failed := toolCall(t, s, player.Secret, "mark_watched", map[string]any{"id": th.eps[0], "watched": true}); failed {
		t.Fatalf("marking watched failed: %s", text)
	}
	text, _ := toolCall(t, s, player.Secret, "get_title", map[string]any{"id": th.eps[0]})
	var title map[string]any
	json.Unmarshal([]byte(text), &title)
	if title["watched"] != true {
		t.Errorf("after mark_watched the title reads %s", text)
	}
	if text, _ := toolCall(t, s, player.Secret, "list_libraries", nil); strings.Contains(text, th.libs["movies"].PublicID) {
		t.Errorf("the assistant listed a library the member may not see: %s", text)
	}

	status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{Assistants: &off})
	if code, _ := rpc(t, s, player.Secret, "tools/list", nil); code != 404 {
		t.Errorf("with Assistants turned off the address answered %d, want 404", code)
	}
}

// An assistant whose key acts as a limited profile is told the account has a stream, not the
// title that profile may not see; and a tool call the server did not carry out is reported as
// failed, never as done.
func TestAssistantSeesOnlyWhatItsProfileMay(t *testing.T) {
	th := newTheatre(t)
	on := true
	status(t, th.s, "PUT", "/v1/admin/settings/integrations", th.owner.AccessToken, IntegrationSettingsEdit{Assistants: &on})
	key := kidsKey(t, th)
	text, failed := toolCall(t, th.s, key, "now_playing", nil)
	if failed || !strings.Contains(text, "playing") || strings.Contains(text, "Heat") || strings.Contains(text, th.film) {
		t.Errorf("a limited profile's assistant was told %s", text)
	}
	var player KeyCreated
	call(t, th.s, "POST", "/v1/me/tokens", th.owner.AccessToken, KeyCreate{Name: "Mark", Scopes: []string{"read", "playback"}}, &player)
	if text, failed := toolCall(t, th.s, player.Secret, "mark_watched", map[string]any{"id": "..", "watched": true}); !failed {
		t.Errorf("a call the router redirected was reported as done: %s", text)
	}
}
