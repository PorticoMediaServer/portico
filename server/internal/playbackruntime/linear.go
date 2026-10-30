package playbackruntime

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/linearbuffer"
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	library "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/producerinput"
)

type LinearConfig struct {
	DB                                             *sql.DB
	Live                                           *livechannels.Store
	Library                                        *library.Store
	Policy                                         playback.CachedPreparationPolicy
	CacheDirectory, LockDirectory, FFmpeg, FFprobe string
	DecoderLibraries                               []string
	Retention                                      time.Duration
	MaxBytes                                       int64
	// ReadBufferLimits supplies the current owner policy for each new buffer.
	// Existing buffers retain the limits under which they admitted their media.
	// Nil keeps the fixed Retention/MaxBytes configuration for other embedders.
	ReadBufferLimits func(context.Context) (time.Duration, int64, error)
}
type LinearRuntime struct {
	owner *Runtime
	// authority is the work's owner: a v1 channel session.
	authority    *playback.ChannelSessions
	inputs       directInputs
	config       LinearConfig
	locks        *livechannels.PhysicalLocks
	availability string
	mu           sync.Mutex
	entries      map[string]*linearEntry
	skips        map[string]string // session -> last logged reason it can't start
	wg           sync.WaitGroup
}
type linearEntry struct {
	captions    linearinput.CaptionDeclarations
	mu          sync.Mutex
	buffer      *linearbuffer.Buffer
	token       string
	generation  int64
	producer    int64
	selection   playback.LinearSelection
	selections  map[int64]playback.LinearSelection
	state, code string
	// engine is the player engine the buffer's stream is made for; the first
	// producer chooses it and later ones keep it.
	engine   string
	logged   string // the last start failure written to the log
	cancel   context.CancelFunc
	requests chan struct{}
}

func privateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("linear cache must be a canonical private directory")
	}
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path {
		return errors.New("linear cache cannot traverse symlinks")
	}
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return errors.New("linear cache must be private")
	}
	return nil
}
func resolveDecoder(name string) (string, error) {
	p, e := exec.LookPath(name)
	if e != nil {
		return "", e
	}
	p, e = filepath.Abs(p)
	if e != nil {
		return "", e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	info, e := os.Stat(p)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("decoder unavailable")
	}
	return p, nil
}

// confinementProbe says why decoders may not run here at all; a variable so
// tests can stand in for the host.
var confinementProbe = decoder.RequiredConfinement

// linearSandboxInUse says whether media jobs with this tool run sandboxed; a
// variable so tests can stand in for a host without a sandbox.
var linearSandboxInUse = func(executable string) bool {
	return mediaexec.Decide(executable).Sandboxed
}

// linearAvailability is why live can't run on this host ("" when it can) and
// the concrete cause. Live runs on every platform: sandboxed where the platform
// can (macOS sandbox-exec, Linux bubblewrap with user namespaces), otherwise
// with the baseline restrictions, which the owner's diagnostics report
// (D-MEDIA-6, BE-MEDIA-02). Only an owner who requires the sandbox on a host
// without one gets decoder_confinement_unavailable.
func linearAvailability(ffmpeg, ffprobe error, probe func() error) (string, error) {
	if err := probe(); err != nil {
		return "decoder_confinement_unavailable", err
	}
	if ffprobe != nil {
		return "ffprobe_not_configured", fmt.Errorf("ffprobe: %w", ffprobe)
	}
	if ffmpeg != nil {
		return "ffmpeg_not_configured", fmt.Errorf("ffmpeg: %w", ffmpeg)
	}
	return "", nil
}

