package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	binaryencoding "encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// The load test this is built on is good and it has two blind spots: it never
// fetches a media byte, and it injects no faults. Both of those are where the
// reliability audit's findings actually live — a four-slot supervisor cap and a
// missing write deadline are invisible to a workload that never opens a stream,
// and panic containment is invisible to a workload where nothing panics.
//
// So this one streams real bytes over real sockets, holds real event streams
// open, and breaks things on purpose while it does: clients that vanish
// mid-body, clients that read at a trickle, a saturated connection pool, and
// injected panics in the search fan-out. The pass criteria are the audit's.
//
//	PORTICO_PERFORMANCE_TIER=soak go test ./internal/httpapi -run TestSoak
//
// Defaults are sized to finish locally in about five minutes. Seconds, viewers,
// streams, event holds, catalogue size and stream byte rate are configurable.

// soakSettings is one run's shape.
type soakSettings struct {
	seconds              int
	viewers              int
	streams              int
	eventHolds           int
	catalogue            int
	faultPeriod          time.Duration
	streamBytesPerSecond int
	playbackProtocol     string
}

func soakConfiguration() soakSettings {
	s := soakSettings{seconds: 180, viewers: 200, streams: 100, eventHolds: 20, catalogue: 4000, faultPeriod: 5 * time.Second, streamBytesPerSecond: 1 << 20, playbackProtocol: "v1"}
	if value, err := strconv.Atoi(os.Getenv("PORTICO_SOAK_SECONDS")); err == nil && value > 0 {
		s.seconds = value
	}
	if value, err := strconv.Atoi(os.Getenv("PORTICO_SOAK_VIEWERS")); err == nil && value > 0 {
		s.viewers = value
	}
	if value, err := strconv.Atoi(os.Getenv("PORTICO_SOAK_CATALOGUE")); err == nil && value > 0 {
		s.catalogue = value
	}
	if value, err := strconv.Atoi(os.Getenv("PORTICO_SOAK_STREAMS")); err == nil && value > 0 {
		s.streams = value
	}
	if value, err := strconv.Atoi(os.Getenv("PORTICO_SOAK_EVENT_HOLDS")); err == nil && value >= 0 && os.Getenv("PORTICO_SOAK_EVENT_HOLDS") != "" {
		s.eventHolds = value
	}
	if value, err := strconv.Atoi(os.Getenv("PORTICO_SOAK_STREAM_BYTES_PER_SECOND")); err == nil && value > 0 {
		s.streamBytesPerSecond = value
	}
	return s
}

// soakShape scales the smoke catalogue to the requested size. The soak is about
// what happens to the process under load and faults, not about how a query
// behaves at a million rows — that is the deep tier's question — so it keeps the
// smoke shape's mix of shows, music, books, collections, credits and restricted
// profiles and grows the movie library to reach the requested count.
func soakShape(items int) fixture.Shape {
	shape := fixture.Smoke()
	fixed := shape.Items() - shape.Movies
	shape.Movies = max(200, items-fixed)
	shape.Seed = 11
	return shape
}

// soakSample is the process's state at one moment. The audit's leak criteria are
// all differences between two of these.
type soakSample struct {
	goroutines  int
	descriptors int
	heapBytes   uint64
	walBytes    int64
	walPeak     int64
	walState    dbwork.WALStats
	children    int
}

func takeSoakSample(ctx context.Context, db *sql.DB) soakSample {
	// Two collections and a short pause: the first frees what is unreachable, the
	// second lets finalisers run, and goroutines that are on their way out need a
	// moment to actually go.
	runtime.GC()
	time.Sleep(250 * time.Millisecond)
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	wal := dbwork.WAL(ctx, db)
	return soakSample{
		goroutines:  runtime.NumGoroutine(),
		descriptors: openDescriptors(),
		heapBytes:   memory.HeapInuse,
		walBytes:    wal.FileBytes,
		walPeak:     wal.PeakFileBytes,
		walState:    wal,
		children:    childProcesses(),
	}
}

// soakWALFileBytes observes physical allocation without running a checkpoint.
func soakWALFileBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// openDescriptors counts this process's open files. /dev/fd is the portable-
// enough answer on the platforms this runs on; where it is not readable the
// criterion is reported as unavailable rather than guessed.
func openDescriptors() int {
	// Reading the directory entries of /dev/fd on macOS fails on the entries
	// themselves, so the names are read without stat-ing them.
	directory, err := os.Open("/dev/fd")
	if err == nil {
		names, readErr := directory.Readdirnames(-1)
		directory.Close()
		if readErr == nil {
			return len(names)
		}
	}
	out, err := exec.Command("lsof", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return -1
	}
	return len(strings.Split(strings.TrimSpace(string(out)), "\n")) - 1
}

