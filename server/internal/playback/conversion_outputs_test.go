package playback

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strconv"
	"strings"
	"testing"
	"time"
)

func conversionOutputFixture(t *testing.T) (*sql.DB, *HLS, string) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, _, token := catalogFixture(t, db, "library", "/isolated/no-media", compactcatalog.Movie, "item", "Test", compactcatalog.Asset{Path: "/isolated/no-media/test.mp4", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "hevc", AudioCodec: "truehd", Width: 1920, Height: 1080, Duration: 100})
	_, err = db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('output-test','session','owner','profile',?,?,3,'playing','output-grant','token','2100-01-01T00:00:00Z','output-request',100)`, id, token)
	if err != nil {
		t.Fatal(err)
	}
	h := &HLS{db: db, root: filepath.Join(root, "hls"), active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, usage: map[string]*hlsUsage{}}
	if _, err := db.Exec(`UPDATE playback_sessions SET mode='hls' WHERE id='output-test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('output-test',?,1,1)`, token); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.root, "output-test"), 0700); err != nil {
		t.Fatal(err)
	}
	return db, h, "output-test"
}

func TestConversionTargetSurvivesSessionReadback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, _, id := conversionOutputFixture(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var sourceID string
	if err := tx.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, id).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	plan := DeliveryPlan{SourceSize: 1, SourceModifiedNS: 1, Profile: DeliveryProfileLegacy, SourceID: sourceID, Mode: "hls", Strategy: DeliveryVideoConversion, Reason: "conversion", VideoAction: "convert", AudioAction: "convert", OutputContainer: "mpegts_hls", OutputVideoCodec: "h264", OutputAudioCodec: "aac", AudioChannels: 2, HardwareBackend: "software", AudioStream: 1}
	if err := persistDeliveryPlan(tx, id, &plan); err != nil {
		t.Fatal(err)
	}
	target := decoder.ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "hdr10", Width: 1920, Height: 1080, FrameRate: 24, AudioCodec: "flac", AudioChannels: 8, AudioSampleRate: 96000, RateControl: "quality", Packaging: "fmp4_hls"}
	if err := StoreConversionTarget(context.Background(), tx, id, target); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadDeliveryPlan(context.Background(), db, id)
	if err != nil || loaded.ConversionTarget == nil || loaded.OutputVideoCodec != "hevc" || loaded.OutputContainer != "fmp4_hls" || loaded.AudioChannels != 8 {
		t.Fatal(loaded, err)
	}
	_, args, _, err := loaded.CodecArgs()
	if err != nil || !strings.Contains(strings.Join(args, " "), "-c:v libx265") || !strings.Contains(strings.Join(args, " "), "-c:a flac") {
		t.Fatal(args, err)
	}
	master := string(hlsMasterManifest(loaded))
	if !strings.Contains(master, "VIDEO-RANGE=PQ") || !strings.Contains(master, "hvc1.2.4.L") {
		t.Fatal(master)
	}
}

func TestConversionBudgetPrunesOnlyPlayedSegments(t *testing.T) {
	root := t.TempDir()
	id := "budget"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		file, err := os.Create(filepath.Join(dir, hlsSegmentFile(index)))
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(32 << 20); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultDeliveryConfiguration()
	cfg.SessionStorageMiB = 64
	cancelled := false
	h := &HLS{root: root, settings: fixedSettings{cfg}, active: map[string]context.CancelFunc{id: func() { cancelled = true }}, windows: map[string]*hlsWindow{id: {served: 2}}, usage: map[string]*hlsUsage{}}
	h.enforceSessionCeilings([]string{id})
	if cancelled {
		t.Fatal("stopped a producer instead of reclaiming played output")
	}
	if !segmentReady(filepath.Join(dir, hlsSegmentFile(2))) || !segmentReady(filepath.Join(dir, "init.mp4")) {
		t.Fatal("removed current segment or init")
	}
	if _, err := os.Stat(filepath.Join(dir, hlsSegmentFile(0))); !os.IsNotExist(err) {
		t.Fatal("old played output survived pressure")
	}
	if bytes, _, err := measureDirectory(dir); err != nil || bytes > 64<<20 {
		t.Fatal(bytes, err)
	}
}