// ConfigureChannels is called before Start. A real missing binary/confinement
// backend is reported as configuration/support state, never a qualification gate.
func (r *Runtime) ConfigureChannels(c LinearConfig) error {
	if c.DB == nil || c.Live == nil || c.Library == nil || c.Policy == nil || r.Linear != nil {
		return errors.New("invalid channel runtime dependencies")
	}
	if c.Retention == 0 {
		c.Retention = 30 * time.Minute
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 1 << 30
	}
	if c.Retention < 12*time.Second || c.Retention > 24*time.Hour || c.MaxBytes < 64<<20 || c.MaxBytes > 16<<30 {
		return errors.New("invalid channel retention limits")
	}
	if e := privateDirectory(c.CacheDirectory); e != nil {
		return e
	}
	if e := linearbuffer.Sweep(c.CacheDirectory); e != nil {
		return e
	}
	locks, e := livechannels.NewPhysicalLocks(c.LockDirectory)
	if e != nil {
		return e
	}
	l := &LinearRuntime{owner: r, config: c, locks: locks, entries: map[string]*linearEntry{}}
	var ffmpegErr, ffprobeErr, cause error
	l.config.FFmpeg, ffmpegErr = resolveDecoder(c.FFmpeg)
	l.config.FFprobe, ffprobeErr = resolveDecoder(c.FFprobe)
	l.availability, cause = linearAvailability(ffmpegErr, ffprobeErr, func() error { return confinementProbe(l.config.FFmpeg) })
	if l.availability == "" {
		libraries, e := decoderLibraries(l.config.FFmpeg, l.config.FFprobe, c.DecoderLibraries)
		if e != nil {
			l.availability, cause = "decoder_dependencies_unavailable", fmt.Errorf("FFmpeg's libraries can't be resolved: %w", e)
		} else {
			l.config.DecoderLibraries = libraries
		}
	}
	capabilityreport.Report(capabilityreport.LiveTV, l.availability == "", l.availability, cause)
	resolver := NewChannelResolver(c.Live, c.Library, c.Policy, l.Available)
	if e = r.Channels.ConfigureLinear(resolver, l); e != nil {
		return e
	}
	l.authority = r.Channels
	r.Linear = l
	return nil
}
func (l *LinearRuntime) Available() (bool, string) {
	return l != nil && l.availability == "", func() string {
		if l == nil {
			return "channel_runtime_unavailable"
		}
		return l.availability
	}()
}
func (l *LinearRuntime) run(ctx context.Context) {
	defer l.wg.Wait()
	defer func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, e := range l.entries {
			e.cancel()
		}
	}()
	urgent, unsubscribe := l.owner.wake.Subscribe()
	defer unsubscribe()
	shared := workerloop.NewSignal()
	unregister := watchLinearWork(shared)
	defer unregister()
	lastReconcile := time.Time{}
	workerloop.RunWith(ctx, "playbackruntime.linear", urgent, shared, func(ctx context.Context) time.Duration {
		if time.Since(lastReconcile) >= 5*time.Second {
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, _ = l.locks.ReconcileExpired(check, l.config.DB, time.Now())
			cancel()
			lastReconcile = time.Now()
		}
		if l.availability != "" {
			return 0
		}
		after := ""
		for {
			ids, e := l.owner.Linear.authority.LinearWorkIDs(ctx, after, 128)
			if e != nil {
				break
			}
			for _, id := range ids {
				after = id
				l.mu.Lock()
				existing := l.entries[id]
				l.mu.Unlock()
				if existing != nil {
					continue
				}
				if _, e = l.owner.Linear.authority.LinearWork(ctx, id); e != nil {
					l.skipped(id, e)
					continue
				}
				life, cancel := context.WithCancel(ctx)
				entry := &linearEntry{cancel: cancel, selections: map[int64]playback.LinearSelection{}, requests: make(chan struct{}, 16), state: "preparing"}
				l.mu.Lock()
				l.entries[id] = entry
				l.mu.Unlock()
				l.wg.Add(1)
				supervise.Go("playbackruntime.linear.entry", func() {
					e := entry
					defer l.wg.Done()
					defer e.cancel()
					defer func() {
						e.mu.Lock()
						b := e.buffer
						e.mu.Unlock()
						if b != nil {
							_ = b.Close()
						}
						l.mu.Lock()
						if l.entries[id] == e {
							delete(l.entries, id)
						}
						l.mu.Unlock()
					}()
					l.runOccurrence(life, id, e)
				})
			}
			if len(ids) < 128 {
				break
			}
		}
		l.mu.Lock()
		active := len(l.entries) > 0
		l.mu.Unlock()
		if active {
			return 500 * time.Millisecond
		}
		return 0
	})
}
func waitLinear(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (l *LinearRuntime) runOccurrence(ctx context.Context, id string, entry *linearEntry) {
	// A buffer that can't be opened is reported on the occurrence (recoverable,
	// with its code) and retried with backoff: never a silent return that the
	// next wake repeats while the viewer waits on "preparing".
	var work playback.LinearWork
	var e error
	for attempt := 0; ; attempt++ {
		if work, e = l.owner.Linear.authority.BeginLinearBuffer(ctx, id); e == nil {
			break
		}
		current, readErr := l.owner.Linear.authority.LinearWork(ctx, id)
		if ctx.Err() != nil {
			return
		}
		if readErr != nil {
			l.skipped(id, readErr)
			return
		}
		l.fail(ctx, entry, current, startFault(e))
		if !waitLinear(ctx, time.Duration(1<<min(attempt, 3))*time.Second) {
			return
		}
	}
	retention, maxBytes := l.config.Retention, l.config.MaxBytes
	if l.config.ReadBufferLimits != nil {
		retention, maxBytes, e = l.config.ReadBufferLimits(ctx)
		if e == nil && (retention < 12*time.Second || retention > 24*time.Hour || maxBytes < 64<<20 || maxBytes > 16<<30) {
			e = errors.New("invalid channel retention limits")
		}
		if e != nil {
			l.fail(ctx, entry, work, startFault(e))
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	buffer, e := linearbuffer.New(linearbuffer.Options{Parent: l.config.CacheDirectory, OriginMS: time.Now().UnixMilli(), MaxBytes: maxBytes, Retention: retention, Scheduled: work.Selection.Reference.Kind == "library-channel"})
	if e != nil {
		l.report(ctx, entry, work, "timeshift_storage_unavailable", "delivery", e)
		return
	}
	var secret [32]byte
	if _, e = rand.Read(secret[:]); e != nil {
		_ = buffer.Close()
		return
	}
	entry.mu.Lock()
	entry.buffer = buffer
	entry.engine = ""
	entry.generation = work.BufferOrdinal
	entry.token = hex.EncodeToString(secret[:])
	entry.selection = work.Selection
	entry.mu.Unlock()
	attempts := 0
	retryRevision := work.RetryRevision
	selectionKey := linearSelectionKey(work.Selection)
	initialPolicy, initialLease, initialAuthority := work.PolicyRevision, work.LeaseGeneration, work.LeaseAuthorityRevision
	initialMemberCeilings := work.MemberCeilings
	for ctx.Err() == nil {
		changed, refreshErr := l.owner.Linear.authority.RefreshLinearSelection(ctx, id)
		if changed {
			l.owner.Wake()
		}
		work, e = l.owner.Linear.authority.LinearWork(ctx, id)
		if e != nil {
			return
		}
		if work.PolicyRevision != initialPolicy || work.LeaseGeneration != initialLease || work.LeaseAuthorityRevision != initialAuthority || work.MemberCeilings != initialMemberCeilings {
			return
		}
		entry.mu.Lock()
		entry.selection = work.Selection
		entry.mu.Unlock()
		if key := linearSelectionKey(work.Selection); key != selectionKey {
			selectionKey = key
			attempts = 0
		}
		if work.RetryRevision != retryRevision {
			attempts = 0
			retryRevision = work.RetryRevision
			_ = l.owner.Linear.authority.RestartLinearSource(ctx, id)
			l.owner.Wake()
			continue
		}

		if refreshErr != nil {
			l.fail(ctx, entry, work, refreshErr)
			if !waitLinear(ctx, time.Second) {
				return
			}
			continue
		}
		if attempts >= linearRecoveryBudget {
			if !waitLinear(ctx, time.Second) {
				return
			}
			continue
		}
		work, e = l.owner.Linear.authority.BeginLinearProducer(ctx, id, work.SourceRevision, work.BufferOrdinal)
		if e != nil {
			// A source that changed underneath is a quick retry; anything else is a
			// reported failure with backoff.
			var fault *playback.ControlFault
			delay := 250 * time.Millisecond
			if !errors.As(e, &fault) || fault.Code != "source_changed" {
				attempts++
				if current, readErr := l.owner.Linear.authority.LinearWork(ctx, id); readErr == nil {
					l.fail(ctx, entry, current, startFault(e))
				}
				delay = time.Duration(1<<min(attempts-1, 3)) * time.Second
			}
			if !waitLinear(ctx, delay) {
				return
			}
			continue
		}
		entry.mu.Lock()
		entry.producer = work.ProducerOrdinal
		entry.selections[work.ProducerOrdinal] = work.Selection
		entry.state = "preparing"
		entry.code = ""
		entry.mu.Unlock()
		before := entry.buffer.Produced()
		e = l.produceMonitored(ctx, work, entry)
		if ctx.Err() != nil {
			return
		}
		current, authorityErr := l.owner.Linear.authority.LinearWork(ctx, id)
		if authorityErr != nil {
			return
		}
		if current.SourceRevision != work.SourceRevision {
			if linearSelectionKey(current.Selection) != selectionKey {
				attempts = 0
				selectionKey = linearSelectionKey(current.Selection)
			}
			continue
		}
		if current.PolicyRevision != initialPolicy || current.LeaseGeneration != initialLease || current.LeaseAuthorityRevision != initialAuthority || current.MemberCeilings != initialMemberCeilings {
			return
		}
		attempts = linearFailureCount(attempts, entry.buffer.Produced()-before)
		retryExpired := errors.Is(e, linearinput.ErrRetryWindow)
		if retryExpired {
			// The gateway already spent this outage's admitted wall-time budget.
			// A producer restart must not silently grant another whole retry window.
			attempts = linearRecoveryBudget
		}
		if e == nil {
			e = channelFault("source_ended", 503)
		}
		l.fail(ctx, entry, work, e)
		if retryExpired {
			continue
		}
		if !waitLinear(ctx, time.Duration(1<<min(attempts-1, 3))*time.Second) {
			return
		}
		if e = l.owner.Linear.authority.RestartLinearSource(ctx, id); e != nil {
			continue
		}
		l.owner.Wake()
	}
}

// linearRecoveryBudget is how many producer failures in a row are retried
// before the session waits for the viewer, the schedule or the source to change.
const linearRecoveryBudget = 5

// linearHealthyRun is how much media one producer must add to the buffer for
// its eventual failure to count as a new fault rather than the next in a row.
const linearHealthyRun = time.Minute

// linearFailureCount is the consecutive-failure count once a producer has
// ended, given how much media it added to the buffer. A producer that added a
// sustained run was healthy, so its failure is the first of a new series.
// What it added is measured as it is appended, so neither a short retention
// nor the byte cap evicting most of a long run makes that run look short, and
// media left by earlier producers and time spent reconnecting are not counted.
func linearFailureCount(attempts int, produced time.Duration) int {
	if produced >= linearHealthyRun {
		return 1
	}
	return attempts + 1
}
func (l *LinearRuntime) fail(ctx context.Context, e *linearEntry, w playback.LinearWork, err error) {
	code := "source_unavailable"
	var fault *playback.ControlFault
	switch {
	case errors.As(err, &fault):
		code = fault.Code
	case errors.Is(err, livechannels.ErrCapacity):
		code = "source_capacity_unavailable"
	case errors.Is(err, livechannels.ErrReservation), errors.Is(err, livechannels.ErrLease):
		code = "tuner_reservation_changed"
	case errors.Is(err, decoder.ErrConfinementUnavailable):
		code = "decoder_confinement_unavailable"
	case errors.Is(err, linearbuffer.ErrMedia):
		code = "source_timeline_invalid"
	}
	l.report(ctx, e, w, code, "recovery", err)
}

// report puts the occurrence in "recoverable" with a code the client shows, and
// logs the cause once per occurrence and code.
func (l *LinearRuntime) report(ctx context.Context, e *linearEntry, w playback.LinearWork, code, stage string, cause error) {
	e.mu.Lock()
	if e.state == "recoverable" && e.code == code {
		e.mu.Unlock()
		return // already said, and already recorded
	}
	e.state = "recoverable"
	e.code = code
	first := e.logged != code
	e.logged = code
	e.mu.Unlock()
	if first {
		log.Printf("Channel playback %s can't continue: %s: %s", shortPlaybackID(w.PlaybackID), code, strings.TrimPrefix(fmt.Sprint(cause), code+": "))
	}
	if e := l.owner.Linear.authority.LinearStatus(ctx, w, "recoverable", stage, code); e != nil && ctx.Err() == nil {
		log.Printf("Channel playback %s: its status could not be recorded: %v", shortPlaybackID(w.PlaybackID), e)
	}
}

// skipped logs, once per occurrence and reason, an active channel occurrence
// the runtime can't work on (its lease, controller or authority is gone).
func (l *LinearRuntime) skipped(id string, cause error) {
	code := "channel_start_failed"
	var fault *playback.ControlFault
	if errors.As(cause, &fault) {
		code = fault.Code
	}
	if code == "lease_expired" {
		return // the viewer left; nothing to say
	}
	l.mu.Lock()
	if l.skips == nil {
		l.skips = map[string]string{}
	}
	first := l.skips[id] != code
	if len(l.skips) > 1024 {
		clear(l.skips)
	}
	l.skips[id] = code
	l.mu.Unlock()
	if first {
		log.Printf("Channel playback %s can't start: %s: %v", shortPlaybackID(id), code, cause)
	}
}

// startFault keeps a control fault's own code and names anything else (a
// database or runtime failure) channel_start_failed.
func startFault(e error) error {
	var fault *playback.ControlFault
	if errors.As(e, &fault) {
		return e
	}
	return fmt.Errorf("%w: %w", channelFault("channel_start_failed", 503), e)
}

func shortPlaybackID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
func (l *LinearRuntime) produceMonitored(parent context.Context, w playback.LinearWork, entry *linearEntry) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var startup *linearStartup
	if w.Selection.Reference.Kind == "live-source" {
		startup = &linearStartup{started: time.Now(), phase: linearStartupAdmission}
	}
	done := make(chan struct{})
	supervise.Go("playbackruntime.linear.readiness", func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		ready := false
		check := func(ctx context.Context) (bool, error) {
			changed, e := l.owner.Linear.authority.RefreshLinearSelection(ctx, w.PlaybackID)
			if changed {
				l.owner.Wake()
			}
			if e == nil && !changed {
				e = l.owner.Linear.authority.CheckLinearProducer(ctx, w)
			}
			return changed, e
		}
		persisted := false
		markReady := func(ctx context.Context) {
			entry.mu.Lock()
			buffer := entry.buffer
			entry.mu.Unlock()
			window := buffer.Window()
			if !window.Ready {
				startup.report(w, window)
				return
			}
			if !ready {
				ready = true
				entry.mu.Lock()
				entry.state = "active"
				entry.code = ""
				entry.mu.Unlock()
			}
			// The stored status follows until one write lands: a write refused
			// under load is retried on the next tick, never dropped.
			if !persisted && l.owner.Linear.authority.LinearStatus(ctx, w, "active", "delivery", "") == nil {
				persisted = true
			}
		}
		logf := func(format string, args ...any) {
			log.Printf("Channel playback %s: producer %d "+format, append([]any{shortPlaybackID(w.PlaybackID), w.ProducerOrdinal}, args...)...)
		}
		if linearMonitor(ctx, ticker.C, check, markReady, logf) {
			cancel()
		}
	})
	var e error
	if w.Selection.Reference.Kind == "live-source" {
		e = l.produceLive(ctx, w, entry, startup)
	} else {
		e = l.produceLibraryDirect(ctx, w, entry)
	}
	cancel()
	<-done
	// Retained selections are bounded by the actual rolling media, not total uptime.
	keep := map[int64]bool{}
	for _, n := range entry.buffer.Producers() {
		keep[n] = true
	}
	entry.mu.Lock()
	for n := range entry.selections {
		if !keep[n] && n != entry.producer {
			delete(entry.selections, n)
		}
	}
	entry.mu.Unlock()
	return e
}

// linearMonitor is a running producer's readiness monitor. At every tick it
// runs check (follow the schedule and the source, then confirm this producer
// is still the current one) and, while the producer stands, markReady. It
// returns true when it decided to stop the producer, having logged why;
// false when ctx ended first.
func linearMonitor(ctx context.Context, ticks <-chan time.Time, check func(context.Context) (bool, error), markReady func(context.Context), logf func(string, ...any)) bool {
	transient := 0
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticks:
			changed, e := check(ctx)
			stop, count := linearMonitorVerdict(changed, e, transient)
			transient = count
			if stop {
				if ctx.Err() != nil {
					return false
				}
				switch {
				case changed:
					logf("stopped for a retune")
				case transient >= linearMonitorPatience || count >= linearMonitorPatience:
					logf("stopped after %d failed checks in a row: %v", linearMonitorPatience, e)
				default:
					logf("stopped by its monitor: %v", e)
				}
				return true
			}
			// Readiness is the buffer's, not the check's: a check that failed
			// for infrastructure (a busy database under load) says nothing
			// about the bytes the viewer can already play, so the producer is
			// still marked ready from its buffer. Only a stop (above) withholds it.
			markReady(ctx)
		}
	}
}