// childProcesses counts processes this one started and has not reaped: the
// orphan criterion.
func childProcesses() int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// soakMedia produces a real media file to stream. ffmpeg is the only way to get
// one that the probe and the container checks accept, so a host without it skips
// the streaming leg and says so rather than pretending.
func soakMedia(t *testing.T) string {
	t.Helper()
	if supplied := os.Getenv("PORTICO_SOAK_MEDIA"); supplied != "" {
		return supplied
	}
	binary := os.Getenv("PORTICO_FFMPEG")
	if binary == "" {
		binary = "ffmpeg"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		for _, candidate := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/usr/bin/ffmpeg"} {
			if _, statErr := os.Stat(candidate); statErr == nil {
				resolved = candidate
				break
			}
		}
	}
	if resolved == "" {
		t.Log("ffmpeg is not available; the soak runs without the media-byte leg")
		return ""
	}
	// Not t.TempDir: the fixture copies it, and generating it once per run is
	// enough. A few-megabyte file fits socket buffers: paced client readers can
	// appear concurrent after every server handler has already finished. Append
	// a valid MP4 free box so each paced body remains active server-side.
	directory, err := os.MkdirTemp("", "portico-soak-media-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "soak.mp4")
	build, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(build, resolved,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "96k", "-movflags", "+faststart", path)
	if err = command.Run(); err != nil {
		t.Logf("ffmpeg could not produce a soak media file (%v); the soak runs without the media-byte leg", err)
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() < 1<<18 {
		t.Log("the generated soak media file is too small to stream; the soak runs without the media-byte leg")
		return ""
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	const padding = 64 << 20
	header := make([]byte, 8)
	binaryencoding.BigEndian.PutUint32(header[:4], padding)
	copy(header[4:], "free")
	_, err = file.Write(header)
	if err == nil {
		err = file.Truncate(info.Size() + padding)
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("padding soak media: write=%v close=%v", err, closeErr)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("soak media: %s (%d KiB)", path, info.Size()>>10)
	return path
}

func TestSoakUnderFaultsAndRealMediaBytes(t *testing.T) {
	if os.Getenv("PORTICO_PERFORMANCE_TIER") != "soak" {
		t.Skip("set PORTICO_PERFORMANCE_TIER=soak to run the soak and chaos tier")
	}
	if raceDetector {
		t.Skip("the soak tier's budgets are not meaningful under the race detector")
	}
	settings := soakConfiguration()
	media := soakMedia(t)
	if media == "" {
		t.Skip("the streaming capacity tier requires real media; install ffmpeg or supply PORTICO_SOAK_MEDIA")
	}
	dbwork.ResetProbes()
	t.Cleanup(dbwork.ResetProbes)
	lockEscapes.Store(0)

	shape := soakShape(settings.catalogue)
	tier := performanceTier{name: "soak", shape: shape, catalogItems: shape.Items(), concurrentViewers: settings.viewers, iterations: 1, think: 250 * time.Millisecond, mediaPath: media, capabilities: true}
	f := newLoadFixture(t, tier)

	// Real sockets, not a recorder: a write deadline, a client that stops reading
	// and a client that vanishes mid-body are all properties of a socket, and a
	// test that never opens one cannot see any of them.
	server := httptest.NewServer(f.handler)
	defer server.Close()

	panicsBefore := supervise.Panics()
	baseline := takeSoakSample(context.Background(), f.db)
	walFile := dbwork.DatabaseFile(context.Background(), f.db) + "-wal"
	var physicalWALPeak atomic.Int64
	samplePhysicalWAL := func() int64 {
		size := soakWALFileBytes(walFile)
		for previous := physicalWALPeak.Load(); size > previous; previous = physicalWALPeak.Load() {
			if physicalWALPeak.CompareAndSwap(previous, size) {
				break
			}
		}
		return size
	}
	readsBefore := dbwork.Reads()
	cacheBefore := readsBefore.StatementCache
	gateBefore, poolBefore := dbwork.WriteGate().Stats(), dbwork.Pool(f.db)
	// Fixture construction is not request load and must not set its maximum
	// transaction hold or inflate request-phase wait/cache counters.
	dbwork.WriteGate().ResetPeak()
	ResetRouteCosts()
	t.Logf("baseline: goroutines=%d fds=%d heap=%d KiB wal=%d KiB children=%d",
		baseline.goroutines, baseline.descriptors, baseline.heapBytes>>10, baseline.walBytes>>10, baseline.children)
	t.Logf("baseline WAL/readers/checkpoints: %s", mustSoakJSON(baseline.walState))
	if profilePath := os.Getenv("PORTICO_SOAK_CPU_PROFILE"); profilePath != "" {
		profile, err := os.Create(profilePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(profile); err != nil {
			profile.Close()
			t.Fatal(err)
		}
		defer func() { pprof.StopCPUProfile(); profile.Close() }()
	}

	ctx, stop := context.WithTimeout(context.Background(), time.Duration(settings.seconds)*time.Second)
	defer stop()

	// The first third runs clean, so "zero refusals at target load" is measured
	// where it means something; faults start after it.
	quiet := time.Duration(settings.seconds/3) * time.Second
	faultsFrom := time.Now().Add(quiet)
	recoveryFrom := faultsFrom.Add(quiet)
	isClean := func() bool { now := time.Now(); return now.Before(faultsFrom) || !now.Before(recoveryFrom) }

	var scanned, refreshed atomic.Int64
	var background sync.WaitGroup
	background.Add(2)
	supervise.Go("soak.scan", func() {
		defer background.Done()
		runBulkWriter(ctx, f.db, "scan", func(ctx context.Context, tx *sql.Tx, n int) error {
			return tl6BulkMovieTx(ctx, tx, f.bulkLibrary, fmt.Sprintf("soak-%06d", n), fmt.Sprintf("Soaked %06d", n), 2001, "")
		}, &scanned)
	})
	supervise.Go("soak.metadata", func() {
		defer background.Done()
		runBulkWriter(ctx, f.db, "metadata", func(ctx context.Context, tx *sql.Tx, n int) error {
			id := 1 + n%256
			return tl6BulkMovieTx(ctx, tx, f.bulkLibrary, fmt.Sprintf("soak-%06d", id), fmt.Sprintf("Soaked %06d", id), 2001, fmt.Sprintf("soaked %d", n))
		}, &refreshed)
	})

	observed := &soakObservations{codes: map[string]int{}, caps: map[string]int{}, byLabel: map[string]int{}, phaseAt: func() string {
		now := time.Now()
		if now.Before(faultsFrom) {
			return "quiet"
		}
		if now.Before(recoveryFrom) {
			return "fault"
		}
		return "recovery"
	}}
	var work sync.WaitGroup
	work.Add(1)
	supervise.Go("soak.capacity-observer", func() {
		defer work.Done()
		var samples int
		for ctx.Err() == nil {
			samples++
			physicalBytes := samplePhysicalWAL()
			if samples%20 == 0 {
				t.Logf("reader/WAL sample phase=%s physicalBytes=%d readers=%s", observed.phaseAt(), physicalBytes, mustSoakJSON(dbwork.ReaderLifetimes()))
			}
			code, _, raw := f.callTimed(f.owner.AccessToken, "GET", "/v1/admin/diagnostics/concurrency", nil)
			var diagnostics ConcurrencyDiagnostics
			if code == 200 && json.Unmarshal([]byte(raw), &diagnostics) == nil {
				for _, lane := range diagnostics.Lanes {
					observed.sampleLane(lane)
					if lane.Lane == laneMediaBody {
						observed.sampleStreams(lane.Active, settings.streams, isClean(), !time.Now().Before(recoveryFrom))
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	})

	// Use production's shared scheduler: ordinary one-second polls only stat the
	// file; its regular/pressure/backoff policy decides when SQL runs.
	checkpointSchedule, scheduleErr := dbwork.NewCheckpointSchedule(ctx, f.db)
	if scheduleErr != nil {
		t.Logf("checkpoint native-path lookup: %v; regular/retry policy remains active", scheduleErr)
	}
	work.Add(1)
	supervise.Go("soak.checkpoint", func() {
		defer work.Done()
		ticker := time.NewTicker(dbwork.CheckpointPollInterval)
		defer ticker.Stop()
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			beforeBytes := samplePhysicalWAL()
			result, ran := checkpointSchedule.Tick(ctx)
			if !ran {
				continue
			}
			t.Logf("checkpoint phase=%s physicalBefore=%d physicalAfter=%d logicalBacklogBefore=%d result=%s", observed.phaseAt(), beforeBytes, samplePhysicalWAL(), max(0, result.BeforeLogFrames-result.BeforeCheckpointedFrames), mustSoakJSON(result))
		}
	})

	// Viewers: the ordinary browsing and playback workload, run round after round
	// until the window closes.
	for index := 0; index < settings.viewers; index++ {
		work.Add(1)
		index := index
		supervise.Go("soak.viewer", func() {
			defer work.Done()
			// Spread arrival over two seconds. A synchronized restart storm is a
			// separate overload shape, not the steady browsing capacity claim.
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(index) * 2 * time.Second / time.Duration(settings.viewers)):
			}
			// A viewer that waits longer than this has already given up in any real
			// sense, and a round that never returns is a round whose remaining
			// requests never happen — which would quietly empty the workload.
			client := &http.Client{Timeout: 8 * time.Second}
			token := f.viewers[index%len(f.viewers)].AccessToken
			for ctx.Err() == nil {
				soakViewerRound(ctx, client, server.URL, token, f, index, observed, isClean())
			}
		})
	}

	// Media streams: real byte ranges held open for the window, which is what
	// makes the supervisor pool, the media-body lane and the rolling write
	// deadline all real.
	if media != "" {
		for index := 0; index < settings.streams; index++ {
			work.Add(1)
			index := index
			supervise.Go("soak.stream", func() {
				defer work.Done()
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(index) * 2 * time.Second / time.Duration(settings.streams)):
				}
				client := &http.Client{Timeout: 60 * time.Second}
				token := f.viewers[index%len(f.viewers)].AccessToken
				for ctx.Err() == nil {
					soakStream(ctx, client, server.URL, token, f, index, observed, isClean, settings.streamBytesPerSecond, settings.playbackProtocol)
				}
			})
		}
	}

	// Event streams: twenty clients holding a notification stream, which is what
	// the raised stream cap and the per-frame write deadline are for.
	for index := 0; index < settings.eventHolds; index++ {
		work.Add(1)
		index := index
		supervise.Go("soak.events", func() {
			defer work.Done()
			client := &http.Client{Timeout: 0}
			token := f.viewers[index%len(f.viewers)].AccessToken
			for ctx.Err() == nil {
				soakHoldEvents(ctx, client, server.URL, token, observed)
			}
		})
	}

	// Faults, one every few seconds, once the quiet phase is over.
	work.Add(1)
	supervise.Go("soak.faults", func() {
		defer work.Done()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(faultsFrom)):
		}
		// Panic injection runs for the whole fault phase rather than in bursts:
		// the audit's shape is a small constant fraction of search goroutines, HLS
		// producers and background batches failing while everything else carries
		// on, and a burst would let a quiet second look like a pass.
		restoreChaos := supervise.SetChaosForTest("catalog.search.group:1.0,playback.hls.produce:0.10,ingestion.quantum:0.02")
		defer restoreChaos()
		fired := 0
		for probe := 0; probe < 200; probe++ {
			func() {
				defer func() {
					if recover() != nil {
						fired++
					}
				}()
				supervise.Chaos("catalog.search.group")
			}()
		}
		t.Logf("chaos self-check: %d of 200 probes fired", fired)
		injected := 0
		for ctx.Err() == nil && time.Now().Before(recoveryFrom) {
			soakFault(ctx, t, f, server.URL, injected, observed)
			injected++
			select {
			case <-ctx.Done():
			case <-time.After(settings.faultPeriod):
			}
		}
		t.Logf("faults injected: %d", injected)
		restoreChaos()
		// The final third must recover while clients remain active; a process
		// surviving faults alone does not establish usable recovery.
		client := &http.Client{Timeout: 8 * time.Second}
		for ctx.Err() == nil {
			soakCall(ctx, client, server.URL, f.owner.AccessToken, "recovery-home", "GET", "/v1/home", nil, observed, true)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	})

	work.Wait()
	stop()
	background.Wait()
	// Remove this harness's client keep-alives and accept machinery before
	// comparing at-rest descriptors with the pre-client baseline.
	server.Close()
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	// Let everything that was in flight actually finish before measuring at rest.
	time.Sleep(10 * time.Second)
	rest := takeSoakSample(context.Background(), f.db)
	if code, _, raw := f.callTimed(f.owner.AccessToken, "GET", "/v1/admin/diagnostics/concurrency", nil); code == 200 {
		var diagnostics ConcurrencyDiagnostics
		if json.Unmarshal([]byte(raw), &diagnostics) == nil {
			for _, lane := range diagnostics.Lanes {
				observed.sampleLane(lane)
			}
		}
	}

	report := observed.snapshot()
	integrity := dbwork.Integrity(context.Background(), f.db)
	gate := dbwork.WriteGate().Stats()
	pool := dbwork.Pool(f.db)
	gate.Acquired -= gateBefore.Acquired
	gate.Queued -= gateBefore.Queued
	pool.WaitCount -= poolBefore.WaitCount
	pool.WaitMillis -= poolBefore.WaitMillis
	cache := dbwork.Reads().StatementCache
	cache.Hits -= cacheBefore.Hits
	cache.Misses -= cacheBefore.Misses
	cache.Bypasses -= cacheBefore.Bypasses
	cache.Evictions -= cacheBefore.Evictions

	t.Logf("soak: %d s, %d viewers, %d streams, %d event holds, catalogue %d",
		settings.seconds, settings.viewers, settings.streams, settings.eventHolds, settings.catalogue)
	t.Logf("playback protocol: %s", settings.playbackProtocol)
	t.Logf("requests=%d p50=%s p95=%s p99=%s", report.total, report.p50, report.p95, report.p99)
	t.Logf("refusals: quiet=%d faulted=%d fixedCeilings=%v failures=%d clientTimeouts=%d mediaBytes=%d MiB",
		report.quietRefusals, report.faultedRefusals, report.caps, report.failures, report.timeouts, report.mediaBytes>>20)
	t.Logf("deliberately abandoned mid-flight: %d", report.abandoned)
	t.Logf("actual server media-body concurrency: peak=%d cleanSamples=%d cleanSamplesAtTarget=%d recoverySamplesAtTarget=%d", report.streamPeak, report.cleanStreamSamples, report.cleanStreamsAtTarget, report.recoveryStreamsAtTarget)
	t.Logf("consecutive samples at stream target: clean=%d recovery=%d; distinct readers consuming at least 1MiB=%d", report.cleanTargetStreakPeak, report.recoveryTargetStreakPeak, report.streamReaders)
	t.Logf("clean requests=%d p95=%s p99=%s", report.cleanRequests, report.cleanP95, report.cleanP99)
	t.Logf("normal client retries=%d; clean logical requests=%d unresolvedFailures=%d endToEndP95=%s (raw refusals remain gated)", report.clientRetries, report.logicalCleanRequests, report.logicalCleanFailures, report.logicalCleanP95)
	t.Logf("at rest: goroutines %d -> %d, fds %d -> %d, heap %d -> %d KiB, children %d -> %d",
		baseline.goroutines, rest.goroutines, baseline.descriptors, rest.descriptors, baseline.heapBytes>>10, rest.heapBytes>>10, baseline.children, rest.children)
	samplePhysicalWAL()
	observedWALPeak := max(rest.walPeak, physicalWALPeak.Load())
	t.Logf("wal peak %d KiB, integrity quick_check=%q foreignKeyViolations=%d", observedWALPeak>>10, integrity.QuickCheck, integrity.ForeignKeyViolations)
	t.Logf("physical WAL peak sampled=%d bytes, diagnostic remembered=%d bytes", physicalWALPeak.Load(), rest.walPeak)
	t.Logf("at-rest WAL/readers/checkpoints: %s", mustSoakJSON(rest.walState))
	t.Logf("background committed batches: scan=%d metadata=%d (10 rows each)", scanned.Load(), refreshed.Load())
	t.Logf("gate acquired=%d queued=%d maxHeld=%dms, pool waits=%d/%dms",
		gate.Acquired, gate.Queued, gate.MaxHeldMilli, pool.WaitCount, pool.WaitMillis)
	t.Logf("request-phase statement cache: hits=%d misses=%d bypasses=%d evictions=%d", cache.Hits, cache.Misses, cache.Bypasses, cache.Evictions)
	t.Logf("achieved HTTP attempts/s=%.1f; normal clean logical calls/s=%.1f; successful clean logical calls/s=%.1f", float64(report.total)/float64(settings.seconds), float64(report.logicalCleanRequests)/(float64(settings.seconds)*2/3), float64(report.logicalCleanRequests-report.logicalCleanFailures)/(float64(settings.seconds)*2/3))
	for _, phase := range []string{"quiet", "fault", "recovery"} {
		if p, ok := report.phases[phase]; ok {
			t.Logf("phase %s: rawAttempts=%d refusals=%d hardFailures=%d p95=%s p99=%s logicalRequests=%d unresolved=%d endToEndP95=%s", phase, p.attempts, p.refusals, p.failures, p.p95, p.p99, p.logicalRequests, p.logicalFailures, p.logicalP95)
		}
	}
	routeLabels := make([]string, 0, len(report.routes))
	for label := range report.routes {
		routeLabels = append(routeLabels, label)
	}
	sort.Strings(routeLabels)
	for _, label := range routeLabels {
		r := report.routes[label]
		t.Logf("clean route latency %s: raw=%d p50=%s p95=%s p99=%s successful=%d p50=%s p95=%s p99=%s", label, r.attempts, r.rawP50, r.rawP95, r.rawP99, r.successes, r.successP50, r.successP95, r.successP99)
	}
	beforeClasses := map[string]dbwork.ClassReadStats{}
	for _, class := range readsBefore.ByClass {
		beforeClasses[class.Class] = class
	}
	for _, class := range dbwork.Reads().ByClass {
		before := beforeClasses[class.Class]
		t.Logf("work class %s: statements=%d measuredStatementMs=%d", class.Class, class.Statements-before.Statements, class.TotalMs-before.TotalMs)
	}
	for _, route := range RouteCosts() {
		if route.Requests > 0 {
			t.Logf("route cost %s: requests=%d statementsMean=%.1f statementsMax=%d acquisitionsMean=%.1f acquisitionsMax=%d", route.Route, route.Requests, route.MeanStatements(), route.MaxStatements, route.MeanAcquisitions(), route.MaxAcquisitions)
		}
	}
	for _, lane := range report.lanes {
		t.Logf("lane %s: activePeak=%d waitingPeak=%d final=%s", lane.last.Lane, lane.activePeak, lane.waitingPeak, mustSoakJSON(lane.last))
	}
	contained := supervise.Panics() - panicsBefore
	t.Logf("contained panics: %d (injected throughout the fault phase)", contained)
	for _, line := range report.transportErrors {
		t.Logf("  client error %s", line)
	}
	byLabel := make([]string, 0, len(report.byLabel))
	for key := range report.byLabel {
		byLabel = append(byLabel, key)
	}
	sort.Strings(byLabel)
	for _, key := range byLabel {
		t.Logf("  requests %s x%d", key, report.byLabel[key])
	}
	if len(report.codes) > 0 {
		keys := make([]string, 0, len(report.codes))
		for key := range report.codes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			t.Logf("  %s x%d", key, report.codes[key])
		}
	}

	// The audit's pass criteria, in its order.
	if lockEscapes.Load() != 0 {
		t.Errorf("%d operations escaped as SQLITE_BUSY or SQLITE_LOCKED", lockEscapes.Load())
	}
	if integrity.QuickCheck != "ok" {
		t.Errorf("quick_check after the soak: %q", integrity.QuickCheck)
	}
	if integrity.ForeignKeyViolations != 0 {
		t.Errorf("the soak left %d orphaned rows: %v", integrity.ForeignKeyViolations, integrity.ByChildTable)
	}
	// A refusal caused by a fixed ceiling is always a defect: those ceilings are
	// the server's own numbers and no legitimate viewer may meet one.
	if len(report.caps) > 0 {
		t.Errorf("fixed ceilings refused legitimate requests: %v", report.caps)
	}
	// Fault-phase shedding is expected. Clean-load shedding means the configured
	// browsing/stream target has not been demonstrated, even if the refusal was
	// honest and the process remained healthy.
	if report.quietRefusals != 0 {
		t.Errorf("%d of %d clean requests were refused at %d viewers; target capacity has not been established", report.quietRefusals, report.cleanRequests, settings.viewers)
	}
	if report.streamPeak < int64(settings.streams) || report.cleanTargetStreakPeak < 20 || report.recoveryTargetStreakPeak < 8 || report.streamReaders < settings.streams {
		t.Errorf("configured %d streams were not sustained server-side during clean load and recovery: peak=%d cleanTargetSamples=%d recoveryTargetSamples=%d (250ms samples)", settings.streams, report.streamPeak, report.cleanStreamsAtTarget, report.recoveryStreamsAtTarget)
	}
	if report.cleanP95 > 750*time.Millisecond || report.cleanP99 > 1500*time.Millisecond {
		t.Errorf("clean-load latency target missed: p95=%s p99=%s", report.cleanP95, report.cleanP99)
	}
	if report.recoverySuccesses < 5 {
		t.Errorf("usable recovery was not demonstrated: %d successful recovery Home requests", report.recoverySuccesses)
	}
	if report.failures != 0 {
		t.Errorf("%d requests failed for a reason other than admission or revision movement", report.failures)
	}
	// A malformed request is the client's mistake and must be answered as one.
	// A 500 says the server broke, which under load is the shape a crash arrives
	// in — and the hostile-client fault exists precisely to find one.
	if len(report.serverErrors) > 0 {
		t.Errorf("hostile input produced %d server errors:\n%s", len(report.serverErrors), strings.Join(report.serverErrors, "\n"))
	}
	// A client that gave up waiting is this host running out of breath rather
	// than the server answering wrongly, so it is bounded rather than forbidden.
	if report.timeouts*100 > report.total {
		t.Errorf("%d of %d requests never completed; more than one in a hundred clients gave up waiting", report.timeouts, report.total)
	} else if report.timeouts != 0 {
		t.Logf("%d of %d requests never completed; this host's headroom under the fault phase", report.timeouts, report.total)
	}
	// The audit's criterion is 110% of the baseline. The baseline here is a
	// handful of goroutines — the fixture before any client exists — so a
	// percentage of it is noise; sixteen is the absolute headroom that means "the
	// same shape, plus the server's own accept machinery" rather than "a leak".
	if baseline.goroutines > 0 && rest.goroutines > baseline.goroutines*110/100+16 {
		t.Errorf("goroutines at rest %d against a baseline of %d", rest.goroutines, baseline.goroutines)
	}
	if baseline.descriptors > 0 && rest.descriptors > baseline.descriptors+16 {
		t.Errorf("open descriptors at rest %d against a baseline of %d", rest.descriptors, baseline.descriptors)
	}
	if rest.children > baseline.children {
		t.Errorf("%d child processes outlived the run (baseline %d)", rest.children, baseline.children)
	}
	if observedWALPeak > 256<<20 {
		t.Errorf("the write-ahead log peaked at %d MiB", observedWALPeak>>20)
	}
	// The injection has to have actually happened, or "the process survived" is a
	// statement about a run where nothing was broken.
	if contained == 0 {
		t.Errorf("no panic was injected during the fault phase; the chaos seam did not fire")
	}
	if scanned.Load() == 0 || refreshed.Load() == 0 {
		t.Errorf("background work was starved: scan=%d refresh=%d", scanned.Load(), refreshed.Load())
	}
}

// soakObservations collects what every client saw.
type soakObservations struct {
	routes                   map[string]*soakRouteLatencyObservations
	lanes                    map[string]soakLaneReport
	phaseAt                  func() string
	phases                   map[string]*soakPhaseObservations
	mu                       sync.Mutex
	latencies                []time.Duration
	cleanLatencies           []time.Duration
	streamPeak               int64
	cleanStreamSamples       int
	cleanStreamsAtTarget     int
	recoveryStreamsAtTarget  int
	streamReaderBytes        map[int]int64
	recoverySuccesses        int
	cleanTargetStreak        int
	cleanTargetStreakPeak    int
	recoveryTargetStreak     int
	recoveryTargetStreakPeak int
	clientRetries            int
	logicalCleanLatencies    []time.Duration
	logicalCleanFailures     int
	codes                    map[string]int
	caps                     map[string]int
	byLabel                  map[string]int
	transportErrors          []string
	quietRefusals            int
	faultedRefusals          int
	failures                 int
	timeouts                 int
	abandoned                int
	mediaBytes               int64
	// serverErrors are 5xx answers other than 503 to deliberately malformed
	// requests. A malformed request is the client's mistake and must be answered
	// as one; a 500 says the server broke, and under load is the shape a crash
	// arrives in.
	serverErrors []string
}

type soakPhaseObservations struct {
	latencies, logicalLatencies         []time.Duration
	refusals, failures, logicalFailures int
}

type soakPhaseReport struct {
	attempts, refusals, failures     int
	p95, p99                         time.Duration
	logicalRequests, logicalFailures int
	logicalP95                       time.Duration
}

type soakRouteLatencyObservations struct{ cleanRaw, cleanSuccess []time.Duration }
type soakRouteLatencyReport struct {
	attempts, successes                int
	rawP50, rawP95, rawP99             time.Duration
	successP50, successP95, successP99 time.Duration
}

type soakReport struct {
	routes                   map[string]soakRouteLatencyReport
	lanes                    []soakLaneReport
	phases                   map[string]soakPhaseReport
	total                    int
	p50, p95, p99            time.Duration
	cleanRequests            int
	cleanP95, cleanP99       time.Duration
	streamPeak               int64
	cleanStreamSamples       int
	cleanStreamsAtTarget     int
	recoveryStreamsAtTarget  int
	streamReaders            int
	recoverySuccesses        int
	cleanTargetStreakPeak    int
	recoveryTargetStreakPeak int
	clientRetries            int
	logicalCleanRequests     int
	logicalCleanFailures     int
	logicalCleanP95          time.Duration
	codes                    map[string]int
	caps                     map[string]int
	byLabel                  map[string]int
	transportErrors          []string
	quietRefusals            int
	faultedRefusals          int
	failures                 int
	timeouts                 int
	abandoned                int
	mediaBytes               int64
	serverErrors             []string
}

type soakLaneReport struct {
	last        LaneDiagnostics
	activePeak  int64
	waitingPeak int
}

func mustSoakJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "unavailable"
	}
	return string(raw)
}