func TestConversionFailureHistorySurvivesCleanup(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	if _, err := db.Exec(`INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,?,'building')`, id, filepath.Join(h.root, id)); err != nil {
		t.Fatal(err)
	}
	h.failedWith(withProducerGeneration(context.Background(), 2), id, FailureConverter, "stale")
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM playback_conversion_failures`).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale producer entered history", count, err)
	}
	h.failedWith(withProducerGeneration(context.Background(), 3), id, FailureConverter, "encoder failed in "+h.root)
	if err := h.CleanupGenerated(context.Background()); err != nil {
		t.Fatal(err)
	}
	var diagnostic string
	if err := db.QueryRow(`SELECT diagnostic FROM playback_conversion_failures WHERE session_id=?`, id).Scan(&diagnostic); err != nil || strings.Contains(diagnostic, h.root) {
		t.Fatal(diagnostic, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM playback_artifacts WHERE session_id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("output row not reclaimed", count, err)
	}
}

func TestUnindexedPacketTimelineKeepsTheLastKeyframe(t *testing.T) {
	writer := newPacketTimelineWriter(1.4, 19)
	for _, chunk := range []string{"1.4,K_\n1.44,__\n5.", "4,K_\n9.4,K_\n13.4,K_\n17.4,K_\n"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	timeline, err := writer.timeline()
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 4, 12, 16, 19}
	if len(timeline.Boundaries) != len(want) {
		t.Fatal(timeline.Boundaries)
	}
	for i := range want {
		if math.Abs(timeline.Boundaries[i]-want[i]) > 0.000001 {
			t.Fatal(timeline.Boundaries)
		}
	}
	bad := newPacketTimelineWriter(0, 60)
	_, _ = bad.Write([]byte("0,K_\n"))
	if _, err := bad.timeline(); err == nil {
		t.Fatal("accepted unsafe keyframe gap")
	}
}

func TestEmbeddedFontsUsePrivateGeneratedNames(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	dir := t.TempDir()
	pack, err := mediaexec.Fonts()
	if err != nil {
		t.Fatal(err)
	}
	font := filepath.Join(pack.Dir, "DejaVuSans.ttf")
	source := filepath.Join(dir, "source.mkv")
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: []string{"-v", "error", "-f", "lavfi", "-i", "color=black:s=160x90:r=24:d=0.2", "-c:v", "libx264", "-threads", "2", "-attach", font, "-metadata:s:t", "mimetype=application/x-truetype-font", "-metadata:s:t", "filename=../../outside.ttf", source}, ReadPaths: []string{font}, WriteDirs: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	h := &HLS{binary: binary}
	fonts, err := h.extractBurnFonts(ctx, &assets.PlaybackInput{Argument: source}, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(fonts, "font-00.ttf")); err != nil || info.Size() == 0 {
		t.Fatal("embedded font absent", err)
	}
	if _, err := os.Stat(filepath.Join(fonts, "zz-fallback-NotoSansCJKjp-Regular.otf")); err != nil {
		t.Fatal("fallback absent", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "outside.ttf")); !os.IsNotExist(err) {
		t.Fatal("followed attachment metadata as a path")
	}
}

func TestMPEGTSHasASequentialCopyTimeline(t *testing.T) {
	binary, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	dir := t.TempDir()
	source := filepath.Join(dir, "broadcast.ts")
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=19", "-c:v", "libx264", "-preset", "ultrafast", "-g", "96", "-sc_threshold", "0", "-threads", "2", "-f", "mpegts", source}, WriteDirs: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	timeline, err := probeCopyTimeline(ctx, localTimelineProbe(probe, &assets.PlaybackInput{Argument: source}), localPacketTimelineProbe(probe, &assets.PlaybackInput{Argument: source}), 19)
	if err != nil || timeline == nil || timeline.count() < 3 {
		t.Fatal(timeline, err)
	}
	for _, at := range timeline.Boundaries[1 : len(timeline.Boundaries)-1] {
		if math.Abs(at/4-math.Round(at/4)) > 0.05 {
			t.Fatal("cut was not a source keyframe", timeline.Boundaries)
		}
	}
}

func TestMPEGTSSeekRestartsOnIndexedPacket(t *testing.T) {
	binary, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.ts")
	run := func(args []string) {
		t.Helper()
		// The source is given to the tool to read only once it exists: the
		// first run is the one that writes it, into the writable folder.
		job := mediaexec.Job{Executable: binary, Args: args, WriteDirs: []string{dir}}
		if _, err := os.Stat(source); err == nil {
			job.ReadPaths = []string{source}
		}
		cmd, err := mediaexec.CommandContext(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	run([]string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=19", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "2", "-g", "96", "-sc_threshold", "0", "-f", "mpegts", source})
	timeline, err := probeCopyTimeline(ctx, localTimelineProbe(probe, &assets.PlaybackInput{Argument: source}), localPacketTimelineProbe(probe, &assets.PlaybackInput{Argument: source}), 19)
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []int{0, 1, 2} {
		staging := filepath.Join(dir, strconv.Itoa(from))
		if err := os.Mkdir(staging, 0700); err != nil {
			t.Fatal(err)
		}
		position := timeline.Boundaries[from]
		args := []string{"-v", "error", "-fflags", "+genpts+discardcorrupt"}
		if from > 0 {
			args = append(args, "-ss", strconv.FormatFloat(position+0.002, 'f', 6, 64))
		}
		args = append(args, "-i", source, "-map", "0:v:0", "-c:v", "copy", "-an", "-muxdelay", "0", "-muxpreload", "0")
		args = append(args, copySegmentArgs(timeline, from, staging)...)
		run(args)
		out, err := runProbe(ctx, probe, &assets.PlaybackInput{Argument: filepath.Join(staging, hlsSegmentFile(from))}, "-select_streams", "v:0", "-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", "-read_intervals", "%+#1")
		if err != nil {
			t.Fatal(err)
		}
		stamp, flags, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
		pts, err := strconv.ParseFloat(stamp, 64)
		if err != nil || math.Abs(pts-position) > 0.05 || !strings.HasPrefix(flags, "K") {
			t.Fatalf("window %d expected keyframe at %.6f, got %q", from, position, out)
		}
		frameHash := func(path string, seek float64) string {
			args := []string{"-v", "error", "-i", path}
			if seek > 0 {
				args = append(args, "-ss", strconv.FormatFloat(seek, 'f', 6, 64))
			}
			args = append(args, "-map", "0:v:0", "-frames:v", "1", "-threads", "2", "-f", "framemd5", "-")
			cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: args, ReadPaths: []string{path}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			rows := strings.Split(strings.TrimSpace(string(raw)), "\n")
			parts := strings.Split(rows[len(rows)-1], ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
		if frameHash(source, position) != frameHash(filepath.Join(staging, hlsSegmentFile(from)), 0) {
			t.Fatalf("window %d copied the wrong source picture", from)
		}

	}
}

func TestConversionReplacementRetainsHDRTargetAndRotatesGrant(t *testing.T) {
	f := newReplanFixture(t, 1_600_000)
	session := f.start("hardware-replacement", V1Choice{})
	target := decoder.ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "hdr10", AudioCodec: "aac", AudioChannels: 6, Width: 1920, Height: 1080, RateControl: "quality", Packaging: "fmp4_hls"}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE playback_delivery_plans SET video_action='convert',audio_action='convert',strategy='video_conversion' WHERE session_id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	if err = StoreConversionTarget(context.Background(), tx, session.ID, target); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before := f.plan(session.ID)
	var generation int
	var grant string
	if err = f.db.QueryRow(`SELECT generation,grant_hash FROM playback_sessions WHERE id=?`, session.ID).Scan(&generation, &grant); err != nil {
		t.Fatal(err)
	}
	var published struct {
		media, url string
		generation int
	}
	f.s.GrantReplacedTx = func(_ context.Context, tx *sql.Tx, media string, generation int, url string) error {
		// Inside the replacing transaction: the rotated grant is visible.
		var current int
		if err := tx.QueryRow(`SELECT generation FROM playback_sessions WHERE id=?`, media).Scan(&current); err != nil || current != generation {
			t.Fatalf("published generation %d inside the transaction, row has %d (%v)", generation, current, err)
		}
		published.media, published.generation, published.url = media, generation, url
		return nil
	}
	next, err := f.h.downgradeCopyPlan(context.Background(), session.ID, before, true)
	if err != nil {
		t.Fatal(err)
	}
	loaded := f.plan(session.ID)
	if next.ConversionTarget == nil || loaded.ConversionTarget == nil || loaded.ConversionTarget.DynamicRange != "hdr10" || loaded.OutputVideoCodec != "hevc" || loaded.HardwareBackend != "software" {
		t.Fatalf("lost selected output: %+v", loaded)
	}
	var after int
	var newGrant string
	if err = f.db.QueryRow(`SELECT generation,grant_hash FROM playback_sessions WHERE id=?`, session.ID).Scan(&after, &newGrant); err != nil {
		t.Fatal(err)
	}
	if after != generation+1 || grant == newGrant {
		t.Fatal("replacement reused generation or grant")
	}
	var token string
	if err = f.db.QueryRow(`SELECT grant_token FROM playback_sessions WHERE id=?`, session.ID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if published.media != session.ID || published.generation != after || !strings.HasPrefix(published.url, "/v1/media/"+token+"/") {
		t.Fatalf("replacement published %+v, want generation %d under the new grant", published, after)
	}
}

func TestConvertedFragmentClockSurvivesSeeks(t *testing.T) {
	binary, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	facts, err := decoder.ProbeToolchain(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"h264", "hevc", "av1", "flac", "opus"} {
		t.Run(codec, func(t *testing.T) {
			// Qualify the optional codec from inventory before exercising planning.
			// A planner error after this point is a regression, not an environment skip.
			candidates := map[string][]string{
				"h264": {"libx264"}, "hevc": {"libx265"},
				"av1":  {"libsvtav1", "libaom-av1", "librav1e"},
				"flac": {"flac"}, "opus": {"libopus"},
			}[codec]
			available := false
			for _, encoder := range candidates {
				available = available || facts.Encoders[encoder]
			}
			if !available {
				decodertest.Unavailable(t, fmt.Sprintf("qualified FFmpeg has no %s encoder", codec))
			}
			audio := codec == "flac" || codec == "opus"
			target := decoder.ConversionTarget{VideoCodec: codec, RateControl: "quality", Width: 160, Height: 90, FrameRate: 24, Packaging: "fmp4_hls"}
			request := decoder.ConversionRequest{Target: &target, ConvertVideo: true, SoftwarePreset: "ultrafast", Toolchain: &facts}
			if audio {
				target.VideoCodec = "h264"
				target.AudioCodec = codec
				target.AudioChannels = 6
				request.ConvertVideo = false
				request.ConvertAudio = true
			}
			graph, err := decoder.BuildConversion(request)
			if err != nil {
				t.Fatalf("qualified %s conversion plan failed: %v", codec, err)
			}
			dir := t.TempDir()
			firstPTS := 0.0
			for _, from := range []int{0, 2} {
				stage := filepath.Join(dir, fmt.Sprintf("window-%d", from))
				if err := os.Mkdir(stage, 0700); err != nil {
					t.Fatal(err)
				}
				initName := fmt.Sprintf("init-%d.mp4", from)
				filename := fmt.Sprintf("segment-%06d.m4s", from)
				pattern := "segment-%06d.m4s"
				stream := "v:0"
				var rendition *AudioRenditionPlan
				args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=7"}
				if audio {
					rendition = &AudioRenditionPlan{Ordinal: 0, Codec: codec}
					initName = fmt.Sprintf("audio-0-init-%d.mp4", from)
					filename = renditionSegmentFile(*rendition, from)
					pattern = "audio-0-%06d.m4s"
					stream = "a:0"
					args[len(args)-1] = "anullsrc=r=48000:cl=5.1:d=7"
					args = append(args, graph.Audio...)
					args = append(args, "-vn")
				} else {
					args = append(args, graph.Video...)
					args = append(args, "-an")
				}
				pack, _, err := decoder.HLSPackagingArgs("fmp4_hls", initName)
				if err != nil {
					t.Fatal(err)
				}
				args = append(args, pack...)
				args = append(args, "-f", "hls", "-hls_time", "6", "-hls_list_size", "0", "-hls_flags", "independent_segments+temp_file", "-output_ts_offset", strconv.Itoa(from*6), "-start_number", strconv.Itoa(from), "-hls_segment_filename", filepath.Join(stage, pattern), filepath.Join(stage, "window.m3u8"))
				cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: args, WriteDirs: []string{dir}})
				if err != nil {
					t.Fatal(err)
				}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatal(err, string(out))
				}
				highest, err := promoteConvertedFragments(stage, dir, initName, from, rendition, &convertedFragmentClock{})
				if err != nil || highest < from {
					t.Fatal(highest, err)
				}
				initFile := "init.mp4"
				if audio {
					initFile = renditionInitFile(0)
				}
				init, err := os.ReadFile(filepath.Join(dir, initFile))
				if err != nil {
					t.Fatal(err)
				}
				media, err := os.ReadFile(filepath.Join(dir, filename))
				if err != nil {
					t.Fatal(err)
				}
				standalone := filepath.Join(dir, fmt.Sprintf("standalone-%d.mp4", from))
				if err := os.WriteFile(standalone, append(init, media...), 0600); err != nil {
					t.Fatal(err)
				}
				out, err := runProbe(ctx, probe, &assets.PlaybackInput{Argument: standalone}, "-select_streams", stream, "-show_entries", "packet=pts_time", "-of", "csv=p=0", "-read_intervals", "%+#1")
				if err != nil {
					t.Fatal(err)
				}
				stamp, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
				pts, err := strconv.ParseFloat(stamp, 64)
				if err != nil {
					t.Fatal(err, string(out))
				}
				if from == 0 {
					firstPTS = pts
				} else if math.Abs(pts-firstPTS-float64(from*6)) > 0.001 {
					t.Fatal("presentation clock reset", firstPTS, pts)
				}
				cmd, err = mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: []string{"-v", "error", "-i", standalone, "-threads", "2", "-f", "null", "-"}, ReadPaths: []string{standalone}})
				if err != nil {
					t.Fatal(err)
				}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatal(err, string(out))
				}
			}
		})
	}
}