// linearMonitorVerdict decides whether a running producer's readiness monitor
// stops it. A new selection (a retune) or an authority refusal stops it at once:
// the producer is no longer the right one or the viewer may no longer watch. An
// infrastructure error (a busy database, a slow read under a scan) is not a
// verdict on the stream: the producer keeps running and the check is retried on
// the next tick, up to linearMonitorPatience ticks in a row (NEW-28: on the demo
// a check that failed once under scan load killed a healthy first producer, and
// the viewer saw "The channel's source isn't responding").
func linearMonitorVerdict(changed bool, e error, transient int) (stop bool, count int) {
	if changed {
		return true, 0
	}
	if e == nil {
		return false, 0
	}
	var fault *playback.ControlFault
	if errors.As(e, &fault) || errors.Is(e, identity.ErrUnauthorized) || errors.Is(e, identity.ErrNotVisible) || errors.Is(e, identity.ErrContentRestricted) || errors.Is(e, identity.ErrForbidden) {
		return true, 0
	}
	transient++
	return transient >= linearMonitorPatience, transient
}

// linearMonitorPatience is how many consecutive monitor ticks (500 ms each) may
// fail for infrastructure reasons before the producer is stopped anyway.
const linearMonitorPatience = 10

func (l *LinearRuntime) produceLive(ctx context.Context, w playback.LinearWork, entry *linearEntry, startup *linearStartup) (result error) {
	var allocation livechannels.Allocation
	var input livechannels.Input
	e := l.owner.Linear.authority.WithLinearWriteTx(ctx, w.PlaybackID, func(tx *sql.Tx, current playback.LinearWork) error {
		if current.SourceRevision != w.SourceRevision || current.ProducerOrdinal != w.ProducerOrdinal {
			return channelFault("source_changed", 409)
		}
		var e error
		allocation, e = livechannels.AcquireTx(ctx, tx, livechannels.Allocation{SourceID: w.Selection.Reference.SourceID, ChannelID: w.Selection.Reference.ChannelID, ResourceID: w.PlaybackID, Kind: "live", Owner: livechannels.Owner{Authority: w.Principal.Authority, AccountID: w.Principal.AccountID, ProfileID: w.Principal.ProfileID}}, time.Now())
		if e != nil {
			return e
		}
		input, e = l.config.Live.InputTx(ctx, tx, allocation.SourceID, allocation.ChannelID, w.Selection.Reference.Generation)
		return e
	})
	if e != nil {
		return e
	}
	// Allocation is committed before physical acquisition; every exit closes the
	// decoder and gateway before releasing its exact token/generation.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gated, e := dbwork.Begin(cleanup, l.config.DB, dbwork.ClassEstablishedPlayback)
		tx := gated.Tx()
		if e == nil {
			e = livechannels.ReleaseAllocationTx(cleanup, tx, allocation)
			if e == nil {
				e = gated.Commit()
			} else {
				_ = gated.Rollback()
			}
		}
		result = errors.Join(result, e)
	}()
	if e = entry.buffer.SetLiveDelay(input.StreamBufferSeconds); e != nil {
		return e
	}
	lock, e := l.locks.Lock(allocation)
	if e != nil {
		return e
	}
	defer lock.Close()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	renewed := make(chan struct{})
	supervise.Go("playbackruntime.linear.renewal", func() {
		defer close(renewed)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				e := l.owner.Linear.authority.WithLinearWriteTx(work, w.PlaybackID, func(tx *sql.Tx, current playback.LinearWork) error {
					if current.SourceRevision != w.SourceRevision || current.ProducerOrdinal != w.ProducerOrdinal {
						return channelFault("source_changed", 409)
					}
					_, e := livechannels.RenewAllocationTx(work, tx, allocation, time.Now())
					return e
				})
				if e != nil {
					cancel()
					return
				}
			}
		}
	})
	defer func() { cancel(); <-renewed }()
	startup.enter(linearStartupOpening, nil)
	gateway, e := linearinput.Open(work, input)
	if e != nil {
		return e
	}
	defer gateway.Close()
	defer func() {
		if gateway.RetryExpired() {
			result = errors.Join(result, linearinput.ErrRetryWindow)
		}
	}()
	startup.enter(linearStartupProbing, gateway)
	reservation, e := gateway.Reserve()
	if e != nil {
		return e
	}
	probeCtx, probeCancel := context.WithCancel(work)
	raw, e := decoder.LinearProbe(probeCtx, l.owner.probeSupervisor, w.PlaybackID+"-live-probe", l.config.FFprobe, gateway.URL(), gateway.HLS(), reservation, lock, l.config.DecoderLibraries...)
	probeCancel()
	if e != nil {
		return e
	}
	// A live input may publish fragmented MP4 where the device needs it; a
	// buffer already holding media keeps its kind.
	packaging := playback.LinearEither
	if held, fragmented := entry.buffer.Packaging(); held && fragmented {
		packaging = playback.LinearFragmentedOnly
	} else if held {
		packaging = playback.LinearTSOnly
	}
	entry.mu.Lock()
	pinned := entry.engine
	entry.mu.Unlock()
	startup.enter(linearStartupPlanning, nil)
	plan, engine, e := linearEnginePlan(raw, w, packaging, pinned)
	if e != nil {
		return e
	}
	startup.planned(plan)
	entry.mu.Lock()
	entry.engine = engine
	entry.captions = gateway.Captions()
	entry.mu.Unlock()
	plan.HLSInput = gateway.HLS()
	publisher, e := entry.buffer.Publisher(linearbuffer.Producer{ID: w.ProducerOrdinal, Fragmented: plan.Fragmented}, func() error { return l.checkProducerWithPatience(work, w) })
	if e != nil {
		return e
	}
	defer func() { _ = gateway.Close(); _ = publisher.Close() }()
	supervise.Go("playbackruntime.linear.publisher-watch", func() {
		select {
		case <-publisher.Failed():
			cancel()
		case <-work.Done():
		}
	})
	output, e := gateway.AttachOutput(publisher)
	if e != nil {
		return e
	}
	reservation, e = gateway.Reserve()
	if e != nil {
		return e
	}
	startup.enter(linearStartupCapture, nil)
	result = decoder.RunLinearHLS(work, l.owner.probeSupervisor, w.PlaybackID+"-"+strconv.FormatInt(w.ProducerOrdinal, 10), l.config.FFmpeg, gateway.URL(), output, plan, reservation, lock, l.config.DecoderLibraries...)
	if result == nil {
		finish, done := context.WithTimeout(work, 5*time.Second)
		result = publisher.Finish(finish)
		done()
	}
	_ = gateway.Close()
	return errors.Join(result, publisher.Result())
}
func privateListeners() (*net.TCPListener, *net.TCPListener, error) {
	v4, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		return nil, nil, e
	}
	v6, e := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: v4.Addr().(*net.TCPAddr).Port})
	if e != nil {
		v4.Close()
		return nil, nil, e
	}
	return v4, v6, nil
}
func (l *LinearRuntime) produceLibrary(ctx context.Context, w playback.LinearWork, entry *linearEntry, borrow producerinput.BorrowedInput) (result error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	v4, v6, e := privateListeners()
	if e != nil {
		return e
	}
	bridge, e := producerinput.NewPair(ctx, borrow, l.config.CacheDirectory, v4, v6)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, bridge.Shutdown(context.Background())) }()
	reservation, e := decoder.ReserveEndpoints(v4, v6)
	if e != nil {
		return e
	}
	probeCtx, probeCancel := context.WithCancel(ctx)
	raw, e := decoder.LinearProbe(probeCtx, l.owner.probeSupervisor, w.PlaybackID+"-library-probe", l.config.FFprobe, bridge.URL(), false, reservation, nil, l.config.DecoderLibraries...)
	probeCancel()
	if e != nil {
		return e
	}
	// A library channel's timeline is the source's own MPEG-TS clock. Its
	// engine is chosen like a live input's and kept for the buffer.
	entry.mu.Lock()
	pinned := entry.engine
	entry.mu.Unlock()
	plan, engine, e := linearEnginePlan(raw, w, playback.LinearTSOnly, pinned)
	if e != nil {
		return e
	}
	entry.mu.Lock()
	entry.engine = engine
	entry.captions = linearinput.CaptionDeclarations{}
	entry.mu.Unlock()
	now := time.Now().UnixMilli()
	if now >= w.Selection.EntryEndMS {
		return channelFault("programme_boundary", 409)
	}
	plan.Finite = true
	plan.SeekUS = max(int64(0), w.Selection.SourceOffsetMS+now-w.Selection.EntryStartMS) * 1000
	plan.ClipEndUS = (w.Selection.SourceOffsetMS + w.Selection.EntryEndMS - w.Selection.EntryStartMS) * 1000
	publisher, e := entry.buffer.Publisher(linearbuffer.Producer{ID: w.ProducerOrdinal, Scheduled: true, TimelineBaseMS: w.Selection.EntryStartMS - w.Selection.SourceOffsetMS, EndMS: w.Selection.EntryEndMS, OnPlayable: func() error {
		// This observer is on the actual borrowed library-media output only;
		// slate/live/setup paths never reach it. Recheck the exact producer
		// before recording its retained real occurrence, without viewer IDs.
		observed, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		if e := l.checkProducerWithPatience(observed, w); e != nil {
			return e
		}
		if _, e := borrow.Validate(observed); e != nil {
			return e
		}
		return l.config.Library.RecordPlayableStart(observed, w.Selection.Reference.ChannelID, w.Selection.Reference.Generation, w.Selection.EntryID, w.Selection.ItemID)
	}}, func() error {
		if e := l.checkProducerWithPatience(ctx, w); e != nil {
			return e
		}
		_, e := borrow.Validate(ctx)
		return e
	})
	if e != nil {
		return e
	}
	defer func() { _ = bridge.Shutdown(context.Background()); _ = publisher.Close() }()
	supervise.Go("playbackruntime.linear.bridge-watch", func() {
		select {
		case <-publisher.Failed():
			cancel()
		case <-ctx.Done():
		}
	})
	output, e := bridge.AttachOutput(publisher)
	if e != nil {
		return e
	}
	reservation, e = decoder.ReserveEndpoints(v4, v6)
	if e != nil {
		return e
	}
	e = decoder.RunLinearHLS(ctx, l.owner.probeSupervisor, w.PlaybackID+"-"+strconv.FormatInt(w.ProducerOrdinal, 10), l.config.FFmpeg, bridge.URL(), output, plan, reservation, nil, l.config.DecoderLibraries...)
	if e == nil {
		finish, done := context.WithTimeout(ctx, 5*time.Second)
		e = publisher.Finish(finish)
		done()
	}
	e = errors.Join(e, bridge.Shutdown(context.Background()), publisher.Result())
	if e != nil {
		return e
	}
	// Read-rate burst intentionally prepares ahead. Do not transition merely
	// because FFmpeg reached the planned end before its scheduled wall boundary.
	if !waitLinear(ctx, time.Until(time.UnixMilli(w.Selection.EntryEndMS))) {
		return ctx.Err()
	}
	return nil
}
func (l *LinearRuntime) entry(id string) *linearEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[id]
}
func (l *LinearRuntime) Snapshot(id string) playback.LinearMedia {
	result := playback.LinearMedia{Gaps: []playback.LinearInterval{}, State: "preparing"}
	e := l.entry(id)
	if e == nil {
		return result
	}
	e.mu.Lock()
	buffer, generation, producer, token, state, code, logo, engine := e.buffer, e.generation, e.producer, e.token, e.state, e.code, e.selection.LogoItemID, e.engine
	e.mu.Unlock()
	result.Engine = engine
	if buffer == nil {
		return result
	}
	w := buffer.Window()
	result.BufferGeneration = strconv.FormatInt(generation, 10)
	if producer > 0 {
		result.PresentationGeneration = strconv.FormatInt(generation, 10)
	}
	result.OriginMS = w.OriginMS
	result.WindowRevision = strconv.FormatInt(w.Revision, 10)
	result.WindowStartUS = strconv.FormatInt(w.StartUS, 10)
	result.WindowEndUS = strconv.FormatInt(w.EndUS, 10)
	result.LiveEdgeUS = strconv.FormatInt(w.LiveUS, 10)
	result.State = state
	result.ErrorCode = code
	if w.Ready {
		result.StreamURL = fmt.Sprintf("/v1/media/linear/%s/%d/%s/index.m3u8", id, generation, token)
	}
	if logo != "" {
		result.LogoURL = fmt.Sprintf("/v1/media/linear/%s/%d/%s/logo", id, generation, token)
	}
	for _, gap := range w.Gaps {
		result.Gaps = append(result.Gaps, playback.LinearInterval{StartUS: strconv.FormatInt(gap.StartUS, 10), EndUS: strconv.FormatInt(gap.EndUS, 10)})
	}
	result.Clocks = []playback.LinearClock{}
	for _, c := range buffer.Clocks() {
		result.Clocks = append(result.Clocks, playback.LinearClock{StartUS: strconv.FormatInt(c.StartUS, 10), EndUS: strconv.FormatInt(c.EndUS, 10), OffsetUS: strconv.FormatInt(c.OffsetUS, 10)})
	}
	return result
}
func (l *LinearRuntime) ResolveSeek(id, generation, kind string, raw *string) (string, error) {
	e := l.entry(id)
	if e == nil {
		return "", channelFault("window_expired", 410)
	}
	e.mu.Lock()
	buffer, gen := e.buffer, e.generation
	e.mu.Unlock()
	if buffer == nil || generation != strconv.FormatInt(gen, 10) {
		return "", channelFault("window_expired", 410)
	}
	var position *int64
	if kind == "position" {
		if raw == nil {
			return "", channelFault("invalid_seek", 400)
		}
		v, err := strconv.ParseInt(*raw, 10, 64)
		if err != nil || strconv.FormatInt(v, 10) != *raw {
			return "", channelFault("invalid_seek", 400)
		}
		position = &v
	} else if kind != "live" {
		return "", channelFault("invalid_seek", 400)
	}
	target, err := buffer.Resolve(position)
	if errors.Is(err, linearbuffer.ErrGap) {
		return "", channelFault("media_gap", 409)
	}
	if err != nil {
		return "", channelFault("window_expired", 410)
	}
	return strconv.FormatInt(target, 10), nil
}
func (l *LinearRuntime) SelectionAt(id, generation, position string) (*playback.LinearSelection, error) {
	e := l.entry(id)
	if e == nil {
		return nil, channelFault("window_expired", 410)
	}
	n, err := strconv.ParseInt(position, 10, 64)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	b, gen := e.buffer, e.generation
	e.mu.Unlock()
	if b == nil || generation != strconv.FormatInt(gen, 10) {
		return nil, channelFault("window_expired", 410)
	}
	producer, err := b.ProducerAt(n)
	if err != nil {
		return nil, channelFault("window_expired", 410)
	}
	e.mu.Lock()
	selection, ok := e.selections[producer]
	e.mu.Unlock()
	if !ok {
		return nil, channelFault("window_expired", 410)
	}
	return &selection, nil
}
func (l *LinearRuntime) mediaEntry(id, generation, token string) (*linearEntry, error) {
	e := l.entry(id)
	if e == nil {
		return nil, channelFault("media_generation_expired", 410)
	}
	e.mu.Lock()
	valid := e.buffer != nil && generation == strconv.FormatInt(e.generation, 10) && subtle.ConstantTimeCompare([]byte(token), []byte(e.token)) == 1
	e.mu.Unlock()
	if !valid {
		return nil, identity.ErrUnauthorized
	}
	return e, nil
}

