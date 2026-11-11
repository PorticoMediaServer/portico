package api

import (
	"strings"
	"testing"
)

// The owner adds a webhook pointed at the home network and is shown its secret once; an
// address on this machine's loopback is refused, and a member cannot see the webhooks.
func TestWebhookRoutes(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var refused errorBody
	if w := call(t, s, "POST", "/v1/admin/webhooks", owner.AccessToken,
		WebhookCreate{Name: "Self", URL: "http://127.0.0.1:8096/v1/auth/sign-in", Events: []string{"server.started"}}, &refused); w.Code != 400 || refused.Error.Fields[0].Path != "url" {
		t.Errorf("a loopback webhook answered %d, want 400 naming the url", w.Code)
	}
	var made WebhookSecret
	if w := call(t, s, "POST", "/v1/admin/webhooks", owner.AccessToken,
		WebhookCreate{Name: "Home Assistant", URL: "http://192.168.1.20:8123/api/webhook/x", Events: []string{"playback.started"}}, &made); w.Code != 200 || !strings.HasPrefix(made.Secret, "whsec_") {
		t.Fatalf("adding a webhook answered %d with secret %q", w.Code, made.Secret)
	}
	w := call(t, s, "GET", "/v1/admin/webhooks", owner.AccessToken, nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), made.Secret) || !strings.Contains(w.Body.String(), made.Webhook.ID) {
		t.Errorf("listing webhooks answered %d: %s", w.Code, w.Body)
	}
	addMember(t, s, owner.AccessToken, "max", "member")
	if got := status(t, s, "GET", "/v1/admin/webhooks", signInAs(t, s, "max", testPassword, 6).AccessToken, nil); got != 403 {
		t.Errorf("a member listing webhooks answered %d, want 403", got)
	}

	// A key may look after webhooks but not set one up, point it elsewhere or replace its secret.
	var key KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Tool", Scopes: []string{"admin"}}, &key)
	elsewhere, renamed := "http://192.168.1.30/hook", "Renamed"
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"POST", "/v1/admin/webhooks", WebhookCreate{Name: "Mine", URL: "http://192.168.1.30/hook", Events: []string{"playback.started"}}, 403},
		{"PATCH", "/v1/admin/webhooks/" + made.Webhook.ID, WebhookEdit{URL: &elsewhere}, 403},
		{"POST", "/v1/admin/webhooks/" + made.Webhook.ID + "/secret", nil, 403},
		{"PATCH", "/v1/admin/webhooks/" + made.Webhook.ID, WebhookEdit{Name: &renamed}, 200},
		{"GET", "/v1/admin/webhooks/" + made.Webhook.ID + "/deliveries", nil, 200},
		{"DELETE", "/v1/admin/webhooks/" + made.Webhook.ID, nil, 204},
	} {
		if got := status(t, s, c.method, c.path, key.Secret, c.body); got != c.want {
			t.Errorf("%s %s with a key answered %d, want %d", c.method, c.path, got, c.want)
		}
	}
}