func (o *soakObservations) sampleLane(lane LaneDiagnostics) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.lanes == nil {
		o.lanes = map[string]soakLaneReport{}
	}
	report := o.lanes[lane.Lane]
	report.last = lane
	report.activePeak = max(report.activePeak, lane.Active)
	report.waitingPeak = max(report.waitingPeak, lane.Waiting)
	o.lanes[lane.Lane] = report
}

// hostile records a deliberately malformed request's answer. A 4xx here is the
// correct outcome, not a failure: the whole point of the fault is that the
// server rejects nonsense cleanly, so counting those rejections as failures
// would make the criterion assert the opposite of what it means.
func (o *soakObservations) hostile(code int, elapsed time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.latencies = append(o.latencies, elapsed)
	o.byLabel["hostile-input"]++
	if code != 0 {
		o.codes[fmt.Sprintf("hostile-input=%d", code)]++
	}
}

// serverError records a 5xx that is not an honest 503.
func (o *soakObservations) serverError(label, target string, code int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.serverErrors) < 16 {
		o.serverErrors = append(o.serverErrors, fmt.Sprintf("%s %s -> %d", label, target, code))
	}
}

// capRefusal marks a refusal caused by a fixed ceiling rather than by lane
// admission. Those are the ones the audit's criterion exists to catch: a
// four-slot helper pool and a sixty-four-stream notification cap are arbitrary
// numbers far below the target, and no legitimate viewer may ever meet one.
func (o *soakObservations) capRefusal(label, body string) bool {
	for _, code := range []string{"playback_source_busy", "too_many_streams", "capacity", "conversion_capacity"} {
		if strings.Contains(body, code) {
			o.mu.Lock()
			o.caps[label+":"+code]++
			o.mu.Unlock()
			return true
		}
	}
	return false
}

