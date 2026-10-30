package httpapi

import (
	"net/url"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// NEW-28 (demo, 24 Sep): after B8 a web play is a v1 session (ps_…), and the
// player then reads offers, the subtitle plan and chapters with that id. Those
// routes looked the id up only among media sessions, so every read answered
// 404 (offers, chapters) or 401 (subtitles), the player lost its audio and
// quality options, and the 401 sent the client into a token refresh.
//
// This runs the web client's exact sequence against a v1 session: start →
// offers?sessionId → subtitle plan → chapters → events → stop. Every read
// answers 200 and names the v1 session and presentation generation the client
// holds; a renewed token of the same profile still reaches it; another profile
// gets a hidden 404, never a 401.
func TestPlaybackV1ClientReadsWithTheV1SessionID(t *testing.T) {
	f, _, _, _ := v1SubtitleFixture(t)
	item := f.items[0]
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "new28-sequence-00001"}, startBody(item, nil), 201, &s)
	if s.ID == "" || s.Presentation.Generation < 1 {
		t.Fatalf("start: %+v", s)
	}

	reads := func(token string) {
		t.Helper()
		var offers playback.Offers
		f.callAs(token, "GET", "/v1/items/"+item+"/playback-offers?sessionId="+url.QueryEscape(s.ID), nil, nil, 200, &offers)
		if offers.Current == nil || offers.Current.SessionID != s.ID || offers.Current.Generation != s.Presentation.Generation {
			t.Fatalf("offers name the wrong session: %+v (want %s gen %d)", offers.Current, s.ID, s.Presentation.Generation)
		}
		if offers.SubtitlePlan != nil && (offers.SubtitlePlan.SessionID != s.ID || offers.SubtitlePlan.Generation != s.Presentation.Generation) {
			t.Fatalf("offers subtitle plan names the wrong session: %s gen %d", offers.SubtitlePlan.SessionID, offers.SubtitlePlan.Generation)
		}
		var plan subtitles.PlaybackPlan
		f.callAs(token, "GET", "/v1/items/"+item+"/playback/"+s.ID+"/subtitles", nil, nil, 200, &plan)
		if plan.SessionID != s.ID || plan.Generation != s.Presentation.Generation {
			t.Fatalf("subtitle plan names %s gen %d, want %s gen %d", plan.SessionID, plan.Generation, s.ID, s.Presentation.Generation)
		}
		var chapters playback.ChapterProjection
		f.callAs(token, "GET", "/v1/playback/sessions/"+s.ID+"/chapters?limit=100", nil, nil, 200, &chapters)
		if chapters.Scope.SessionID != s.ID || chapters.Scope.Generation != s.Presentation.Generation {
			t.Fatalf("chapters name %s gen %d", chapters.Scope.SessionID, chapters.Scope.Generation)
		}
		var page struct {
			NextAfter string `json:"nextAfter"`
		}
		f.callAs(token, "GET", "/v1/events?waitMs=0", nil, nil, 200, &page)
	}
	reads(f.owner.AccessToken)

	// A renewed or second credential of the same profile still reaches the
	// presentation (the media session's binding follows the caller).
	second := f.device("new28-second-device")
	reads(second.AccessToken)
	reads(f.owner.AccessToken)

	// Another profile: hidden, and never 401 for a valid session.
	member, _ := f.member()
	for _, path := range []string{
		"/v1/items/" + item + "/playback-offers?sessionId=" + url.QueryEscape(s.ID),
		"/v1/items/" + item + "/playback/" + s.ID + "/subtitles",
		"/v1/playback/sessions/" + s.ID + "/chapters?limit=100",
	} {
		w := f.raw("GET", path, member, nil, nil)
		if w.Code != 404 {
			t.Errorf("another profile reading %s: %d %s", path, w.Code, w.Body.String())
		}
	}

	// A subtitle selection is a change of the session: it fences on the
	// session revision the client holds, and the plan still names the v1
	// session afterwards. There is no separate selection route.
	stale := map[string]any{"subtitles": map[string]any{"trackId": nil}}
	if w := f.raw("PATCH", "/v1/playback/sessions/"+s.ID, f.owner.AccessToken, map[string]string{"If-Match": `"` + s.Revision + `9"`}, stale); w.Code != 412 {
		t.Fatalf("a stale revision must be refused: %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("PUT", "/v1/items/"+item+"/playback/"+s.ID+"/subtitles", f.owner.AccessToken, nil, map[string]any{}); w.Code != 404 && w.Code != 405 {
		t.Fatalf("the retired subtitle selection route still answers: %d %s", w.Code, w.Body.String())
	}
	w := f.raw("PATCH", "/v1/playback/sessions/"+s.ID, f.owner.AccessToken, map[string]string{"If-Match": `"` + s.Revision + `"`}, stale)
	if w.Code != 200 {
		t.Fatalf("subtitles off on the v1 session: %d %s", w.Code, w.Body.String())
	}
	var selected subtitles.PlaybackPlan
	f.call("GET", "/v1/items/"+item+"/playback/"+s.ID+"/subtitles", nil, nil, 200, &selected)
	if selected.SessionID != s.ID || selected.Mode != "off" {
		t.Fatalf("selection names %s mode %s", selected.SessionID, selected.Mode)
	}

	// Stop ends it; the reads then answer a hidden 404 (not 401, not 503).
	f.call("DELETE", "/v1/playback/sessions/"+s.ID, nil, nil, 0, nil)
	for _, path := range []string{
		"/v1/items/" + item + "/playback-offers?sessionId=" + url.QueryEscape(s.ID),
		"/v1/items/" + item + "/playback/" + s.ID + "/subtitles",
		"/v1/playback/sessions/" + s.ID + "/chapters?limit=100",
	} {
		if w := f.raw("GET", path, f.owner.AccessToken, nil, nil); w.Code != 404 {
			t.Errorf("after stop, %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
