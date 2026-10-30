package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sampleEventManifest = "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:6.000000,\nsegment-000000.ts\n"

func TestSourceOriginManifestPreservesMediaAndRejectsAmbiguousStart(t *testing.T) {
	for _, raw := range []string{sampleEventManifest, sampleEventManifest + "#EXT-X-ENDLIST\n", strings.Replace(sampleEventManifest, "PLAYLIST-TYPE:EVENT", "PLAYLIST-TYPE:VOD", 1) + "#EXT-X-ENDLIST\n", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=500000,AUDIO=\"audio\"\nrendition-0.m3u8\n"} {
		got, err := sourceOriginManifest([]byte(raw), true)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(got, []byte(hlsStartAtSourceOrigin)) != 1 || strings.Replace(string(got), hlsStartAtSourceOrigin+"\n", "", 1) != raw {
			t.Fatal("start preference changed media references or timing")
		}
		repeated, err := sourceOriginManifest(got, true)
		if err != nil || !bytes.Equal(repeated, got) {
			t.Fatal("existing identical preference not preserved", err)
		}
	}
	child, err := sourceOriginManifest([]byte(sampleEventManifest), false)
	if err != nil || string(child) != sampleEventManifest {
		t.Fatal("child manifest changed", err)
	}
	for _, raw := range []string{
		"", "#EXTM3U\n", sampleEventManifest + "\x00", strings.Replace(sampleEventManifest, "PLAYLIST-TYPE:EVENT", "PLAYLIST-TYPE:VOD", 1),
		sampleEventManifest + "#EXT-X-START:TIME-OFFSET=12\n",
		sampleEventManifest + hlsStartAtSourceOrigin + "\n" + hlsStartAtSourceOrigin + "\n",
		sampleEventManifest + strings.Repeat("x", maxHLSManifestBytes),
	} {
		if _, err := sourceOriginManifest([]byte(raw), true); err == nil {
			t.Fatal("invalid root accepted")
		}
	}
}

func TestHLSManifestRepresentationRangeReloadAndGrantRevocation(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	player := playback.New(db)
	// No converter is started: this HTTP test publishes a fixed fixture under a
	// real authorized session. Actual encoded playback is qualified separately.
	hls, err := playback.NewHLS(context.Background(), db, filepath.Join(root, "hls"), "/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := hls.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	player.ConfigureHLS(hls)
	handler := New(Dependencies{DB: db, Identity: id, Catalog: cat, Ingestion: ingestion.New(db, cat, assets.Probe{}), Playback: player, Hosted: host})
	setupToken, err := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"authMode": "local", "setupToken": string(setupToken), "username": "owner", "password": "long-test-password", "name": "Test"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	setup := httptest.NewRequest("POST", "/v1/setup", bytes.NewReader(body))
	setup.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, setup)
	if w.Code != 201 {
		t.Fatal("setup", w.Code)
	}
	var auth identity.Envelope
	if err = json.Unmarshal(w.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	principal, err := id.Authenticate(auth.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := cat.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Film.mkv")
	if err = os.WriteFile(path, []byte("fixture source pin"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('job',?,'running',?);INSERT INTO scan_queue(job_id,path,kind) VALUES('job',?,'file')`, lib.ID, time.Now().Format(time.RFC3339), path); err != nil {
		t.Fatal(err)
	}
	if err = seedHTTPMovie(context.Background(), db, lib.ID, path, assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 30}); err != nil {
		t.Fatal(err)
	}
	settleCompactCatalogue(t, db)
	items, _, err := cat.List(catalog.Viewer{Profile: auth.Viewer.ProfileID, Libraries: []string{lib.ID}}, lib.ID, "", 10)
	if err != nil || len(items) != 1 {
		t.Fatal("catalog", err)
	}
	session, err := player.Create(principal, items[0].ID, "automatic", "hls-manifest-http")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "hls", session.ID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "generation"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "master.m3u8")
	if err = os.WriteFile(manifest, []byte(sampleEventManifest), 0600); err != nil {
		t.Fatal(err)
	}
	get := func(headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", session.StreamURL, nil)
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		return response
	}
	response := get(nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), hlsStartAtSourceOrigin) {
		t.Fatal("manifest", response.Code)
	}
	if response.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Last-Modified") != "" {
		t.Fatal("manifest headers", response.Header())
	}
	full := response.Body.String()
	if response.Header().Get("Content-Length") != strconv.Itoa(len(full)) {
		t.Fatal("representation length")
	}
	ranged := get(map[string]string{"Range": "bytes=8-31"})
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != full[8:32] {
		t.Fatal("transformed range", ranged.Code)
	}
	// Same-second replacement must not be hidden by a304 based on file mtime.
	grown := sampleEventManifest + "#EXTINF:6.000000,\nsegment-000001.ts\n"
	if err = os.WriteFile(manifest, []byte(grown), 0600); err != nil {
		t.Fatal(err)
	}
	reloaded := get(map[string]string{"If-Modified-Since": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)})
	if reloaded.Code != 200 || !strings.Contains(reloaded.Body.String(), "segment-000001.ts") {
		t.Fatal("fresh playlist reload", reloaded.Code)
	}
	t.Run("decoder symlink cannot be served", func(t *testing.T) {
		outside := filepath.Join(root, "private-stream")
		if err := os.WriteFile(outside, []byte("private decoder target"), 0600); err != nil {
			t.Fatal(err)
		}
		segment := filepath.Join(dir, "segment-000000.ts")
		if err := os.Symlink(outside, segment); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		defer os.Remove(segment)
		request := httptest.NewRequest("GET", strings.TrimSuffix(session.StreamURL, "master.m3u8")+"segment-000000.ts", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private decoder target") {
			t.Fatalf("decoder symlink response: %d %s", response.Code, response.Body.String())
		}
	})
	segment := filepath.Join(dir, "segment-000000.ts")
	if err := os.WriteFile(segment, []byte("ordinary segment"), 0600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", strings.TrimSuffix(session.StreamURL, "master.m3u8")+"segment-000000.ts", nil)
	served := httptest.NewRecorder()
	handler.ServeHTTP(served, request)
	if served.Code != http.StatusOK || served.Body.String() != "ordinary segment" {
		t.Fatalf("ordinary segment response: %d %s", served.Code, served.Body.String())
	}
	if err = player.Stop(principal, session.ID); err != nil {
		t.Fatal(err)
	}
	// NEW-35: an ended grant is a hidden 404 presentation_ended, never a 401.
	if denied := get(nil); denied.Code != http.StatusNotFound || !strings.Contains(denied.Body.String(), "presentation_ended") || strings.Contains(denied.Body.String(), "#EXTM3U") {
		t.Fatal("revoked manifest remained accessible", denied.Code)
	}
}

func TestGuardedManifestChecksActualReads(t *testing.T) {
	denied := false
	r := &guardedManifest{Reader: bytes.NewReader([]byte("protected manifest")), check: func() error {
		if denied {
			return identity.ErrUnauthorized
		}
		return nil
	}}
	buf := make([]byte, 4)
	if n, err := r.Read(buf); n != 4 || err != nil {
		t.Fatal(n, err)
	}
	denied = true
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if n, err := r.Read(buf); n != 0 || err != identity.ErrUnauthorized {
		t.Fatal("read after revocation", n, err)
	}
}