func (o *soakObservations) record(label string, code int, elapsed time.Duration, quiet, windowClosed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.phaseAt != nil {
		quiet = o.phaseAt() != "fault"
	}
	o.latencies = append(o.latencies, elapsed)
	o.byLabel[label]++
	if !windowClosed {
		if phase := o.phaseLocked(); phase != nil {
			phase.latencies = append(phase.latencies, elapsed)
			if code == 429 || code == 503 {
				phase.refusals++
			} else if code == 0 || code < 200 || (code >= 300 && code != 409) {
				phase.failures++
			}
		}
	}
	if quiet && !windowClosed {
		o.cleanLatencies = append(o.cleanLatencies, elapsed)
		if o.routes == nil {
			o.routes = map[string]*soakRouteLatencyObservations{}
		}
		if o.routes[label] == nil {
			o.routes[label] = &soakRouteLatencyObservations{}
		}
		route := o.routes[label]
		route.cleanRaw = append(route.cleanRaw, elapsed)
		if code >= 200 && code < 300 {
			route.cleanSuccess = append(route.cleanSuccess, elapsed)
		}
	}
	if code >= 200 && code <= 299 {
		if label == "recovery-home" {
			o.recoverySuccesses++
		}
		return
	}
	o.codes[fmt.Sprintf("%s=%d", label, code)]++
	switch code {
	case 429, 503:
		if quiet {
			o.quietRefusals++
		} else {
			o.faultedRefusals++
		}
	case 409:
		// The catalogue moved under a reader and the server said so. That is the
		// design working, not a failure.
	case 0:
		// The request never completed: a client-side timeout or a broken
		// connection. Counted apart from a server answer, because the server may
		// have been perfectly willing and this host simply out of breath — and
		// apart again when the client abandoned it deliberately, which is a fault
		// being injected rather than anything going wrong.
		if label == "cancelled" || windowClosed {
			o.abandoned++
		} else {
			o.timeouts++
		}
	default:
		o.failures++
	}
}

