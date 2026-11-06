//go:build linux || darwin

package httpapi

import (
	"context"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestFiniteHLSUnknownArtifactHTTPIsHiddenNotFound(t *testing.T) {
	f := newMediaEndFixture(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := decodertest.QualifiedFFmpeg(t)
	probe := decodertest.QualifiedFFprobe(t)
	path := filepath.Join(f.root, "finite.mkv")
	if out, err := exec.CommandContext(ctx, binary, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "13", "-c:v", "libx264", "-preset", "ultrafast", "-g", "48", "-pix_fmt", "yuv420p", "-c:a", "aac", path).CombinedOutput(); err != nil {
		t.Fatalf("finite fixture: %v %s", err, out)
	}
	facts, err := (assets.Probe{}).Inspect(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(f.db)
	var library string
	if err := f.db.QueryRow(`SELECT id FROM libraries LIMIT 1`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	if err := seedHTTPMovie(ctx, f.db, library, path, facts); err != nil {
		t.Fatal(err)
	}
	settleCompactCatalogue(t, f.db)
	items, _, err := cat.List(catalog.Viewer{Profile: f.owner.Viewer.ProfileID, Libraries: []string{library}}, library, "", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("finite catalogue: %v %+v", err, items)
	}
	hls, err := playback.NewHLS(ctx, f.db, filepath.Join(f.root, "finite-hls"), binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := hls.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := hls.EnableFinite(probe); err != nil {
		t.Fatal(err)
	}
	f.player.ConfigureHLS(hls)
	session := f.session(items[0].ID, "finite-unknown-artifact-http")
	if err := f.player.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	var profile string
	if err := f.db.QueryRow(`SELECT profile FROM playback_delivery_plans WHERE session_id=?`, session.ID).Scan(&profile); err != nil || !strings.HasPrefix(profile, "finite_") {
		t.Fatalf("not finite: %s %v", profile, err)
	}
	if w := f.get(session.StreamURL); w.Code != 200 {
		t.Fatalf("declared finite manifest: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"segment-invalid.ts", "audio-0.m3u8", "text-99.m3u8", "video.m3u8", "init.mp4", "segment-000000.m4s", "segment-000099.ts", "segment-999999.ts", "%2e%2e%2fmaster.m3u8"} {
		w := f.get(segmentURL(session, name))
		if w.Code != 404 || v1Code(w) != "not_found" || w.Header().Get("Retry-After") != "" || strings.Contains(w.Body.String(), `"retryable":true`) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// A request without this session's grant never reaches plan membership or
	// preparation, regardless of whether the name would be declared elsewhere.
	var hidden *httptest.ResponseRecorder
	for _, name := range []string{"master.m3u8", "segment-000000.ts", "audio-0.m3u8", "%2e%2e%2fmaster.m3u8"} {
		w := f.get("/v1/media/unknown-grant/" + name)
		if hidden == nil {
			hidden = w
		}
		if w.Code != 404 || w.Body.String() != hidden.Body.String() || w.Header().Get("Retry-After") != "" {
			t.Errorf("unknown grant %s: %d %s", name, w.Code, w.Body.String())
		}
	}
}
