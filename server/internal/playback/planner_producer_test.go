package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

// This runs the real producer from a planner-created and persisted target,
// through readiness, playlists, fragment publication, seek restart and re-plan.
func TestPlannerFragmentedConversionsReachTheRealProducer(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	for _, codec := range []string{"h264", "hevc"} {
		t.Run(codec, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			toolchain, err := decoder.ProbeToolchain(ctx, binary)
			if err != nil {
				t.Fatal(err)
			}
			old := decoder.CurrentToolchain()
			decoder.ConfigureToolchain(toolchain)
			defer decoder.RestoreToolchain(old)
			if codec == "hevc" && !toolchain.Encoders["libx265"] {
				t.Skip("software HEVC encoder absent")
			}
			root := t.TempDir()
			input := filepath.Join(root, "source.mkv")
			// MPEG-2 obliges both target profiles to convert rather than remux.
			args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=19", "-f", "lavfi", "-i", "sine=r=48000:d=19", "-map", "0:v", "-map", "1:a", "-map", "1:a", "-c:v", "mpeg2video", "-g", "24", "-c:a", "aac", input}
			if out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
				t.Fatalf("sample: %v %s", err, out)
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
			mustAudioExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',X'00','profile',1)`)
			insertFixtureSession(t, db, "login", "owner", "profile", "owner", "2099-01-01T00:00:00Z")
			_, item, asset := catalogFixture(t, db, "lib", root, compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: input, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := assets.PersistStreams(tx, asset, info.Size(), info.ModTime().UnixNano(), facts.Streams); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			h, err := NewHLS(ctx, db, filepath.Join(root, "hls"), binary)
			if err != nil {
				t.Fatal(err)
			}
			defer h.cancelAll()
			s := New(db)
			s.ConfigureHLS(h)
			p := identity.Principal{Hash: "login", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}, Epoch: 1}
			profile := BaselineClientProfile()
			profile.Video = []ClientVideoCodec{{Codec: codec, BitDepths: []int{8}, DynamicRanges: []string{"sdr"}, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRate: 60}}
			profile.Transports = []ClientTransport{{Transport: TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{codec}, Audio: []string{"aac"}}}
			raw, _ := json.Marshal(documentOf(profile))
			if _, err := s.PublishCapabilities(ctx, p, raw); err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateV1(ctx, p, item, "start", V1Choice{QualityID: "original"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := s.SessionPlan(ctx, session.ID)
			if err != nil || plan.ConversionTarget == nil || plan.OutputVideoCodec != codec || plan.OutputContainer != "fmp4_hls" || plan.ConversionTarget.RateControl != "quality" {
				t.Fatalf("plan: %+v %v", plan, err)
			}
			if err := s.Ready(ctx, session); err != nil {
				t.Fatal(err)
			}
			grant := strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8")
			master, err := s.HLSFileContext(ctx, grant, "master.m3u8")
			if err != nil {
				t.Fatal(err)
			}
			raw, err = os.ReadFile(master)
			if err != nil || !strings.Contains(string(raw), "video.m3u8") {
				t.Fatalf("master: %s %v", raw, err)
			}
			playlist, err := s.HLSFileContext(ctx, grant, "video.m3u8")
			if err != nil {
				t.Fatal(err)
			}
			raw, err = os.ReadFile(playlist)
			if err != nil || !strings.Contains(string(raw), "#EXT-X-MAP:URI=\"init.mp4\"") {
				t.Fatalf("playlist: %s %v", raw, err)
			}
			init, err := s.HLSFileContext(ctx, grant, "init.mp4")
			if err != nil {
				t.Fatal(err)
			}
			initial, _ := os.ReadFile(init)
			if len(plan.Renditions) != 2 {
				t.Fatalf("missing alternate audio: %+v", plan.Renditions)
			}
			for _, r := range plan.Renditions {
				if r.Packaging != "fmp4_hls" {
					t.Fatalf("loaded rendition lost transport: %+v", r)
				}
				playlist, err := s.HLSFileContext(ctx, grant, "audio-"+itoa(r.Ordinal)+".m3u8")
				if err != nil {
					t.Fatal(err)
				}
				body, err := os.ReadFile(playlist)
				if err != nil || !strings.Contains(string(body), "#EXT-X-MAP") || strings.Contains(string(body), ".ts") {
					t.Fatalf("audio playlist: %s %v", body, err)
				}
				segment, err := s.HLSFileContext(ctx, grant, renditionSegmentFile(r, 0))
				if err != nil {
					t.Fatal(err)
				}
				audioInit, err := s.HLSFileContext(ctx, grant, renditionInitFile(r.Ordinal))
				if err != nil {
					t.Fatal(err)
				}
				initBytes, err := os.ReadFile(audioInit)
				if err != nil {
					t.Fatal(err)
				}
				mediaBytes, err := os.ReadFile(segment)
				if err != nil {
					t.Fatal(err)
				}
				joined := filepath.Join(root, "audio-"+itoa(r.Ordinal)+".mp4")
				if err := os.WriteFile(joined, append(initBytes, mediaBytes...), 0600); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.CommandContext(ctx, binary, "-v", "error", "-i", joined, "-f", "null", "-").CombinedOutput(); err != nil {
					t.Fatalf("audio decode: %s %v", out, err)
				}
			}
			// Demand a later slot, then delete it after the producer exits, forcing an
			// actual restart at a nonzero source time. Earlier fragments must survive.
			first, err := s.HLSFileContext(ctx, grant, "segment-000000.m4s")
			if err != nil {
				t.Fatal(err)
			}
			firstBytes, _ := os.ReadFile(first)
			for {
				h.mu.Lock()
				n := len(h.active)
				h.mu.Unlock()
				if n == 0 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(20 * time.Millisecond):
				}
			}
			dir := filepath.Dir(first)
			if err := os.Remove(filepath.Join(dir, "segment-000002.m4s")); err != nil {
				t.Fatal(err)
			}
			mustAudioExec(t, db, `DELETE FROM playback_hls_windows WHERE session_id=?`, session.ID)
			seeked, err := s.HLSFileContext(ctx, grant, "segment-000002.m4s")
			if err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(init)
			if !bytes.Equal(initial, after) {
				t.Fatal("seek changed the init")
			}
			before, _ := os.ReadFile(first)
			if !bytes.Equal(firstBytes, before) {
				t.Fatal("seek rewrote prior fragment")
			}
			media, _ := os.ReadFile(seeked)
			standalone := filepath.Join(root, "seek.mp4")
			if err := os.WriteFile(standalone, append(after, media...), 0600); err != nil {
				t.Fatal(err)
			}
			probe := decodertest.QualifiedFFprobe(t)
			out, err := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "stream=codec_name:packet=pts_time", "-read_intervals", "%+#1", "-of", "json", standalone).CombinedOutput()
			if err != nil || !strings.Contains(string(out), codec) || !strings.Contains(string(out), "12.") {
				t.Fatalf("seek: %s %v", out, err)
			}
			if out, err := exec.CommandContext(ctx, binary, "-v", "error", "-i", standalone, "-threads", "2", "-f", "null", "-").CombinedOutput(); err != nil {
				t.Fatalf("decode: %s %v", out, err)
			}
			// Re-plan the same media generation through the shared fallback seam.
			next, err := h.downgradeCopyPlan(ctx, session.ID, plan, true)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := s.SessionPlan(ctx, session.ID)
			if err != nil || loaded.ConversionTarget == nil || next.OutputVideoCodec != codec || loaded.OutputVideoCodec != codec {
				t.Fatalf("re-plan lost target: %+v %v", loaded, err)
			}
		})
	}
}