// transport keeps the first few client-side errors verbatim. A status of zero
// says only that the request never completed; the reason is the interesting part.
func (o *soakObservations) transport(label string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.transportErrors) < 8 {
		o.transportErrors = append(o.transportErrors, label+": "+err.Error())
	}
}

func (o *soakObservations) bytes(n int64) {
	o.mu.Lock()
	o.mediaBytes += n
	o.mu.Unlock()
}

func (o *soakObservations) sampleStreams(active int64, target int, clean, recovery bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.streamPeak = max(o.streamPeak, active)
	if clean {
		o.cleanStreamSamples++
		if active >= int64(target) {
			o.cleanTargetStreak++
			o.cleanTargetStreakPeak = max(o.cleanTargetStreakPeak, o.cleanTargetStreak)
			o.cleanStreamsAtTarget++
			if recovery {
				o.recoveryTargetStreak++
				o.recoveryTargetStreakPeak = max(o.recoveryTargetStreakPeak, o.recoveryTargetStreak)
				o.recoveryStreamsAtTarget++
			}
		}
	}
	if !clean || active < int64(target) {
		o.cleanTargetStreak = 0
	}
	if !recovery || active < int64(target) {
		o.recoveryTargetStreak = 0
	}
}

func (o *soakObservations) streamRead(index int, n int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.streamReaderBytes == nil {
		o.streamReaderBytes = map[int]int64{}
	}
	o.streamReaderBytes[index] += n
}

func (o *soakObservations) retry() {
	o.mu.Lock()
	o.clientRetries++
	o.mu.Unlock()
}

func (o *soakObservations) logical(code int, elapsed time.Duration, clean, windowClosed bool) {
	if windowClosed {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.phaseAt != nil {
		clean = o.phaseAt() != "fault"
	}
	if phase := o.phaseLocked(); phase != nil {
		phase.logicalLatencies = append(phase.logicalLatencies, elapsed)
		if code == 0 || code == 429 || code >= 500 {
			phase.logicalFailures++
		}
	}
	if !clean {
		return
	}
	o.logicalCleanLatencies = append(o.logicalCleanLatencies, elapsed)
	if code == 0 || code == 429 || code >= 500 {
		o.logicalCleanFailures++
	}
}

// phaseLocked classifies completion time independently of a request/round's
// initial phase, so a retry crossing a phase boundary remains observable.
func (o *soakObservations) phaseLocked() *soakPhaseObservations {
	if o.phaseAt == nil {
		return nil
	}
	if o.phases == nil {
		o.phases = map[string]*soakPhaseObservations{}
	}
	name := o.phaseAt()
	if o.phases[name] == nil {
		o.phases[name] = &soakPhaseObservations{}
	}
	return o.phases[name]
}

func soakPercentile(latencies []time.Duration, percent int) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[min(len(sorted)-1, len(sorted)*percent/100)]
}

