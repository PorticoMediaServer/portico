package playback

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestRealFFmpegRemuxProducesPlayableHLS(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	input := filepath.Join(root, "fixture.mkv")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-y", input)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	facts, e := (assets.Probe{}).Inspect(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(input)
	if e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, item, _ := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: input, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	h, e := NewHLS(ctx, db, filepath.Join(root, "hls"), binary)
	if e != nil {
		t.Fatal(e)
	}
	s := New(db)
	s.ConfigureHLS(h)
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	session, e := s.Create(p, item, "automatic", "hls-one")
	if e != nil {
		t.Fatal(e)
	}
	if session.Mode != "hls" {
		t.Fatal(session)
	}
	if e = s.Ready(ctx, session); e != nil {
		t.Fatal(e)
	}
	manifest := filepath.Join(root, "hls", session.ID, "master.m3u8")
	raw, e := os.ReadFile(manifest)
	if e != nil || !strings.Contains(string(raw), "segment-") {
		t.Fatalf("manifest %s %v", raw, e)
	}
	for n := 0; n < 100; n++ {
		h.mu.Lock()
		count := len(h.active)
		h.mu.Unlock()
		if count == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	out, e := exec.CommandContext(ctx, decodertest.QualifiedFFprobe(t), "-v", "error", "-show_entries", "stream=codec_name", "-of", "json", manifest).CombinedOutput()
	if e != nil || !strings.Contains(string(out), "h264") || !strings.Contains(string(out), "aac") {
		t.Fatalf("HLS probe %s %v", out, e)
	}
	if _, e = h.File(strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8"), "../fixture.mkv"); e == nil {
		t.Fatal("artifact traversal accepted")
	}
}

func TestRealFLACConvertsToAudioOnlyHLS(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	input := filepath.Join(root, "tone.flac")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "flac", input).CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, out)
	}
	facts, err := (assets.Probe{}).Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(input)
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, item, _ := catalogFixture(t, db, "lib", "/test", compactcatalog.Track, "song", "Tone", compactcatalog.Asset{Path: input, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	hls, err := NewHLS(ctx, db, filepath.Join(root, "hls"), binary)
	if err != nil {
		t.Fatal(err)
	}
	player := New(db)
	player.ConfigureHLS(hls)
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	session, err := player.Create(p, item, "automatic", "audio")
	if err != nil || session.Mode != "hls" {
		t.Fatal(session, err)
	}
	if err = player.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "hls", session.ID, "master.m3u8")
	for i := 0; i < 100; i++ {
		hls.mu.Lock()
		n := len(hls.active)
		hls.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	output, err := exec.CommandContext(ctx, decodertest.QualifiedFFprobe(t), "-v", "error", "-show_entries", "stream=codec_name,codec_type", "-of", "json", manifest).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "aac") || strings.Contains(string(output), "video") {
		t.Fatalf("not audio-only playable HLS: %v %s", err, output)
	}
}
