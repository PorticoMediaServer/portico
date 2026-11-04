package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func TestUnknownHLSArtifactWithValidGrantIsHiddenNotFound(t *testing.T) {
	f := replanFixtureV1(t, 1)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "hls")
	hls, err := playback.NewHLS(context.Background(), f.db, root, binary)
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Playback.ConfigureHLS(hls)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := hls.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "unknown-artifact-start"}, startBody(f.items[0], map[string]any{"quality": map[string]any{"mode": "fixed", "id": "720p-high"}}), 201, &session)
	if session.Presentation.Mode != "stream" {
		t.Fatalf("fixture did not plan HLS: %+v", session.Presentation)
	}
	base := strings.TrimSuffix(session.Presentation.URL, "/master.m3u8")
	// A real live grant reaches the artifact validator. These names need no encoder.
	for _, name := range []string{"segment-invalid.ts", "segment-1.ts", "init-0.mp4", "audio-100-000000.ts"} {
		w := f.raw("GET", base+"/"+name, "", nil, nil)
		var body struct {
			Error struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 404 || body.Error.Code != "not_found" || body.Error.Retryable || w.Header().Get("Retry-After") != "" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// A declared artifact absent from ready output keeps the distinct retryable
	// response. This uses a temporary registered HLS output, without an encoder.
	grant := strings.TrimPrefix(base, "/v1/media/")
	var id string
	var generation int
	if err := f.db.QueryRow(`SELECT id,generation FROM playback_sessions WHERE grant_hash=?`, identity.Digest(grant)).Scan(&id, &generation); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generation"), []byte(strconv.Itoa(generation)), 0600); err != nil {
		t.Fatal(err)
	}
	path := base + "/master.m3u8"
	w := f.raw("GET", path, "", nil, nil)
	if w.Code != 503 || v1Code(w) != "stream_not_ready" || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("declared artifact not ready: %d %s", w.Code, w.Body.String())
	}
	const segment = "fixture HLS segment bytes"
	if err := os.WriteFile(filepath.Join(dir, "segment-000000.ts"), []byte(segment), 0600); err != nil {
		t.Fatal(err)
	}
	w = f.raw("GET", base+"/segment-000000.ts", "", nil, nil)
	if w.Code != 200 || w.Body.String() != segment {
		t.Fatalf("ready declared artifact: %d %s", w.Code, w.Body.String())
	}

}

func TestHLSArtifactAbsenceDoesNotHidePreparingOrConverterFailure(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{playback.ErrHLSArtifactNotFound, 404, "not_found"},
		{playback.ErrSegmentPreparing, 503, "segment_preparing"},
		{errors.New("converter process failed"), 500, "internal"},
	} {
		w := httptest.NewRecorder()
		failure(w, tc.err)
		var body struct {
			Error struct {
				Code string `json:"code"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || body.Error.Code != tc.code {
			t.Errorf("%v: %d %s", tc.err, w.Code, w.Body.String())
		}
	}
}