func (o *soakObservations) snapshot() soakReport {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := soakReport{total: len(o.latencies), codes: map[string]int{}, caps: map[string]int{}, quietRefusals: o.quietRefusals, faultedRefusals: o.faultedRefusals, failures: o.failures, timeouts: o.timeouts, abandoned: o.abandoned, mediaBytes: o.mediaBytes}
	out.routes = map[string]soakRouteLatencyReport{}
	for label, route := range o.routes {
		out.routes[label] = soakRouteLatencyReport{attempts: len(route.cleanRaw), successes: len(route.cleanSuccess), rawP50: soakPercentile(route.cleanRaw, 50), rawP95: soakPercentile(route.cleanRaw, 95), rawP99: soakPercentile(route.cleanRaw, 99), successP50: soakPercentile(route.cleanSuccess, 50), successP95: soakPercentile(route.cleanSuccess, 95), successP99: soakPercentile(route.cleanSuccess, 99)}
	}
	out.phases = map[string]soakPhaseReport{}
	for _, lane := range o.lanes {
		out.lanes = append(out.lanes, lane)
	}
	sort.Slice(out.lanes, func(i, j int) bool { return out.lanes[i].last.Lane < out.lanes[j].last.Lane })
	for name, phase := range o.phases {
		out.phases[name] = soakPhaseReport{attempts: len(phase.latencies), refusals: phase.refusals, failures: phase.failures, p95: soakPercentile(phase.latencies, 95), p99: soakPercentile(phase.latencies, 99), logicalRequests: len(phase.logicalLatencies), logicalFailures: phase.logicalFailures, logicalP95: soakPercentile(phase.logicalLatencies, 95)}
	}
	out.streamPeak, out.cleanStreamSamples, out.cleanStreamsAtTarget, out.recoveryStreamsAtTarget = o.streamPeak, o.cleanStreamSamples, o.cleanStreamsAtTarget, o.recoveryStreamsAtTarget
	out.cleanRequests = len(o.cleanLatencies)
	out.recoverySuccesses = o.recoverySuccesses
	out.cleanTargetStreakPeak, out.recoveryTargetStreakPeak = o.cleanTargetStreakPeak, o.recoveryTargetStreakPeak
	out.clientRetries, out.logicalCleanRequests, out.logicalCleanFailures = o.clientRetries, len(o.logicalCleanLatencies), o.logicalCleanFailures
	if len(o.logicalCleanLatencies) > 0 {
		latencies := append([]time.Duration(nil), o.logicalCleanLatencies...)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		out.logicalCleanP95 = latencies[min(len(latencies)-1, len(latencies)*95/100)]
	}
	for _, n := range o.streamReaderBytes {
		if n >= 1<<20 {
			out.streamReaders++
		}
	}
	if len(o.cleanLatencies) > 0 {
		clean := append([]time.Duration(nil), o.cleanLatencies...)
		sort.Slice(clean, func(i, j int) bool { return clean[i] < clean[j] })
		out.cleanP95, out.cleanP99 = clean[min(len(clean)-1, len(clean)*95/100)], clean[min(len(clean)-1, len(clean)*99/100)]
	}
	for key, count := range o.codes {
		out.codes[key] = count
	}
	for key, count := range o.caps {
		out.caps[key] = count
	}
	out.transportErrors = append(out.transportErrors, o.transportErrors...)
	out.serverErrors = append(out.serverErrors, o.serverErrors...)
	out.byLabel = map[string]int{}
	for key, count := range o.byLabel {
		out.byLabel[key] = count
	}
	if len(o.latencies) == 0 {
		return out
	}
	sorted := append([]time.Duration(nil), o.latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out.p50 = sorted[len(sorted)*50/100]
	out.p95 = sorted[min(len(sorted)-1, len(sorted)*95/100)]
	out.p99 = sorted[min(len(sorted)-1, len(sorted)*99/100)]
	return out
}

// soakCall is one request over a real socket.
func soakCall(ctx context.Context, client *http.Client, base, token, label, method, path string, body any, o *soakObservations, quiet bool) (int, []byte) {
	return soakCallHeaders(ctx, client, base, token, label, method, path, body, nil, o, quiet)
}

func soakCallHeaders(ctx context.Context, client *http.Client, base, token, label, method, path string, body any, headers map[string]string, o *soakObservations, quiet bool) (int, []byte) {
	// Ordinary clients replay retryable refusals after Retry-After, with jitter.
	// Hostile/cancellation/reconnect faults retain their raw aggressive shape.
	normal := label == "home" || label == "browse" || label == "search" || label == "detail" || label == "personal-state" || label == "playback-create" || label == "playback-stop" || label == "playback-observer" || label == "playback-observation" || label == "recovery-home"
	start := time.Now()
	code, raw, retryAfter := soakCallAttempt(ctx, client, base, token, label, method, path, body, headers, o, quiet)
	if normal && (code == 429 || code == 503) {
		o.retry()
		wait := time.NewTimer(retryAfter + time.Duration(rand.Int63n(250))*time.Millisecond)
		select {
		case <-ctx.Done():
			wait.Stop()
		case <-wait.C:
			code, raw, _ = soakCallAttempt(ctx, client, base, token, label, method, path, body, headers, o, quiet)
		}
	}
	if normal {
		o.logical(code, time.Since(start), quiet, ctx.Err() != nil)
	}
	return code, raw
}

func soakCallAttempt(ctx context.Context, client *http.Client, base, token, label, method, path string, body any, headers map[string]string, o *soakObservations, quiet bool) (int, []byte, time.Duration) {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, 0
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		// Recorded even when the window has closed: a request that never completed
		// is the most interesting thing that can happen to one, and dropping it
		// because the clock ran out would hide exactly the case worth seeing.
		if ctx.Err() == nil {
			o.transport(label, err)
		}
		o.record(label, 0, time.Since(start), quiet, ctx.Err() != nil)
		return 0, nil, 0
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if readErr != nil {
		if ctx.Err() == nil {
			o.transport(label+"-body", readErr)
		}
		o.record(label, 0, time.Since(start), quiet, ctx.Err() != nil)
		return 0, nil, 0
	}
	o.record(label, response.StatusCode, time.Since(start), quiet, false)
	if response.StatusCode == 429 || response.StatusCode == 503 {
		o.capRefusal(label, string(raw))
	}
	retryAfter := time.Second
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		retryAfter = time.Duration(seconds) * time.Second
	} else if deadline, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && time.Until(deadline) > 0 {
		retryAfter = time.Until(deadline)
	}
	return response.StatusCode, raw, retryAfter
}

