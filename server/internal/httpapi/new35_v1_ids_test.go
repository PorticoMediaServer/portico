package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// NEW-35: the lyrics routes, route-failure reports and analysis reads take the
// v1 session id a v1 player holds, answer in the v1 id and presentation
// generation, and hide another profile's or an unknown session (404, never a
// 401 that would send a signed-in client to refresh its token).
func TestNew35V1SessionIDsReachLyricsRouteFailuresAndAnalysis(t *testing.T) {
	f := musicFixture(t, 1) // lyrics are a song's
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := mediaanalysis.New(mediaanalysis.Options{DB: f.db, Directory: filepath.Join(root, "analysis"),
		Open: func(context.Context, string, string) (mediaanalysis.Input, error) { return nil, sql.ErrNoRows },
		Decoder: func(mediaanalysis.Input) mediaanalysis.Decode {
			return func(context.Context, string, decoder.AnalysisSpec, func(io.Reader) error) error { return sql.ErrNoRows }
		}})
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Lyrics = &lyrics.Service{DB: f.db}
	f.deps.Analysis = analysis
	f.handler = New(f.deps)
	item := f.items[0]
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "new35-v1-ids-000001"}, startBody(item, nil), 201, &s)
	gen := s.Presentation.Generation

	// Lyrics: the v1 id reaches the presentation's source, answered in v1 terms.
	var view lyrics.View
	f.call("GET", "/v1/items/"+item+"/lyrics?sessionId="+url.QueryEscape(s.ID), nil, nil, 200, &view)
	if view.Scope.SessionID != s.ID || view.Scope.SessionGeneration != int64(gen) {
		t.Fatalf("lyrics scope %s gen %d, want %s gen %d", view.Scope.SessionID, view.Scope.SessionGeneration, s.ID, gen)
	}

	// Analysis: the v1 id with its v1 generation answers exactly what the media
	// session's own id and generation answer (this fixture has no inventory,
	// so both reach the same "no source" outcome past the session check), and
	// a stale v1 generation conflicts before anything else.
	var media string
	var mediaGen int
	if err := f.db.QueryRow(`SELECT v.media_session_id,s.generation FROM playback_v1_sessions v JOIN playback_sessions s ON s.id=v.media_session_id WHERE v.id=?`, s.ID).Scan(&media, &mediaGen); err != nil {
		t.Fatal(err)
	}
	viaMedia := f.raw("GET", "/v1/items/"+item+"/analysis?sessionId="+url.QueryEscape(media)+"&generation="+strconv.Itoa(mediaGen), f.owner.AccessToken, nil, nil)
	viaV1 := f.raw("GET", "/v1/items/"+item+"/analysis?sessionId="+url.QueryEscape(s.ID)+"&generation="+strconv.Itoa(gen), f.owner.AccessToken, nil, nil)
	if viaV1.Code != viaMedia.Code {
		t.Fatalf("analysis via the v1 id answered %d, via the media id %d: %s", viaV1.Code, viaMedia.Code, viaV1.Body.String())
	}
	if viaV1.Code == 200 {
		var analysed mediaanalysis.View
		if err := json.Unmarshal(viaV1.Body.Bytes(), &analysed); err != nil || analysed.Scope.SessionID != s.ID || analysed.Scope.SessionGeneration != int64(gen) {
			t.Fatalf("analysis scope %+v (%v)", analysed.Scope, err)
		}
	}
	if w := f.raw("GET", "/v1/items/"+item+"/analysis?sessionId="+url.QueryEscape(s.ID)+"&generation="+strconv.Itoa(gen+7), f.owner.AccessToken, nil, nil); w.Code != 409 {
		t.Fatalf("a stale v1 generation must conflict: %d %s", w.Code, w.Body.String())
	}

	// Another profile never reaches this profile's session through any of them.
	member, _ := f.member()
	for _, probe := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/items/" + item + "/lyrics?sessionId=" + url.QueryEscape(s.ID), nil},
		{"PATCH", "/v1/playback/sessions/" + s.ID, map[string]any{"failure": map[string]any{"code": "decode_error"}}},
	} {
		if w := f.raw(probe.method, probe.path, member, nil, probe.body); w.Code != 404 {
			t.Errorf("another profile %s %s: %d %s", probe.method, probe.path, w.Code, w.Body.String())
		}
	}
}
