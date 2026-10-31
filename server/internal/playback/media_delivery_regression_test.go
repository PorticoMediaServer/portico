package playback

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// Delay only the original producer's startup. Relocated windows invoke the real
// FFmpeg immediately, proving cancellation, target production and timestamps.
func delayInitialProducer(t *testing.T, h *HLS) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process wrapper")
	}
	path := filepath.Join(t.TempDir(), "slow-ffmpeg")
	script := "#!/bin/sh\nprev=''\nfor arg in \"$@\"; do\n if [ \"$prev\" = '-start_number' ] && [ \"$arg\" = '0' ]; then sleep 8; fi\n prev=\"$arg\"\ndone\nexec '" + strings.ReplaceAll(h.binary, "'", "'\\''") + "' \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	h.binary = path
}

// relocatedProducerStaysAhead keeps delayInitialProducer's slow first run, and
// makes every relocated run write its segments and then stay alive until the
// server cancels it, as a real converter running ahead of the viewer does. The
// throttle then always sees the window ahead.
func relocatedProducerStaysAhead(t *testing.T, h *HLS) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process wrapper")
	}
	path := filepath.Join(t.TempDir(), "ahead-ffmpeg")
	binary := "'" + strings.ReplaceAll(h.binary, "'", "'\\''") + "'"
	script := "#!/bin/sh\nprev=''\nfirst=''\nfor arg in \"$@\"; do\n if [ \"$prev\" = '-start_number' ] && [ \"$arg\" = '0' ]; then first=1; fi\n prev=\"$arg\"\ndone\nif [ -n \"$first\" ]; then sleep 8; exec " + binary + " \"$@\"; fi\n" + binary + " \"$@\" || exit $?\nexec sleep 300\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	h.binary = path
}

func TestActiveVideoDemandRelocatesOnceBeforeThrottle(t *testing.T) {
	for _, throttle := range []int{10, 60, 600} {
		t.Run(strconv.Itoa(throttle), func(t *testing.T) {
			cfg := DefaultDeliveryConfiguration()
			cfg.ThrottleBufferSeconds = throttle
			ctx, db, s, h, session, grant := seekFixture(t, fixedSettings{cfg})
			mustAudioExec(t, db, `UPDATE playback_delivery_plans SET video_action='convert',strategy='video_conversion',output_video='h264' WHERE session_id=?`, session.ID)
			delayInitialProducer(t, h)
			if err := s.Ready(ctx, session); err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			initial := h.windows[session.ID]
			h.mu.Unlock()
			if initial == nil {
				t.Fatal("initial producer is not active")
			}
			start := time.Now()
			var wg sync.WaitGroup
			errs := make(chan error, 24)
			for range 24 {
				wg.Add(1)
				go func() { defer wg.Done(); _, err := h.FileContext(ctx, grant, "segment-000015.ts"); errs <- err }()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			if time.Since(start) > 6*time.Second {
				t.Fatal("forward demand waited for old producer/throttle")
			}
			h.mu.Lock()
			budget := h.restarts[session.ID]
			count := 0
			if budget != nil {
				count = budget.count
			}
			h.mu.Unlock()
			if count != 1 {
				t.Fatalf("duplicate demand used %d restarts", count)
			}
			first, _ := videoSpan(t, filepath.Join(h.root, session.ID, "segment-000015.ts"))
			if math.Abs(first-90) > .1 {
				t.Fatalf("relocation changed source clock: %f", first)
			}
		})
	}
}

// The race behind the intermittent TestActiveVideoDemandRelocatesOnceBeforeThrottle/10,
// held open instead of left to timing: the relocated window throttles, and a
// serve lands while that window is still being torn down. It must not start
// production again.
func TestServeDuringThrottledTeardownRelocatesOnce(t *testing.T) {
	cfg := DefaultDeliveryConfiguration()
	cfg.ThrottleBufferSeconds = 10
	ctx, db, s, h, session, grant := seekFixture(t, fixedSettings{cfg})
	mustAudioExec(t, db, `UPDATE playback_delivery_plans SET video_action='convert',strategy='video_conversion',output_video='h264' WHERE session_id=?`, session.ID)
	relocatedProducerStaysAhead(t, h)
	held, release := make(chan *hlsWindow, 1), make(chan struct{})
	var hold, unhold sync.Once
	letGo := func() { unhold.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	h.windowTeardown = func(id string, window *hlsWindow) {
		h.mu.Lock()
		throttled := window.throttled
		h.mu.Unlock()
		if id == session.ID && throttled {
			hold.Do(func() { held <- window; <-release })
		}
	}
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for range 24 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := h.FileContext(ctx, grant, "segment-000015.ts"); errs <- err }()
	}
	var window *hlsWindow
	select {
	case window = <-held:
	case <-time.After(60 * time.Second):
		t.Fatal("the relocated window never throttled")
	}
	h.mu.Lock()
	registered := h.windows[session.ID] == window
	h.mu.Unlock()
	if !registered {
		t.Fatal("the throttled window is not held registered")
	}
	// The serve lands inside the teardown. Without the fix it spends a second
	// restart and then waits for this window to finish, so it can't return
	// until the hold is released.
	served := make(chan error, 1)
	go func() { _, err := h.FileContext(ctx, grant, "segment-000015.ts"); served <- err }()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
	}
	letGo()
	<-window.done
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	h.mu.Lock()
	budget := h.restarts[session.ID]
	count := 0
	if budget != nil {
		count = budget.count
	}
	h.mu.Unlock()
	if count != 1 {
		t.Fatalf("duplicate demand used %d restarts", count)
	}
}