// soakViewerRound is one person's pass through the product.
func soakViewerRound(ctx context.Context, client *http.Client, base, token string, f *loadFixture, index int, o *soakObservations, quiet bool) {
	pause := func() {
		select {
		case <-ctx.Done():
		case <-time.After(150*time.Millisecond + time.Duration(rand.Int63n(300))*time.Millisecond):
		}
	}
	item := f.items[index%len(f.items)]
	soakCall(ctx, client, base, token, "home", "GET", "/v1/home", nil, o, quiet)
	pause()
	soakCall(ctx, client, base, token, "browse", "POST", "/v1/libraries/"+f.library+"/browse", map[string]any{"pivot": "movies", "sort": []map[string]string{{"field": "title", "direction": "asc"}}, "limit": 40}, o, quiet)
	pause()
	soakCall(ctx, client, base, token, "search", "GET", fmt.Sprintf("/v1/search?q=Title+%06d&limit=10", index), nil, o, quiet)
	pause()
	soakCall(ctx, client, base, token, "detail", "GET", "/v1/items/"+item+"/detail", nil, o, quiet)
	pause()
	soakCall(ctx, client, base, token, "personal-state", "PUT", "/v1/items/"+item+"/personal-state",
		map[string]any{"favorite": index%2 == 0, "expectedRevision": int64(0), "operationId": fmt.Sprintf("soak-%d-%d", index, time.Now().UnixNano())}, o, quiet)
	pause()
}

// soakStream creates a session and reads real bytes from it, sometimes badly.
func soakStream(ctx context.Context, client *http.Client, base, token string, f *loadFixture, index int, o *soakObservations, isClean func() bool, bytesPerSecond int, protocol string) {
	quiet := isClean()
	requestID := fmt.Sprintf("soak-stream-%d-%d", index, time.Now().UnixNano())
	startBody := map[string]string{"itemId": f.playable, "startFrom": "beginning"}
	headers := map[string]string{"Idempotency-Key": requestID}
	code, body := soakCallHeaders(ctx, client, base, token, "playback-create", "POST", "/v1/playback/sessions", startBody, headers, o, quiet)
	if code != 201 {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		return
	}
	var session playback.Session
	var view playbackv1.SessionView
	if json.Unmarshal(body, &view) != nil || view.Presentation.Mode != "direct" {
		o.record("playback-invalid-presentation", http.StatusBadGateway, 0, quiet, false)
		return
	}
	session.ID, session.StreamURL = view.ID, view.Presentation.URL
	if session.StreamURL == "" {
		o.record("playback-missing-media", http.StatusBadGateway, 0, quiet, false)
		return
	}
	defer func() {
		soakCall(ctx, client, base, token, "playback-stop", "DELETE", "/v1/playback/sessions/"+session.ID, nil, o, isClean())
	}()
	if protocol == "v1" {
		code, body := soakCall(ctx, client, base, token, "playback-observer", "POST", "/v1/playback/sessions/"+session.ID+"/observer", map[string]any{"generation": view.Presentation.Generation}, o, isClean())
		var grant playbackv1.ObserverGrant
		if code != 200 || json.Unmarshal(body, &grant) != nil || grant.EveryMs <= 0 {
			return
		}
		reportCtx, cancel := context.WithCancel(ctx)
		var reports sync.WaitGroup
		reports.Add(1)
		reportStart := time.Now()
		go func() {
			defer reports.Done()
			interval := max(time.Second, time.Duration(grant.EveryMs)*time.Millisecond)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for seq := int64(1); ; seq++ {
				select {
				case <-reportCtx.Done():
					return
				case <-ticker.C:
				}
				elapsed := time.Since(reportStart).Milliseconds()
				soakCall(reportCtx, client, base, token, "playback-observation", "PUT", "/v1/playback/sessions/"+session.ID+"/observation", playbackv1.Observation{Epoch: grant.Epoch, Generation: view.Presentation.Generation, Seq: seq, State: "playing", PositionMs: elapsed, MonotonicMs: elapsed, PlayClockMs: elapsed, Rate: 1}, o, isClean())
			}
		}()
		defer func() { cancel(); reports.Wait() }()
	}

	behaviour := "healthy"
	if !quiet {
		// A tenth read at a trickle and a twentieth stop reading entirely: the two
		// halves of the write-deadline criterion. The slow one must never be
		// dropped; the dead one must be reclaimed.
		switch {
		case index%20 == 0:
			behaviour = "dead"
		case index%10 == 0:
			behaviour = "slow"
		case index%7 == 0:
			behaviour = "abandon"
		}
	}
	request, err := http.NewRequestWithContext(ctx, "GET", base+session.StreamURL, nil)
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		o.record("media", 0, time.Since(start), quiet, ctx.Err() != nil)
		return
	}
	o.record("media", response.StatusCode, time.Since(start), quiet, false)
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 206 {
		return
	}
	// A real viewer reads at the bitrate of what they are watching, not as fast
	// as the socket will go. Without pacing, forty streams pull half a gigabyte a
	// second and the soak stops being a model of a household and becomes a
	// throughput benchmark that starves everything else on the machine.
	buffer := make([]byte, 32<<10)
	read := int64(0)
	var bodyErr error
	hold := 20 * time.Second
	if protocol == "v1" {
		// Exercise several lease/progress reports on one presentation rather
		// than mostly measuring session creation and replacement.
		hold = 45 * time.Second
	}
	deadline := time.Now().Add(hold)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		if behaviour != "slow" {
			time.Sleep(time.Duration(len(buffer)) * time.Second / time.Duration(bytesPerSecond))
		}
		switch behaviour {
		case "dead":
			// Stop reading and hold the connection open. The rolling write deadline
			// is what reclaims this.
			select {
			case <-ctx.Done():
			case <-time.After(15 * time.Second):
			}
			return
		case "abandon":
			// Vanish mid-body without closing politely.
			return
		case "slow":
			time.Sleep(200 * time.Millisecond)
		}
		n, err := response.Body.Read(buffer)
		read += int64(n)
		if err != nil {
			bodyErr = err
			break
		}
	}
	o.bytes(read)
	o.streamRead(index, read)
	if behaviour == "healthy" && bodyErr != nil && ctx.Err() == nil && time.Now().Before(deadline) {
		o.transport("media-body-dropped", bodyErr)
		o.record("media-body-dropped", http.StatusBadGateway, time.Since(start), quiet, false)
	}
	if behaviour == "slow" && read == 0 && ctx.Err() == nil {
		o.record("media-slow-dropped", 0, time.Since(start), quiet, false)
	}
}

// soakHoldEvents holds the unified event stream open and reads frames from it.
func soakHoldEvents(ctx context.Context, client *http.Client, base, token string, o *soakObservations) {
	life, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(life, "GET", base+"/v1/events", nil)
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return
	}
	o.record("events", response.StatusCode, time.Since(start), false, false)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		select {
		case <-life.Done():
		case <-time.After(time.Second):
		}
		return
	}
	buffer := make([]byte, 4<<10)
	for life.Err() == nil {
		if _, err = response.Body.Read(buffer); err != nil {
			return
		}
	}
}