// MediaGrant rechecks durable family/lease authority; URL possession alone never
// survives sign-out, permission revocation or buffer generation replacement.
func (l *LinearRuntime) MediaGrant(ctx context.Context, id, generation, token string) (identity.Principal, playback.LinearSelection, error) {
	e, err := l.mediaEntry(id, generation, token)
	if err != nil {
		return identity.Principal{}, playback.LinearSelection{}, err
	}
	e.mu.Lock()
	gen, selection := e.generation, e.selection
	e.mu.Unlock()
	p, err := l.owner.Linear.authority.AuthorizeLinearMedia(ctx, id, gen, &selection)
	return p, selection, err
}

var playlistStart = regexp.MustCompile(`^at=([0-9]{1,13})$`)

func (l *LinearRuntime) ServeMedia(w http.ResponseWriter, r *http.Request, id, generation, token, name string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	e, err := l.mediaEntry(id, generation, token)
	if err != nil {
		http.Error(w, "media expired", 410)
		return
	}
	select {
	case e.requests <- struct{}{}:
		defer func() { <-e.requests }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "media busy", 429)
		return
	}
	// The only query is a playlist's start (at=<milliseconds from the origin>),
	// for a player that reopens a live playlist where it wants to be.
	var from *int64
	if r.URL.RawQuery != "" {
		match := playlistStart.FindStringSubmatch(r.URL.RawQuery)
		if match == nil || name != "index.m3u8" && name != "stream.m3u8" {
			http.NotFound(w, r)
			return
		}
		ms, _ := strconv.ParseInt(match[1], 10, 64)
		us := ms * 1000
		from = &us
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	e.mu.Lock()
	buffer, gen, captions := e.buffer, e.generation, e.captions
	captions.Renditions = append([]linearinput.CaptionRendition(nil), captions.Renditions...)
	e.mu.Unlock()
	authorize := func(selection *playback.LinearSelection) error {
		_, err := l.owner.Linear.authority.AuthorizeLinearMedia(r.Context(), id, gen, selection)
		return err
	}
	if err = authorize(nil); err != nil {
		http.Error(w, "media authority expired", 403)
		return
	}
	if name == "index.m3u8" || name == "stream.m3u8" {
		// Playlist access checks every retained programme as well as the current one.
		for _, producer := range buffer.Producers() {
			e.mu.Lock()
			selection, ok := e.selections[producer]
			e.mu.Unlock()
			if !ok || authorize(&selection) != nil {
				http.Error(w, "retained media unavailable", 403)
				return
			}
		}
		data, err := buffer.PlaylistFrom(from)
		if err != nil {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "media preparing", 503)
			return
		}
		if authorize(nil) != nil {
			http.Error(w, "media authority expired", 403)
			return
		}
		master, hasCaptions := captionMaster(captions, r.URL.RawQuery)
		if name == "index.m3u8" && hasCaptions {
			data = master
		} else if name == "stream.m3u8" && !hasCaptions {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == "GET" {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
			_, _ = w.Write(data)
		}
		return
	}
	reader, err := buffer.Open(name)
	if err != nil {
		http.Error(w, "timeshift window expired", 410)
		return
	}
	defer reader.Close()
	e.mu.Lock()
	selection, ok := e.selections[reader.Producer]
	e.mu.Unlock()
	if !ok || authorize(&selection) != nil {
		http.Error(w, "media authority expired", 403)
		return
	}
	w.Header().Set("Content-Type", reader.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(reader.Size, 10))
	if r.Method == "HEAD" {
		return
	}
	// HLS uses complete immutable objects. Each disk read is fenced again before
	// emission; no ServeFile fallback can bypass current authorization.
	chunk := make([]byte, 64<<10)
	for {
		if authorize(&selection) != nil {
			return
		}
		n, err := reader.File.Read(chunk)
		if n > 0 {
			if authorize(&selection) != nil {
				return
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, e := w.Write(chunk[:n]); e != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return
			}
			break
		}
	}
}