func TestActiveRenditionDemandDoesNotPretendDelivery(t *testing.T) {
	ctx, s, h, session, grant := renditionFixture(t)
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	// Video already exists at the seek target, so its producer cannot mask the
	// audio bug by cancelling the sibling on its own throttle boundary.
	if _, err := h.FileContext(ctx, grant, "segment-000005.ts"); err != nil {
		t.Fatal(err)
	}
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	baselineRestarts := 0
	if budget := h.restarts[session.ID]; budget != nil {
		baselineRestarts = budget.count
	}
	h.mu.Unlock()
	delayInitialProducer(t, h)
	p, err := loadDeliveryPlan(context.Background(), h.db, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Renditions[1]
	if err = h.startRendition(ctx, session.ID, r, 0); err != nil {
		diagnostic, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var state, expires, failure string
		var generation, plans int
		queryErr := h.db.QueryRowContext(diagnostic, `SELECT ps.state,ps.generation,ps.expires_at,(SELECT count(*) FROM playback_delivery_plans WHERE session_id=ps.id),COALESCE((SELECT failure_code FROM playback_conversion_failures WHERE session_id=ps.id ORDER BY sequence DESC LIMIT 1),'') FROM playback_sessions ps WHERE ps.id=?`, session.ID).Scan(&state, &generation, &expires, &plans, &failure)
		t.Fatalf("start rendition: %v; session state=%q generation=%d expires=%q now=%q plan_rows=%d last_failure=%q diagnostic_query=%v", err, state, generation, expires, time.Now().UTC().Format(time.RFC3339Nano), plans, failure, queryErr)
	}
	key := renditionKey(session.ID, 1)
	h.mu.Lock()
	old := h.windows[key]
	h.mu.Unlock()
	if old == nil {
		t.Fatal("missing slow rendition")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	start := time.Now()
	for range 24 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := h.FileContext(ctx, grant, "audio-1-000005.ts"); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) > 6*time.Second {
		t.Fatal("audio seek waited for skipped production")
	}
	h.mu.Lock()
	served := old.served
	budget := h.restarts[session.ID]
	count := 0
	if budget != nil {
		count = budget.count
	}
	h.mu.Unlock()
	if served != -1 {
		t.Fatalf("missing audio was recorded as served: %d", served)
	}
	if count-baselineRestarts != 1 {
		t.Fatalf("duplicate audio demand used %d restarts", count-baselineRestarts)
	}
	if segmentReady(filepath.Join(h.root, session.ID, "audio-1-000001.ts")) {
		t.Fatal("encoded skipped interval")
	}
	// Immediate-ready and later-ready accounting share the same method. Keep
	// a live window for the cached path to observe that actual delivery moves it.
	h.mu.Lock()
	w := h.windows[key]
	if w == nil {
		w = &hlsWindow{start: 5, served: -1}
		h.windows[key] = w
	}
	h.mu.Unlock()
	if _, err = h.FileContext(ctx, grant, "audio-1-000005.ts"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	served = w.served
	if h.active[key] == nil {
		delete(h.windows, key)
	}
	h.mu.Unlock()
	if served != 5 {
		t.Fatalf("ready audio did not advance delivered progress: %d", served)
	}
}

func TestEvictedLiveSessionRecoversFromOrdinaryResourceRetry(t *testing.T) {
	ctx, _, s, h, session, grant := seekFixture(t, fixedSettings{DefaultDeliveryConfiguration()})
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FileContext(ctx, grant, "segment-000001.ts"); err != nil {
		t.Fatal(err)
	}
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	previous := hlsGlobalByteBudgetForTest(0)
	defer hlsGlobalByteBudgetForTest(previous)
	h.mu.Lock()
	h.usage[session.ID].served = time.Now().Add(-24 * time.Hour)
	h.mu.Unlock()
	h.enforceGlobalBudget(ctx)
	if _, err := os.Stat(filepath.Join(h.root, session.ID)); !os.IsNotExist(err) {
		t.Fatalf("session was not evicted: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		path, err := h.FileContext(ctx, grant, "segment-000001.ts")
		if err == nil {
			if !segmentReady(path) {
				t.Fatal("retry returned absent bytes")
			}
			break
		}
		if !errors.Is(err, ErrSegmentPreparing) || time.Now().After(deadline) {
			t.Fatalf("ordinary retry never recovered: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	// A stale generation marker cannot authorize existing bytes or restart.
	if err := os.WriteFile(filepath.Join(h.root, session.ID, "generation"), []byte("999"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FileContext(ctx, grant, "segment-000001.ts"); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatalf("stale generation accepted: %v", err)
	}
	h.mu.Lock()
	active := h.sessionActiveLocked(session.ID)
	h.mu.Unlock()
	if active {
		t.Fatal("stale generation restarted")
	}
	mustAudioExec(t, h.db, `UPDATE playback_sessions SET state='stopped' WHERE id=?`, session.ID)
	if err := os.RemoveAll(filepath.Join(h.root, session.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FileContext(ctx, grant, "segment-000001.ts"); err == nil || errors.Is(err, ErrSegmentPreparing) {
		t.Fatalf("revoked session retried: %v", err)
	}
}

func TestLongHLSTimelinesCoverWholeTitleWithinExplicitBound(t *testing.T) {
	for _, duration := range []float64{12*3600 - 1, 12 * 3600, 13*3600 + 1, 30 * 3600, 7 * 24 * 3600} {
		manifest := string(hlsTimelineManifest(duration))
		total := 0.0
		for _, line := range strings.Split(manifest, "\n") {
			if strings.HasPrefix(line, "#EXTINF:") {
				value, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
				if err != nil {
					t.Fatal(err)
				}
				total += value
			}
		}
		if math.Abs(total-duration) > 1e-6 {
			t.Fatalf("duration %.0f truncated to %.0f", duration, total)
		}
		last := hlsSegmentCount(duration) - 1
		if _, ok := hlsSegmentIndex(hlsSegmentFile(last)); !ok || !strings.Contains(manifest, hlsSegmentFile(last)) {
			t.Fatal("last index unreachable")
		}
		if len(manifest) > 6<<20 {
			t.Fatal("manifest exceeded safety ceiling")
		}
	}
	if err := writeTimelineManifest(t.TempDir(), 7*24*3600+1); err == nil {
		t.Fatal("over-limit title silently published")
	}
}

func TestDeliveryPlanPinsClientMaximumWidth(t *testing.T) {
	client := chromeProfile()
	client.Video[0].MaxWidth = 1920
	client.Video[0].MaxHeight = 1080
	p := decide(t, videoSource("mp4", "h264", assets.StreamDetail{Width: 2560, Height: 1080, BitDepth: 8, DynamicRange: "sdr"}, SourceAudio{Codec: "aac", Channels: 2}), client, nil)
	if p.VideoAction != "convert" || p.TargetWidth != 1920 {
		t.Fatalf("missing width limit: %+v", p)
	}
}

func TestDemandToleranceDoesNotRestartNearEdgeOrTargetStartup(t *testing.T) {
	w := hlsWindow{start: 5000, produced: 4999, served: -1}
	for _, target := range []int{5000, 5001, 5002} {
		if !w.coversDemand(target) {
			t.Fatal("restarted target startup", target)
		}
	}
	for _, target := range []int{4999, 5010} {
		if w.coversDemand(target) {
			t.Fatal("missed distant seek", target)
		}
	}
	w.produced = 5010
	if !w.coversDemand(5012) {
		t.Fatal("near-edge demand restarted")
	}
}

func TestCanceledRenditionPlanLoadKeepsAlternateAvailable(t *testing.T) {
	ctx, _, h, session, _ := renditionFixture(t)
	plan, err := loadDeliveryPlan(ctx, h.db, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := plan.Renditions[1]
	var generation int
	if err := h.db.QueryRowContext(ctx, `SELECT generation FROM playback_sessions WHERE id=?`, session.ID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	h.produceRendition(cancelled, session.ID, r, 0, &hlsWindow{}, generation)
	plan, err = loadDeliveryPlan(ctx, h.db, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Renditions[1] != r {
		t.Fatalf("cancelled plan load poisoned the shared rendition: %+v", plan.Renditions[1])
	}
}
