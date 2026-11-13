package api

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOwnerAPIKeyHistoryKeepsEndedKeysWithoutSecrets(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var live, revoked, expires KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Live", Scopes: []string{"read"}}, &live)
	call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Revoked", Scopes: []string{"library"}}, &revoked)
	until := s.now().Add(2 * time.Minute)
	call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Expired", Scopes: []string{"playback"}, ExpiresAt: &until}, &expires)
	if status(t, s, "DELETE", "/v1/admin/api-keys/"+revoked.Key.ID, owner.AccessToken, nil) != 204 {
		t.Fatal("revoke failed")
	}
	at := until.Add(time.Minute)
	s.setClock(func() time.Time { return at })
	var page Page[Key]
	next := "/v1/admin/api-keys?limit=1"
	seen := map[string]string{}
	for next != "" {
		page = Page[Key]{}
		w := call(t, s, "GET", next, owner.AccessToken, nil, &page)
		if w.Code != 200 || len(page.Items) != 1 {
			t.Fatalf("history page: %d, %d items", w.Code, len(page.Items))
		}
		for _, secret := range []string{live.Secret, revoked.Secret, expires.Secret} {
			if secret == "" || strings.Contains(w.Body.String(), secret) {
				t.Fatal("history disclosed a secret")
			}
		}
		row := page.Items[0]
		seen[row.ID] = row.State
		if row.State == "revoked" && row.RevokedAt == nil {
			t.Fatal("missing actual revocation time")
		}
		next = ""
		if page.NextCursor != "" {
			next = "/v1/admin/api-keys?limit=1&cursor=" + url.QueryEscape(page.NextCursor)
		}
	}
	if seen[live.Key.ID] != "active" || seen[revoked.Key.ID] != "revoked" || seen[expires.Key.ID] != "expired" || len(seen) != 3 {
		t.Fatalf("wrong history states: %v", seen)
	}
	if status(t, s, "GET", "/v1/admin/api-keys", live.Secret, nil) != 403 {
		t.Fatal("a key read the owner's credential history")
	}
}
