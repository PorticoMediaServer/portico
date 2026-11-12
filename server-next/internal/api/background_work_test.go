package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

func TestOwnerCanPauseBackgroundWorkForAnHour(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s.setClock(func() time.Time { return now })
	var state BackgroundWorkState
	if w := call(t, s, "PUT", "/v1/admin/background/pause", owner, nil, &state); w.Code != 200 || state.PausedUntil == nil || !state.PausedUntil.Equal(now.Add(time.Hour)) {
		t.Fatalf("pause: %d %s", w.Code, w.Body)
	}
	reopened, _, err := settings.Open(t.Context(), s.db)
	if err != nil || !reopened.BackgroundPausedUntil(now).IsZero() {
		t.Fatalf("a restart kept the pause: %v", err)
	}
	if got := get[BackgroundWorkState](t, s, owner, "/v1/admin/background/pause"); got.PausedUntil == nil {
		t.Fatal("the pause is absent from its status")
	}
	if code := status(t, s, "DELETE", "/v1/admin/background/pause", owner, nil); code != 200 {
		t.Fatalf("resume answered %d", code)
	}
	if got := get[BackgroundWorkState](t, s, owner, "/v1/admin/background/pause"); got.PausedUntil != nil {
		t.Fatal("resume left the pause in force")
	}
	status(t, s, "PUT", "/v1/admin/background/pause", owner, nil)
	s.setClock(func() time.Time { return now.Add(time.Hour) })
	if got := get[BackgroundWorkState](t, s, owner, "/v1/admin/background/pause"); got.PausedUntil != nil {
		t.Fatal("the pause outlasted its hour")
	}
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	if code := status(t, s, "PUT", "/v1/admin/background/pause", admin, nil); code != 403 {
		t.Fatalf("an administrator paused work: %d", code)
	}
	if code := status(t, s, "PUT", "/v1/admin/settings/general", owner, json.RawMessage(`{"backgroundSpeed":"gentle"}`)); code != 200 {
		t.Fatalf("background speed answered %d", code)
	}
	if settings.General.Get(s.settings).BackgroundSpeed != "gentle" {
		t.Fatal("the chosen speed was not saved")
	}
}
