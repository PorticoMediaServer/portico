package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

func TestTunerDiscoveryHonoursTheSettingAndOwnerAccess(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	if code := status(t, s, "POST", "/v1/admin/live-sources/discover", admin, LiveTunerSearch{}); code != 403 {
		t.Fatal(code)
	}
	if _, err := s.settings.Write(context.Background(), 1, "live", json.RawMessage(`{"discoveryEnabled":false}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	var out live.LiveTunerDiscovery
	w := call(t, s, "POST", "/v1/admin/live-sources/discover", owner, LiveTunerSearch{}, &out)
	if w.Code != 200 || out.Enabled || len(out.Devices) != 0 || out.Message == "" {
		t.Fatal(w.Code, out)
	}
}
