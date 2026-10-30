package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/webhooks"
)

func TestWebhooksAreTheOwnersAndNeedTheAdminScope(t *testing.T) {
	f := administrationFixtureNew(t)
	body := `{"name":"Home automation","url":"http://192.168.1.20:8123/api/webhook/abc","events":["playback.started","playback.stopped"]}`
	for name, token := range map[string]string{"anonymous": "", "member": f.member, "administrator": f.admin} {
		w := f.call("POST", "/v1/admin/webhooks", token, body)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("%s made a webhook: %d %s", name, w.Code, w.Body)
		}
		if w = f.call("GET", "/v1/admin/webhooks", token, ""); w.Code != 401 && w.Code != 403 {
			t.Fatalf("%s listed webhooks: %d %s", name, w.Code, w.Body)
		}
	}
	w := f.call("POST", "/v1/admin/webhooks", f.owner, body)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var made webhooks.Issued
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil || made.Secret == "" || made.Webhook.ID == "" {
		t.Fatalf("created: %v %s", err, w.Body)
	}
	w = f.call("GET", "/v1/admin/webhooks", f.owner, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), made.Secret) || !strings.Contains(w.Body.String(), `"secretHint"`) || !strings.Contains(w.Body.String(), `"member.signed_in"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	// Keys: only the admin scope manages webhooks, and only an owner's key.
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"hook-key-read","name":"Reader","scopes":["read","playback","library"]}`)
	if w = f.call("GET", "/v1/admin/webhooks", read.Secret, ""); w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
		t.Fatalf("a key without the admin scope listed webhooks: %d %s", w.Code, w.Body)
	}
	admin := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"hook-key-admin","name":"Manager","scopes":["admin"]}`)
	if w = f.call("GET", "/v1/admin/webhooks", admin.Secret, ""); w.Code != 200 {
		t.Fatalf("an admin key could not list webhooks: %d %s", w.Code, w.Body)
	}
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"hook-token-admin","name":"Member","scopes":["admin"]}`)
	if w = f.call("GET", "/v1/admin/webhooks", token.Secret, ""); w.Code != 403 {
		t.Fatalf("a member's token listed webhooks: %d %s", w.Code, w.Body)
	}
	// Edits name the revision they were made against: none is 428, an old one
	// 412 with the webhook as it is now; one webhook reads by id.
	if w = f.call("PATCH", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, `{"enabled":false}`); w.Code != 428 {
		t.Fatalf("an edit with no revision: %d %s", w.Code, w.Body)
	}
	if w = f.call("PATCH", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, `{"expectedRevision":99,"enabled":false}`); w.Code != 412 || !strings.Contains(w.Body.String(), `"current":`) {
		t.Fatalf("an edit against an old revision: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, ""); w.Code != 200 || !strings.Contains(w.Body.String(), fmt.Sprintf(`"revision":%d`, made.Webhook.Revision)) {
		t.Fatalf("read one: %d %s", w.Code, w.Body)
	}
	// Edits are partial; bad values and unknown fields are refused.
	w = f.call("PATCH", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, fmt.Sprintf(`{"expectedRevision":%d,"enabled":false}`, made.Webhook.Revision))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) || !strings.Contains(w.Body.String(), `"name":"Home automation"`) || strings.Contains(w.Body.String(), `"secret":`) {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{"expectedRevision":2,"url":"ftp://example.com"}`, `{"expectedRevision":2,"events":[]}`, `{"expectedRevision":2,"events":["webhook.test"]}`, `{"expectedRevision":2,"secret":"short"}`, `{"expectedRevision":2,"owner":"me"}`} {
		if w = f.call("PATCH", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, bad); w.Code != 400 {
			t.Fatalf("%s accepted: %d %s", bad, w.Code, w.Body)
		}
	}
	w = f.call("PATCH", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, `{"expectedRevision":2,"rotateSecret":true}`)
	var rotated webhooks.Issued
	if err := json.Unmarshal(w.Body.Bytes(), &rotated); err != nil || w.Code != 200 || rotated.Secret == "" || rotated.Secret == made.Secret {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	if w = f.call("POST", "/v1/admin/webhooks/"+made.Webhook.ID+"/test", f.owner, ""); w.Code != 202 || !strings.Contains(w.Body.String(), `"event":"webhook.test"`) {
		t.Fatalf("send test: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/admin/webhooks/"+made.Webhook.ID+"/deliveries?limit=10", f.owner, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"pending"`) {
		t.Fatalf("log: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/admin/webhooks/"+made.Webhook.ID+"/deliveries?limit=5000", f.owner, ""); w.Code != 400 {
		t.Fatalf("unbounded log page: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/admin/webhooks/missing/deliveries", f.owner, ""); w.Code != 404 {
		t.Fatalf("log of a missing webhook: %d %s", w.Code, w.Body)
	}
	if w = f.call("DELETE", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w = f.call("DELETE", "/v1/admin/webhooks/"+made.Webhook.ID, f.owner, ""); w.Code != 404 {
		t.Fatalf("second delete: %d %s", w.Code, w.Body)
	}
	// A sign-in reaches a subscribed webhook's queue; making a key does not.
	w = f.call("POST", "/v1/admin/webhooks", f.owner, `{"name":"Sign-ins","url":"http://127.0.0.1:9/hook","events":["member.signed_in"]}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"hook-key-quiet","name":"Quiet","scopes":["read"]}`)
	var queued int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM webhook_deliveries WHERE type='member.signed_in'`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("making a key was reported as a sign-in: %d %v", queued, err)
	}
	if _, err := f.deps.Identity.Issue("member", "member-profile", "local", "member", 1); err != nil {
		t.Fatal(err)
	}
	var data string
	if err := f.deps.DB.QueryRow(`SELECT data FROM webhook_deliveries WHERE type='member.signed_in'`).Scan(&data); err != nil || !strings.Contains(data, `"user":"member"`) {
		t.Fatalf("a sign-in was not queued: %q %v", data, err)
	}
	for _, secret := range []string{"token", "password", "secret"} {
		if strings.Contains(strings.ToLower(data), secret) {
			t.Fatalf("a sign-in payload mentions %q: %s", secret, data)
		}
	}
}
