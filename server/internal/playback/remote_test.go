package playback

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestRealSTRMApprovedLANProbeRangeAndRevocation(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	media := filepath.Join(root, "remote.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", "-y", media)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture %s %v", out, e)
	}
	var originRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		if r.URL.Path == "/media/redirect.mp4" {
			http.Redirect(w, r, "/media/movie.mp4?credential=private", 302)
			return
		}
		if r.URL.Path != "/media/movie.mp4" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials leaked to upstream")
		}
		f, e := os.Open(media)
		if e != nil {
			t.Error(e)
			return
		}
		defer f.Close()
		info, _ := f.Stat()
		http.ServeContent(w, r, "movie.mp4", info.ModTime(), f)
	}))
	defer server.Close()
	descriptor := filepath.Join(root, "Movie (2026).strm")
	if e := os.WriteFile(descriptor, []byte(server.URL+"/media/redirect.mp4?credential=private\n"), 0600); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(descriptor)
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'00','profile')`)
	if e != nil {
		t.Fatal(e)
	}
	_, item, remoteAsset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: descriptor, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "strm"})
	hash := identity.Digest("access")
	insertFixtureSession(t, db, hash, "owner", "profile", "owner", time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	store := storage.New(binary)
	remote := NewRemote(db, store, assets.Probe{Supervisor: store.Supervisor})
	s := New(db)
	s.ConfigureRemote(remote)
	p := identity.Principal{Hash: hash, Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	if _, e = s.CreateContext(ctx, p, item, "automatic", "denied"); e == nil {
		t.Fatal("unapproved LAN descriptor played")
	}
	if _, e = s.ApproveNetworkRoots(ctx, "lib", []string{server.URL + "/media/"}); e != nil {
		t.Fatal(e)
	}
	session, e := s.CreateContext(ctx, p, item, "automatic", "allowed")
	if e != nil {
		t.Fatal(e)
	}
	if session.Mode != "direct" || session.Duration < 1 || strings.Contains(session.StreamURL, "private") || strings.Contains(session.StreamURL, server.URL) {
		t.Fatalf("unsafe or unprobed session %+v", session)
	}
	grant := strings.TrimPrefix(session.StreamURL, "/v1/media/")
	response, close, yes, e := s.OpenRemote(ctx, grant, "GET", "bytes=0-15", false)
	if e != nil || !yes {
		t.Fatalf("range %v %v", yes, e)
	}
	raw, e := io.ReadAll(response.Body)
	close()
	original, e2 := os.ReadFile(media)
	if e != nil || e2 != nil || response.StatusCode != 206 || !bytes.Equal(raw, original[:16]) {
		t.Fatalf("remote range %d %q %v", response.StatusCode, raw, e)
	}
	again, e := s.CreateContext(ctx, p, item, "automatic", "allowed")
	if e != nil || again.ID != session.ID {
		t.Fatal("STRM receipt retry failed", e)
	}
	// A named STRM alternate of a locally preferred version must prepare and
	// play that alternate, rather than silently preparing the default file.
	mediaInfo, e := os.Stat(media)
	if e != nil {
		t.Fatal(e)
	}
	_, localItem, localAsset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: media, Size: mediaInfo.Size(), ModifiedNS: mediaInfo.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 160, Height: 90, Duration: 2})
	if localItem != item {
		t.Fatal("alternate fixture is not linked to the same title")
	}
	if best, err := s.SelectedAsset(ctx, item, nil); err != nil || best != localAsset {
		t.Fatal("local file is not the default version", best, err)
	}
	selectedRemote, e := s.CreateV1(ctx, p, item, "explicit-remote-alternate", V1Choice{AssetID: remoteAsset, QualityID: "original"}, nil)
	if e != nil {
		t.Fatal("explicit STRM alternate did not play", e)
	}
	var acceptedAsset string
	if err := db.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, selectedRemote.ID).Scan(&acceptedAsset); err != nil || acceptedAsset != remoteAsset {
		t.Fatal("wrong remote version accepted", acceptedAsset, err)
	}
	// Make the remote file preferred, then explicitly play local. An upstream
	// refusal cannot affect a version that does not use that origin at all.
	mustAudioExec(t, db, `UPDATE catalog_assets SET height=2160 WHERE token=?`, remoteAsset)
	if best, err := s.SelectedAsset(ctx, item, nil); err != nil || best != remoteAsset {
		t.Fatal("remote file is not the default version", best, err)
	}
	beforeLocal := originRequests.Load()
	selectedLocal, e := s.CreateV1(ctx, p, item, "explicit-local-alternate", V1Choice{AssetID: localAsset, QualityID: "original"}, nil)
	if e != nil {
		t.Fatal("local selection inspected an unselected remote default", e)
	}
	if originRequests.Load() != beforeLocal {
		t.Fatal("local selection fetched the unselected remote default")
	}
	if err := db.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, selectedLocal.ID).Scan(&acceptedAsset); err != nil || acceptedAsset != localAsset {
		t.Fatal("wrong local version accepted", acceptedAsset, err)
	}
	// A valid asset from a different title is not a selectable alternate and
	// must be rejected before touching the otherwise-default remote source.
	_, _, foreignAsset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "other", "Other", compactcatalog.Asset{Path: filepath.Join(root, "foreign.mp4"), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"})
	if _, e = s.CreateV1(ctx, p, item, "foreign-version", V1Choice{AssetID: foreignAsset, QualityID: "original"}, nil); e == nil {
		t.Fatal("foreign asset was accepted as a version")
	}
	if originRequests.Load() != beforeLocal {
		t.Fatal("foreign version triggered remote source inspection")
	}
	if _, e = s.ApproveNetworkRoots(ctx, "lib", nil); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = s.OpenRemote(ctx, grant, "GET", "", false); e == nil {
		t.Fatal("network approval removal did not revoke media")
	}
}