func linearSelectionKey(s playback.LinearSelection) string {
	key := s.Reference.Kind + ":" + s.Reference.Generation + ":" + s.SourceFence
	if s.Reference.Kind == "library-channel" {
		key += ":" + s.EntryID
	}
	return key
}

// A busy database does not invalidate a completed segment. Retry its authority
// check within a small upload budget; explicit permission/source refusals remain immediate.
func (l *LinearRuntime) checkProducerWithPatience(ctx context.Context, w playback.LinearWork) error {
	until := time.Now().Add(3 * time.Second)
	for {
		check, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		e := l.owner.Linear.authority.CheckLinearProducer(check, w)
		cancel()
		stop, _ := linearMonitorVerdict(false, e, 0)
		if e == nil || stop || ctx.Err() != nil || time.Now().After(until) {
			return e
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// watchLinearWork follows this runtime's durable inputs; clock-only expiry
// still runs through worker.SafetyTick. Unrelated telemetry is not work.
func watchLinearWork(signal *workerloop.Signal) func() {
	return dbwork.WakeOnTables(signal,
		"playback_channel_*", "lc_*", "live_source_*", "live_channel_*", "playback_sessions", "playback_v1_sessions", "playback_owner_policy",
		"live_allocations", "live_sources", "live_source_settings", "live_channel_versions",
		"live_programmes", "live_programme_metadata", "catalog_entities", "catalog_libraries",
		"library_sources", "library_revisions", "server_settings", "console_settings_revision",
		"authorization_*", "accounts", "direct_profiles", "direct_memberships",
		"direct_profile_trust", "restrictions", "hosted_*", "identity_devices",
		"profile_*", "media_process_*",
	)
}