// soakFault is one injected failure, chosen in rotation so every kind happens.
func soakFault(ctx context.Context, t *testing.T, f *loadFixture, base string, index int, o *soakObservations) {
	switch index % 7 {
	case 0:
		// Every pooled connection held at once: the pool-saturation shape. A driver
		// shim returning SQLITE_BUSY would be closer to the audit's wording, but
		// this produces the same thing the gate has to survive — every caller
		// waiting on the pool — without a fake driver between the test and the
		// behaviour being tested.
		var held []*sql.Conn
		for index := 0; index < dbwork.DefaultPolicy().MaxOpenConns; index++ {
			conn, err := f.db.Conn(ctx)
			if err != nil {
				break
			}
			held = append(held, conn)
		}
		select {
		case <-ctx.Done():
		case <-time.After(750 * time.Millisecond):
		}
		for _, conn := range held {
			conn.Close()
		}
	case 1:
		// Clients that abandon a request mid-flight: no lane slot, goroutine,
		// descriptor or helper process may be left behind by one.
		client := &http.Client{Timeout: 10 * time.Second}
		for cancelled := 0; cancelled < 20 && ctx.Err() == nil; cancelled++ {
			life, stop := context.WithTimeout(ctx, time.Duration(5+cancelled*3)*time.Millisecond)
			soakCall(life, client, base, f.owner.AccessToken, "cancelled", "GET", "/v1/home", nil, o, false)
			stop()
		}
	case 2:
		// Kill a child process — a converter or a storage helper — and require
		// nothing to be orphaned by it.
		if killed := killOneChild(); killed > 0 {
			t.Logf("fault: killed %d child process(es)", killed)
		}
	case 3:
		// A burst of requests from one client, which is what a retry loop looks
		// like. No other viewer may be affected by it.
		client := &http.Client{Timeout: 10 * time.Second}
		for burst := 0; burst < 40 && ctx.Err() == nil; burst++ {
			soakCall(ctx, client, base, f.owner.AccessToken, "hostile", "GET", "/v1/readiness", nil, o, false)
		}
	case 4:
		// A hostile client: malformed bodies, absurd parameters and unparseable
		// credentials, across the routes a person actually uses. None of it may
		// produce a 5xx other than an honest 503, and none of it may cost another
		// viewer anything — which the quiet-window refusal count is what proves.
		soakHostileClient(ctx, base, f, o)
	case 5:
		// A reconnect storm: fifty clients opening event streams and dropping
		// them immediately, over and over. This is a phone on a train, multiplied
		// — every one of them correct, all of them together a goroutine and
		// descriptor leak if anything is left behind.
		soakReconnectStorm(ctx, base, f, o)
	case 6:
		// Flaky streams: readers that abandon a media body part-way through and
		// come back for a different range, which is what a congested link looks
		// like from the server's side. A partially read body must leave nothing
		// behind.
		soakFlakyStreams(ctx, base, f, o)
	}
}

// soakHostileClient sends the table test's shapes at a live server under load.
// The table test proves a handler answers correctly; this proves doing it two
// hundred times while two hundred people are watching costs them nothing.
func soakHostileClient(ctx context.Context, base string, f *loadFixture, o *soakObservations) {
	client := &http.Client{Timeout: 10 * time.Second}
	bodies := []string{
		`{`, `null`, `[1,2,3]`, `{"limit":-1}`, `{"limit":99999999999999999999}`,
		strings.Repeat(`{"a":`, 500) + "1" + strings.Repeat(`}`, 500),
		`{"q":"'; DROP TABLE items; --"}`, `{"name":"` + strings.Repeat("A", 20000) + `"}`,
	}
	queries := []string{"?limit=-1", "?limit=abc", "?cursor=" + strings.Repeat("A", 4000), "?q=%00%01", "?" + strings.Repeat("k=v&", 500)}
	targets := []struct{ method, path string }{
		{"GET", "/v1/home"}, {"GET", "/v1/items"}, {"GET", "/v1/search"},
		{"POST", "/v1/libraries"}, {"GET", "/v1/me"}, {"POST", "/v1/playback/sessions"},
	}
	for attempt := 0; attempt < 60 && ctx.Err() == nil; attempt++ {
		target := targets[attempt%len(targets)]
		path := target.path + queries[attempt%len(queries)]
		request, err := http.NewRequestWithContext(ctx, target.method, base+path, strings.NewReader(bodies[attempt%len(bodies)]))
		if err != nil {
			continue
		}
		// A quarter of them present rubbish credentials rather than none.
		switch attempt % 4 {
		case 0:
			request.Header.Set("Authorization", "Bearer "+f.owner.AccessToken)
		case 1:
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 5000))
		case 2:
			request.Header.Set("Authorization", "Bearer")
		}
		request.Header.Set("Content-Type", "application/json")
		start := time.Now()
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() == nil {
				o.transport("hostile-input", err)
			}
			o.hostile(0, time.Since(start))
			continue
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		o.hostile(response.StatusCode, time.Since(start))
		if response.StatusCode >= 500 && response.StatusCode != 503 {
			o.serverError("hostile-input", target.method+" "+path, response.StatusCode)
		}
	}
}

// soakReconnectStorm opens and drops event streams as fast as it can. Nothing
// may be left behind: the goroutine and descriptor criteria are what this feeds.
func soakReconnectStorm(ctx context.Context, base string, f *loadFixture, o *soakObservations) {
	var storm sync.WaitGroup
	for client := 0; client < 50; client++ {
		storm.Add(1)
		go func(client int) {
			defer storm.Done()
			http := &http.Client{Timeout: 5 * time.Second}
			for round := 0; round < 4 && ctx.Err() == nil; round++ {
				life, stop := context.WithTimeout(ctx, time.Duration(20+client)*time.Millisecond)
				request, err := newRequest(life, base+"/v1/events", f.owner.AccessToken)
				if err == nil {
					start := time.Now()
					response, callErr := http.Do(request)
					if callErr == nil {
						// Read a little, then abandon: the shape a dropped
						// connection actually has.
						io.Copy(io.Discard, io.LimitReader(response.Body, 512))
						response.Body.Close()
						o.record("reconnect-storm", response.StatusCode, time.Since(start), false, false)
					} else {
						o.record("reconnect-storm", 0, time.Since(start), false, true)
					}
				}
				stop()
			}
		}(client)
	}
	storm.Wait()
}

// soakFlakyStreams opens real sessions, reads part of the body, abandons it and
// comes back for a different range. Ten per cent of a household's streams look
// like this on a bad evening, and a partially read body must leave nothing
// behind: no lane slot, no descriptor, no helper process.
func soakFlakyStreams(ctx context.Context, base string, f *loadFixture, o *soakObservations) {
	var flaky sync.WaitGroup
	for reader := 0; reader < 5; reader++ {
		flaky.Add(1)
		go func(reader int) {
			defer flaky.Done()
			client := &http.Client{Timeout: 8 * time.Second}
			code, body := soakCall(ctx, client, base, f.owner.AccessToken, "flaky-create", "POST", "/v1/playback/sessions",
				map[string]string{"itemId": f.playable, "quality": "auto", "requestId": fmt.Sprintf("soak-flaky-%d-%d", reader, time.Now().UnixNano())}, o, false)
			if code != 201 {
				return
			}
			var session playback.Session
			if json.Unmarshal(body, &session) != nil || session.StreamURL == "" {
				return
			}
			defer soakCall(ctx, client, base, f.owner.AccessToken, "flaky-stop", "DELETE", "/v1/playback/sessions/"+session.ID, nil, o, false)
			for round := 0; round < 3 && ctx.Err() == nil; round++ {
				request, err := newRequest(ctx, base+session.StreamURL, f.owner.AccessToken)
				if err != nil {
					return
				}
				request.Header.Set("Range", fmt.Sprintf("bytes=%d-", reader*4096+round*1024))
				start := time.Now()
				response, callErr := client.Do(request)
				if callErr != nil {
					o.record("flaky-stream", 0, time.Since(start), false, ctx.Err() != nil)
					continue
				}
				// Abandoned after a few kilobytes, which is what a link that has
				// just congested does.
				n, _ := io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
				response.Body.Close()
				o.bytes(n)
				o.record("flaky-stream", response.StatusCode, time.Since(start), false, false)
			}
		}(reader)
	}
	flaky.Wait()
}

// newRequest is the authorised GET every fault here makes.
func newRequest(ctx context.Context, url, token string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	return request, nil
}

// killOneChild ends one child process of this one, for the orphan criterion.
func killOneChild() int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || pid <= 0 {
			continue
		}
		process, findErr := os.FindProcess(pid)
		if findErr != nil {
			continue
		}
		_ = process.Kill()
		return 1
	}
	return 0
}
