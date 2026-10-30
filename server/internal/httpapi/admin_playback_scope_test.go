package httpapi

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestPlaybackCredentialCannotAdministerOtherMembers(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"playback-scope","name":"Player","scopes":["read","playback"]}`)
	for _, request := range []struct{ method, path, body string }{
		{"GET", "/v1/admin/sessions", ""},
		{"GET", "/v1/admin/sessions/unknown/controls", ""},
		{"POST", "/v1/admin/sessions/unknown/controls", `{"action":"pause","expectedRevision":"1"}`},
		{"POST", "/v1/admin/sessions/unknown:terminate", "{}"},
	} {
		w := f.call(request.method, request.path, key.Secret, request.body)
		if w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
			t.Fatalf("%s %s: %d %s", request.method, request.path, w.Code, w.Body)
		}
	}
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	_, answer := f.mcp(t, key.Secret, list)
	names := toolNames(answer)
	if !names["control_playback"] {
		t.Fatal("own playback tool disappeared")
	}
	for _, name := range []string{"now_playing", "stop_playback", "control_member_playback"} {
		if names[name] {
			t.Fatalf("playback-only key offered %s", name)
		}
		_, refused := f.mcp(t, key.Secret, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":{"sessionId":"unknown","action":"pause"}}}`)
		if refused.Error == nil || refused.Error.Code != -32602 {
			t.Fatalf("MCP admitted %s: %+v", name, refused)
		}
	}
	admin := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"admin-playback-scope","name":"Administrator","scopes":["admin"]}`)
	_, answer = f.mcp(t, admin.Secret, list)
	for _, name := range []string{"control_playback", "now_playing", "stop_playback", "control_member_playback"} {
		if !toolNames(answer)[name] {
			t.Fatalf("admin key missing %s", name)
		}
	}
}

func TestReadCredentialDoesNotReceiveOwnerEvents(t *testing.T) {
	f := administrationFixtureNew(t)
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"events-read","name":"Reader","scopes":["read"]}`)
	admin := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"events-admin","name":"Administrator","scopes":["admin"]}`)
	starts := map[string]string{}
	for _, token := range []string{read.Secret, admin.Secret, f.owner} {
		starts[token] = eventStart(t, f.call("GET", "/v1/events?waitMs=0", token, ""))
	}
	if _, err := f.deps.DB.Exec(`INSERT INTO api_events(audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES('admin','operation.updated','operation','private-owner-operation','1','{"private":true}',1),('server','server.changed','server','public-change','1','{}',1)`); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{read.Secret, admin.Secret, f.owner} {
		w := f.call("GET", "/v1/events?after="+starts[token]+"&waitMs=0", token, "")
		if w.Code != 200 {
			t.Fatalf("events: %d %s", w.Code, w.Body)
		}
		body := w.Body.String()
		if !strings.Contains(body, "public-change") {
			t.Fatal("server event disappeared")
		}
		hasPrivate := strings.Contains(body, "private-owner-operation")
		if hasPrivate != (token != read.Secret) {
			t.Fatalf("owner events for credential: %s", body)
		}
	}
}

func TestListHintsReachOwnerAndAdministratorsOnly(t *testing.T) {
	f := administrationFixtureNew(t)
	starts := map[string]string{}
	for _, token := range []string{f.owner, f.admin, f.member} {
		starts[token] = eventStart(t, f.call("GET", "/v1/events?waitMs=0", token, ""))
	}
	issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"list-hint","name":"Hinted","scopes":["read"]}`)
	if err := dbwork.WithWriteTx(context.Background(), f.deps.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := apievents.Flush(context.Background(), tx, 64)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, token string
		hears       bool
	}{{"owner", f.owner, true}, {"administrator", f.admin, true}, {"member", f.member, false}} {
		w := f.call("GET", "/v1/events?after="+starts[c.token]+"&waitMs=0", c.token, "")
		if w.Code != 200 {
			t.Fatalf("%s events: %d %s", c.name, w.Code, w.Body)
		}
		body := w.Body.String()
		heard := strings.Contains(body, `"type":"list.changed"`) && strings.Contains(body, `"id":"apiKeys"`)
		if heard != c.hears {
			t.Fatalf("%s heard the API keys list hint: %v (%s)", c.name, heard, body)
		}
	}
}
