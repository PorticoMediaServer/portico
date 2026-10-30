package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"strings"
	"testing"
)

func TestPlaybackExecutionDiagnosticsAreAdminOnly(t *testing.T) {
	f := newV1Fixture(t, 1)
	member, film := f.member()
	var session playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "execution-private-0001"}, startBody(film, nil), 201, &session)
	var media string
	if err := f.db.QueryRow(`SELECT media_session_id FROM playback_v1_sessions WHERE id=?`, session.ID).Scan(&media); err != nil {
		t.Fatal(err)
	}
	raw := `{"stages":[{"operation":"encode","execution":"hardware"}],"execution":{"launchCount":1,"softwareLaunchCount":0,"unknownLaunchCount":0,"fallbackCount":0,"lastLaunch":{"sequence":1,"generation":1,"startedAt":"2026-10-28T00:00:00Z","encoder":{"name":"h264_vaapi","execution":"hardware"},"decoder":{"name":"h264_vaapi","execution":"hardware"},"renderNode":"/dev/dri/renderD128"}}}`
	if _, err := f.db.Exec(`INSERT INTO playback_hls_stages VALUES(?,?,?)`, media, "vaapi", raw); err != nil {
		t.Fatal(err)
	}
	var page playbackv1.AdminSessionPage
	owner := f.call("GET", "/v1/admin/sessions", nil, nil, 200, &page)
	if !strings.Contains(owner.Body.String(), `"execution":{`) || !strings.Contains(owner.Body.String(), "renderD128") {
		t.Fatal("admin execution missing")
	}
	ordinary := f.callAs(member, "GET", "/v1/playback/sessions/"+session.ID, nil, nil, 200, nil)
	for _, secret := range []string{`"launchCount"`, `"lastLaunch"`, "renderD128", "h264_vaapi"} {
		if strings.Contains(ordinary.Body.String(), secret) {
			t.Fatal("viewer received execution diagnostic")
		}
	}
	refused := f.raw("GET", "/v1/admin/sessions", member, nil, nil)
	if refused.Code != 403 {
		t.Fatalf("ordinary viewer admin response %d", refused.Code)
	}
}
