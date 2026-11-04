//go:build linux

package ingestion

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The optional directory retains identical media bytes for before/after
// evidence. Database and scan state are always fresh. Disabling only worker
// reuse makes one confined ffprobe invocation equal one supervised process;
// sandboxing, source custody and physical retirement stay enabled.
func TestBasicMixedPlaybackProbeCounts(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	shared := os.Getenv("PORTICO_FRAME_COUNT_FIXTURE_DIR")
	if shared == "" {
		shared = t.TempDir()
	}
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	samples := []struct {
		name, kind, file, relative, codec string
		args                              []string
	}{
		{"h264", "movie", "h264.mp4", "Film (2026)/Film (2026).mp4", "h264", []string{"-c:v", "libx264", "-preset", "ultrafast"}},
		{"hevc_hdr10", "movie", "hevc.mp4", "Film (2026)/Film (2026).mp4", "hevc", []string{"-c:v", "libx265", "-x265-params", "pools=none:frame-threads=1:log-level=error", "-pix_fmt", "yuv420p10le", "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc"}},
		{"dv_config", "movie", "dv.mp4", "Film (2026)/Film (2026).mp4", "hevc", nil},
		{"av1", "movie", "av1.mp4", "Film (2026)/Film (2026).mp4", "av1", []string{"-c:v", "libaom-av1", "-cpu-used", "8", "-crf", "45", "-b:v", "0"}},
		{"vp9", "movie", "vp9.webm", "Film (2026)/Film (2026).webm", "vp9", []string{"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "0", "-crf", "45"}},
		{"episode", "tv", "episode.mp4", "Count Show (2026)/Season 01/Count Show - S01E01.mp4", "h264", []string{"-c:v", "libx264", "-preset", "ultrafast"}},
		{"music", "music", "music.flac", "Artist/Album/01 - Track.flac", "flac", []string{"-c:a", "flac"}},
		{"audiobook", "audiobook", "book.m4b", "Author/Book/Book.m4b", "aac", []string{"-c:a", "aac"}},
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mediaexec.Configure(mediaexec.Options{StateDir: t.TempDir(), Helper: binary, SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	pooled := mediaexec.ProbePoolEnabled.Swap(false)
	defer mediaexec.ProbePoolEnabled.Store(pooled)
	for _, sample := range samples {
		t.Run(sample.name, func(t *testing.T) {
			fixture := filepath.Join(shared, sample.file)
			if _, err := os.Stat(fixture); os.IsNotExist(err) {
				if sample.name == "dv_config" {
					raw, e := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "video", "dovi-probe-r40", "source-MEL.mp4"))
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(fixture, raw, 0600); e != nil {
						t.Fatal(e)
					}
				} else {
					args := []string{"-v", "error", "-nostdin", "-threads", "1", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=24"}
					if sample.kind == "music" || sample.kind == "audiobook" {
						args = []string{"-v", "error", "-nostdin", "-threads", "1", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000"}
					}
					args = append(args, "-t", "0.5")
					args = append(args, sample.args...)
					args = append(args, "-threads", "1", "-metadata", "title=Count Fixture", "-metadata", "artist=Count Author", "-metadata", "album=Count Album", fixture)
					if out, e := exec.Command(ffmpeg, args...).CombinedOutput(); e != nil {
						t.Fatalf("generate fixture: %v %s", e, out)
					}
				}
			}
			raw, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			library := filepath.Join(root, "library")
			path := filepath.Join(library, filepath.FromSlash(sample.relative))
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := persistence.Open(filepath.Join(root, "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cat := catalog.New(db)
			store := storage.New(binary)
			cat.SetStorage(store)
			scanner := New(db, cat, assets.Probe{Binary: ffprobe, Supervisor: store.Supervisor})
			scanner.SetStorage(store)
			meta, err := localmetadata.New(filepath.Join(root, "art"), ffmpeg, store)
			if err != nil {
				t.Fatal(err)
			}
			scanner.LocalMetadata = meta
			lib, err := cat.Create("Count", sample.kind, library)
			if err != nil {
				t.Fatal(err)
			}
			scan := func() {
				t.Helper()
				job, err := scanner.Queue(lib.ID)
				if err != nil {
					t.Fatal(err)
				}
				for n := 0; n < 64; n++ {
					scanner.process(context.Background(), job.ID, lib.ID)
					drainDerivedCatalogue(t, db)
					state, e := scanner.Get(job.ID)
					if e != nil {
						t.Fatal(e)
					}
					if state.Status == "complete" || state.Status == "complete_with_warnings" {
						return
					}
					if state.Status == "failed" {
						t.Fatal("scan failed", state)
					}
				}
				t.Fatal("scan did not settle")
			}
			before := processStarts(store)
			scan()
			first := processStarts(store) - before
			scan()
			rescan := processStarts(store) - before - first
			var observed string
			column := "video_codec"
			if sample.kind == "music" || sample.kind == "audiobook" {
				column = "audio_codec"
			}
			if err = db.QueryRow(`SELECT `+column+` FROM catalog_assets WHERE path=?`, path).Scan(&observed); err != nil || observed != sample.codec {
				t.Fatal("fixture not analysed", observed, err)
			}
			t.Logf("PROBE_COUNT file=%s sha256=%x basic=%d unchanged_rescan=%d", sample.name, sha256.Sum256(raw), first, rescan)
			if first != 1 || rescan != 0 {
				t.Errorf("%s: Basic probes=%d unchanged rescan=%d; want 1 and 0", sample.name, first, rescan)
			}
		})
	}
}
