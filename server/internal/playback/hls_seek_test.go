package playback

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// seekFixture builds a real converted session on the ordinary (non-finite) HLS
// path, with a source that must be repackaged so conversion actually runs.
func seekFixture(t *testing.T, settings DeliverySettings, configure ...func(*HLS)) (context.Context, *sql.DB, *Service, *HLS, Session, string) {
	t.Helper()
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	root := t.TempDir()
	input := filepath.Join(root, "fixture.mkv")
	// Two minutes, so a seek can land well past anything converted so far.
	out, err := exec.CommandContext(ctx, binary, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "120", "-c:v", "libx264", "-preset", "ultrafast", "-g", "48", "-pix_fmt", "yuv420p", "-c:a", "aac", input).CombinedOutput()
	if err != nil {
		cancel()
		t.Fatalf("fixture: %v %s", err, out)
	}
	facts, err := (assets.Probe{}).Inspect(ctx, input)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	info, err := os.Stat(input)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	mustAudioExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',X'00','profile',1)`)
	insertFixtureSession(t, db, "login", "owner", "profile", "owner", "2099-01-01T00:00:00Z")
	_, item, _ := catalogFixture(t, db, "lib", root, compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: input, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	h, err := NewHLS(ctx, db, filepath.Join(root, "hls"), binary)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := h.Shutdown(shutdown); err != nil {
			t.Error("shutdown HLS fixture", err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	h.ConfigureSettings(settings)
	for _, setup := range configure {
		setup(h)
	}
	s := New(db)
	s.ConfigureHLS(h)
	s.ConfigureDelivery(settings, nil, nil, "")
	p := identity.Principal{Hash: "login", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}, Epoch: 1}
	session, err := s.Create(p, item, "automatic", "seek")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	grant := strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8")

	return ctx, db, s, h, session, grant
}

type fixedSettings struct{ cfg DeliveryConfiguration }

func (f fixedSettings) Delivery() DeliveryConfiguration { return f.cfg }

// A seek past the converted edge restarts conversion at the requested slot,
// keeps everything already converted, and never renumbers a segment.
func TestHLSSeekRestartsConversionAndKeepsConvertedSegments(t *testing.T) {
	cfg := DefaultDeliveryConfiguration()
	// Retention off for this test: the assertion is about what restart keeps,
	// not about what retention reclaims.
	cfg.PlayedRetentionSeconds = 0
	ctx, db, s, _, session, grant := seekFixture(t, fixedSettings{cfg})

	if session.Mode != "hls" {
		t.Fatal("matroska source was not repackaged", session.Mode)
	}
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	manifest, err := s.HLSFileContext(ctx, grant, "master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// The whole title is published up front, so a seek is a plain segment GET.
	if !strings.Contains(string(raw), "#EXT-X-ENDLIST") || !strings.Contains(string(raw), "segment-000019.ts") {
		t.Fatal("timeline was not published up front", string(raw))
	}
	if strings.Contains(string(raw), "#EXT-X-DISCONTINUITY") {
		t.Fatal("absolute timeline published a discontinuity")
	}

	// Play from the start.
	first, err := s.HLSFileContext(ctx, grant, "segment-000000.ts")
	if err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil || firstInfo.Size() == 0 {
		t.Fatal("first slot was not produced", err)
	}

	// Reclaim everything from slot 10 on, so slot 15 is genuinely past the
	// converted edge whatever the converter managed in the meantime. This is the
	// same state a viewer reaches by seeking ahead of a slower conversion.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var running int
		if err = db.QueryRow(`SELECT count(*) FROM playback_hls_windows WHERE session_id=? AND status='running'`, session.ID).Scan(&running); err != nil {
			t.Fatal(err)
		}
		if running == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	dir := filepath.Dir(first)
	for i := 10; i < 20; i++ {
		if err = os.Remove(filepath.Join(dir, hlsSegmentFile(i))); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`DELETE FROM playback_hls_windows WHERE session_id=?`, session.ID); err != nil {
		t.Fatal(err)
	}

	// Seek to 90 seconds: slot 15, past the converted edge.
	seeked, err := s.HLSFileContext(ctx, grant, "segment-000015.ts")
	if err != nil {
		t.Fatal("seek past the converted edge failed", err)
	}
	if filepath.Base(seeked) != "segment-000015.ts" {
		t.Fatal("seek resolved to a different slot", seeked)
	}
	info, err := os.Stat(seeked)
	if err != nil || info.Size() == 0 {
		t.Fatal("seeked slot was not produced", err)
	}
	var restarted bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM playback_hls_windows WHERE session_id=? AND start_index=15)`, session.ID).Scan(&restarted); err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("conversion did not restart at the requested slot")
	}
	// Slots between the old edge and the seek stay absent: the restart did not
	// grind through the part the viewer skipped.
	if _, err = os.Stat(filepath.Join(dir, hlsSegmentFile(11))); err == nil {
		t.Fatal("restart converted the skipped region")
	}

	// Seeking back is a file read: the earlier output was never discarded.
	back, err := s.HLSFileContext(ctx, grant, "segment-000000.ts")
	if err != nil {
		t.Fatal("seek back failed", err)
	}
	backInfo, err := os.Stat(back)
	if err != nil {
		t.Fatal(err)
	}
	if backInfo.Size() != firstInfo.Size() || !backInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Fatal("seeking back re-cut an already-converted slot")
	}

	// A slot beyond the published timeline is not a seek, it is a bad request.
	if _, err = s.HLSFileContext(ctx, grant, "segment-000999.ts"); err == nil {
		t.Fatal("slot past the end of the title was served")
	}

	// The occurrence diagnostic reports the route and how far conversion got.
	// Progress is published by the window watcher, so give it one tick.
	for wait := time.Now().Add(10 * time.Second); time.Now().Before(wait); {
		var produced int64
		if err = db.QueryRow(`SELECT COALESCE(max(highest_produced),-1) FROM playback_hls_windows WHERE session_id=?`, session.ID).Scan(&produced); err != nil {
			t.Fatal(err)
		}
		if produced >= 15 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	delivery, err := SessionDeliveryTx(ctx, tx, fixedSettings{cfg}, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if delivery == nil || delivery.Mode != "hls" {
		t.Fatal("no delivery diagnostic published", delivery)
	}
	if delivery.Strategy != string(DeliveryCopyRemux) {
		t.Fatal("matroska h264/aac was not repackaged by copy", delivery.Strategy)
	}
	if len(delivery.ReasonCodes) == 0 || len(delivery.Streams) != 2 {
		t.Fatal("diagnostic is incomplete", delivery)
	}
	for _, stream := range delivery.Streams {
		if stream.Action != "copy" {
			t.Fatal("remux re-encoded a stream", stream)
		}
	}
	if delivery.ConvertedThroughMs <= 0 {
		t.Fatal("converted progress not published", delivery.ConvertedThroughMs)
	}
	if delivery.PlayedRetentionMs != 0 || delivery.ThrottleBufferMs != int64(cfg.ThrottleBufferSeconds)*1000 {
		t.Fatal("owner settings not reflected in the diagnostic", delivery)
	}
}

// Conversion that runs far ahead of the viewer is stopped, and the next demand
// restarts it. This is the throttle doing its job, not a failure.
func TestHLSThrottleStopsConversionAheadOfTheViewer(t *testing.T) {
	cfg := DefaultDeliveryConfiguration()
	cfg.ThrottleBufferSeconds = 10 // Two slots ahead is already too far.
	cfg.PlayedRetentionSeconds = 0
	var release string
	started := make(chan int, 2)
	cancelled := make(chan error, 2)
	ctx, db, s, h, session, grant := seekFixture(t, fixedSettings{cfg}, func(h *HLS) {
		fixtures := t.TempDir()
		release = filepath.Join(fixtures, "release")
		out, err := exec.CommandContext(h.ctx, h.binary, "-nostdin", "-v", "error", "-i", filepath.Join(filepath.Dir(h.root), "fixture.mkv"), "-c", "copy", "-f", "segment", "-segment_time", "6", filepath.Join(fixtures, "segment-%06d.ts")).CombinedOutput()
		if err != nil {
			t.Fatalf("segment fixtures: %v %s", err, out)
		}
		h.execute = func(ctx context.Context, job mediaexec.Job) (error, string) {
			var pattern string
			var from int
			for i, arg := range job.Args {
				if arg == "-hls_segment_filename" {
					pattern = job.Args[i+1]
				}
				if arg == "-start_number" || arg == "-segment_start_number" {
					var err error
					from, err = strconv.Atoi(job.Args[i+1])
					if err != nil {
						return err, err.Error()
					}
				}
			}
			if pattern == "" {
				pattern = job.Args[len(job.Args)-1]
			}
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHLSThrottleProducer$", "--", fixtures, pattern, strconv.Itoa(from))
			child.Env = append(os.Environ(), "PORTICO_TEST_THROTTLE_PRODUCER=1")
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, &output
			childprocess.Configure(child)
			started <- from
			err := childprocess.Run(ctx, child)
			if ctx.Err() != context.Canceled || err == nil || child.ProcessState == nil {
				cancelled <- fmt.Errorf("producer not cancelled and reaped: context=%v run=%v state=%v", ctx.Err(), err, child.ProcessState)
			} else {
				cancelled <- nil
			}
			return err, output.String()
		}
	})
	if err := s.Ready(ctx, session); err != nil {
		var diagnostic string
		if queryErr := db.QueryRow(`SELECT error FROM playback_artifacts WHERE session_id=?`, session.ID).Scan(&diagnostic); queryErr != nil {
			t.Fatal(err, queryErr)
		}
		t.Fatal(err, diagnostic)
	}
	if _, err := s.HLSFileContext(ctx, grant, "segment-000000.ts"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, []byte("produce ahead"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	var throttled bool
	for time.Now().Before(deadline) {
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM playback_hls_windows WHERE session_id=? AND status='throttled')`, session.ID).Scan(&throttled); err != nil {
			t.Fatal(err)
		}
		if throttled {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !throttled {
		t.Fatal("active producer was not durably throttled")
	}
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("throttle did not cancel its child", ctx.Err())
	}
	if from := <-started; from != 0 {
		t.Fatal("first producer start", from)
	}

	var demandThrottled bool
	if err := db.QueryRow(`SELECT throttled FROM playback_hls_demand WHERE session_id=?`, session.ID).Scan(&demandThrottled); err != nil || !demandThrottled {
		t.Fatal("throttle demand was not durable", demandThrottled, err)
	}
	if _, err := os.Stat(filepath.Join(h.root, session.ID, hlsSegmentFile(6))); !os.IsNotExist(err) {
		t.Fatal("resume segment must be unproduced", err)
	}
	// Demand for an absent slot must launch a second real producer child.
	resumed, err := s.HLSFileContext(ctx, grant, "segment-000006.ts")
	if err != nil {
		t.Fatal("throttled session did not resume on demand", err)
	}
	select {
	case from := <-started:
		if from != 6 {
			t.Fatal("resume producer start", from)
		}
	default:
		t.Fatal("resume did not start a second producer")
	}
	facts, err := (assets.Probe{}).Inspect(ctx, resumed)
	if err != nil || facts.VideoCodec != "h264" || facts.AudioCodec != "aac" || facts.Duration <= 0 {
		t.Fatalf("resumed output is not playable: %+v %v", facts, err)
	}
	var restarted bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM playback_hls_windows WHERE session_id=? AND start_index=6)`, session.ID).Scan(&restarted); err != nil || !restarted {
		t.Fatal("resume run was not recorded", restarted, err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, session.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "failed" {
		t.Fatal("throttling failed the session")
	}
}

// Runs only in a child selected by the throttle test. Publish real TS fixtures,
// then stay alive until the production watcher cancels and reaps this process.
func TestHLSThrottleProducer(t *testing.T) {
	if os.Getenv("PORTICO_TEST_THROTTLE_PRODUCER") != "1" {
		return
	}
	args := os.Args[len(os.Args)-3:]
	from, err := strconv.Atoi(args[2])
	if err != nil {
		t.Fatal(err)
	}
	for i := from; i < from+4; i++ {
		if from == 0 && i == 2 {
			for {
				_, err := os.Stat(filepath.Join(args[0], "release"))
				if err == nil {
					break
				}
				if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		raw, err := os.ReadFile(filepath.Join(args[0], hlsSegmentFile(i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fmt.Sprintf(args[1], i), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}
