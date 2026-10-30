package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// telemetryRoutes publishes the owner Server overview: transcode capacity,
// host and GPU telemetry with history, the needs-attention list, and playback
// history. Every route is owner-only and answers from memory or from one
// bounded query, so a dashboard polling them costs the server almost nothing.
func (d Dependencies) telemetryRoutes(mux *http.ServeMux) {
	if d.Console == nil || d.DB == nil {
		return
	}
	service := &telemetryService{d: d, capacity: d.capacityComposer()}
	d.dashboardReportRoute(mux, service)
	if d.TelemetryContext != nil {
		service.collector()
	}
	if d.Measurements != nil {
		// The resources panel shows the collector's last sample. It reads what the
		// collector already has and never starts one, so a panel read stays cheap.
		d.Measurements.Host = func() telemetry.Sample {
			if service.started() {
				return service.collector().Latest()
			}
			return telemetry.Sample{}
		}
	}

	service.register(mux, "GET /v1/admin/transcode/capacity", nil, func(r *http.Request, a operations.Authorize) (any, error) {
		return service.capacityReport(r.Context(), a)
	})
	service.registerHardwareTest(mux)
	service.register(mux, "GET /v1/admin/telemetry", []string{"window", "timeZone", "until"}, func(r *http.Request, a operations.Authorize) (any, error) {
		if e := validateReportContext(r.URL.Query().Get("timeZone"), r.URL.Query().Get("until")); e != nil {
			return nil, e
		}
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "10m"
		}
		reading, ok := service.collector().Read(window, r.URL.Query().Get("timeZone"), r.URL.Query().Get("until"))
		if !ok {
			return nil, operations.ErrInvalid
		}
		return reading, nil
	})
	service.register(mux, "GET /v1/admin/telemetry/now", nil, func(r *http.Request, a operations.Authorize) (any, error) {
		return service.collector().Latest(), nil
	})
	service.register(mux, "GET /v1/admin/attention", nil, func(r *http.Request, a operations.Authorize) (any, error) {
		items, e := service.attention(r.Context(), a)
		if e != nil {
			return nil, e
		}
		return map[string]any{"items": items}, nil
	})
	service.register(mux, "GET /v1/admin/playback/history", []string{"period", "limit", "cursor"}, func(r *http.Request, a operations.Authorize) (any, error) {
		period, limit, e := historyQuery(r)
		if e != nil {
			return nil, e
		}
		return d.Console.PlaybackHistory(r.Context(), a, period, r.URL.Query().Get("cursor"), limit)
	})
	d.httpHandle(mux, "GET /v1/admin/playback/history.csv", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		p, e := d.owner(r.WithContext(ctx))
		if e != nil {
			operationsAuthFailure(w, e)
			return
		}
		if e = consoleQuery(r, "period"); e != nil {
			consoleError(w, e)
			return
		}
		period, _, e := historyQuery(r)
		if e != nil {
			consoleError(w, e)
			return
		}
		auth := d.consoleAuthority(p, true)
		body, e := d.Console.PlaybackHistoryCSV(ctx, auth, period)
		if e == nil {
			e = d.ConsoleCheck(ctx, auth)
		}
		if e != nil {
			consoleError(w, e)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="playback-history-`+period+`.csv"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	})
}

func historyQuery(r *http.Request) (string, int, error) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	if _, ok := operations.PlaybackHistoryPeriods[period]; !ok {
		return "", 0, operations.ErrInvalid
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 200 {
			return "", 0, operations.ErrInvalid
		}
		limit = n
	}
	return period, limit, nil
}

// capacityComposer builds a transcode capacity composer for this server. Each
// caller holds its own, so one surface's reporter cache never stalls another's.
func (d Dependencies) capacityComposer() *telemetry.Capacity {
	ffmpeg, ffprobe := decoder.ResolveTools()
	cache := d.CacheDirectory
	if d.StorageLayout != nil {
		cache = d.StorageLayout.Transcode
	}
	if cache != "" {
		if absolute, err := filepath.Abs(cache); err == nil {
			cache = absolute
		}
	}
	capacity := &telemetry.Capacity{
		FFmpeg:          ffmpeg,
		FFprobe:         ffprobe,
		State:           cache,
		Probes:          playbackProbes{d.Playback},
		MemoryAvailable: d.Playback.MemoryOutputAvailable,
	}
	if d.StorageLayout != nil {
		capacity.Directory = &d.StorageLayout.Transcode
	}
	return capacity
}

func (d Dependencies) stateDirectory() string {
	if d.Measurements == nil {
		return ""
	}
	return d.Measurements.StateDirectory
}

// telemetryService owns the sampling goroutine and the short caches these
// routes share. One instance exists per built handler.
type telemetryService struct {
	d        Dependencies
	capacity *telemetry.Capacity

	start     sync.Once
	running   atomic.Bool
	collected *telemetry.Collector

	mu           sync.Mutex
	attentionAt  time.Time
	attentionOut []telemetry.Item
}

// attentionCache is short enough that an owner acting on an item sees it clear,
// and long enough that an open overview does not requery on every poll.
const attentionCache = 30 * time.Second

// collector starts sampling on the first telemetry read rather than at startup,
// so a server nobody is watching runs no reporters at all.
func (s *telemetryService) collector() *telemetry.Collector {
	s.start.Do(func() {
		s.collected = telemetry.New(telemetry.Options{Sampler: dashboardSampler{host: telemetry.HostSampler(s.d.stateDirectory()), d: s.d}, DB: s.d.DB})
		s.running.Store(true)
		ctx := s.d.TelemetryContext
		if ctx == nil {
			ctx = context.Background()
		}
		supervise.Supervise(ctx, "telemetry.collector", s.collected.Run)
	})
	return s.collected
}
func (s *telemetryService) started() bool { return s.running.Load() }

func (s *telemetryService) capacityReport(ctx context.Context, a operations.Authorize) (telemetry.Report, error) {
	document, e := s.d.Console.Settings(ctx, a)
	if e != nil {
		return telemetry.Report{}, e
	}
	sessions := telemetry.SessionCounts{}
	if s.d.Playback != nil {
		if diagnostics, e := s.d.Playback.DeliveryDiagnostics(ctx); e == nil {
			sessions.Active = diagnostics.ActiveConversionSessions
		}
	}
	return s.capacity.Report(ctx, transcodeSettings(document.Effective), sessions), nil
}

// transcodeSettings is the single mapping between the owner settings registry
// and the telemetry package's own view of it.
func transcodeSettings(v operations.Settings) telemetry.TranscodeSettings {
	return telemetry.TranscodeSettings{
		ConversionMode: v.ConversionMode, OutputFormat: v.OutputFormat, DownloadMaxQuality: v.DownloadMaxQuality, MaxMusicQuality: v.MusicAwayQuality,
		Enabled:                 v.TranscodingEnabled,
		HardwareBackend:         v.HardwareBackend,
		HardwareDevice:          v.HardwareDevice,
		HardwareSelection:       v.HardwareSelection,
		HardwareDecode:          v.HardwareDecode,
		HDRToneMapping:          v.HDRToneMapping,
		HDRToneMappingAlgorithm: v.HDRToneMappingAlgorithm,
		X264Preset:              v.X264Preset,
		DirectStreamRemux:       v.DirectStreamRemux,
		PlanningPolicy:          v.PlanningPolicy,
		ThrottleBufferMs:        v.ThrottleBufferMs,
		PlayedRetentionMs:       v.PlayedRetentionMs,
		SessionStorageMiB:       v.SessionStorageMiB,
		TemporaryDirectory:      v.TemporaryDirectory,
		MemoryCap:               v.MemoryCap,
		TranscodeLocation:       v.TranscodeLocation,
		MaxConcurrentSessions:   v.MaxConcurrentSessions,
		MaxHardwareSessions:     v.MaxHardwareSessions,
		MaxSoftwareSessions:     v.MaxSoftwareSessions,
		MaxBackgroundSessions:   v.MaxBackgroundSessions,
	}
}

// attention gathers the database facts, the volume and dependency facts, and
// the certificate expiry, then composes the list. The whole composition is
// cached briefly because it touches several subsystems at once.
func (s *telemetryService) attention(ctx context.Context, a operations.Authorize) ([]telemetry.Item, error) {
	s.mu.Lock()
	if s.attentionOut != nil && time.Since(s.attentionAt) < attentionCache {
		out := s.attentionOut
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()

	facts, e := s.d.Console.AttentionFacts(ctx, a)
	if e != nil {
		return nil, e
	}
	report, e := s.capacityReport(ctx, a)
	if e != nil {
		return nil, e
	}
	facts.TranscodingEnabled = report.Enabled
	facts.TemporaryDirectory = report.TemporaryDirectory
	facts.FFmpegOK = report.Dependencies["ffmpeg"].OK
	if free, total, ok := telemetry.VolumeUsage(s.d.stateDirectory()); ok && total > 0 {
		ratio := float64(free) / float64(total)
		facts.StorageFreeRatio, facts.StorageFreeBytes = &ratio, &free
	}
	facts.CertificateExpiry = s.certificateExpiry(ctx)
	items := telemetry.Attention(facts)
	s.mu.Lock()
	s.attentionAt, s.attentionOut = time.Now(), items
	s.mu.Unlock()
	return items, nil
}

// certificateExpiry reads the existing networking status rather than opening
// certificate material here. A server with no certificate raises nothing.
func (s *telemetryService) certificateExpiry(ctx context.Context) *telemetry.CertificateExpiry {
	if s.d.Networking == nil {
		return nil
	}
	manager := s.d.Networking.Certificates()
	if manager == nil {
		return nil
	}
	status, e := manager.Status(ctx)
	if e != nil || status.NotAfter == nil {
		return nil
	}
	days := int(math.Floor(time.Until(*status.NotAfter).Hours() / 24))
	return &telemetry.CertificateExpiry{DaysRemaining: days, DNSName: status.DNSName}
}

// register mirrors the console envelope so one client transport reads every
// owner surface, including the authorization recheck after the work is done.
func (s *telemetryService) register(mux *http.ServeMux, pattern string, query []string, fn func(*http.Request, operations.Authorize) (any, error)) {
	slots := make(chan struct{}, 4)
	d := s.d
	d.projectedHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		p, e := d.owner(r)
		if e != nil {
			consoleError(w, e)
			return
		}
		if e = consoleQuery(r, query...); e != nil {
			consoleError(w, e)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			consoleError(w, operations.ErrCapacity)
			return
		}
		auth := d.consoleAuthority(p, true)
		out, e := fn(r, auth)
		if e != nil {
			consoleError(w, e)
			return
		}
		if e = d.ConsoleCheck(ctx, auth); e != nil {
			consoleError(w, e)
			return
		}
		consoleWrite(w, 200, map[string]any{"scope": telemetryScope(d, p), "data": out})
	})
}

// hardwareTestSlots allows one deliberate hardware test at a time: it starts a
// probe child process per adapter and backend, and a second owner tapping the
// button must wait rather than stack encoders.
var hardwareTestSlots = make(chan struct{}, 1)

// registerHardwareTest probes every adapter and backend again now and answers
// with the same capacity document the read route serves, so Server › Streaming ›
// Hardware redraws from one write. It is the only route that starts probes
// synchronously, so it has its own bound instead of the seven-second read.
func (s *telemetryService) registerHardwareTest(mux *http.ServeMux) {
	d := s.d
	d.projectedHandle(mux, "POST /v1/admin/transcode/capacity/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		r = r.WithContext(ctx)
		p, e := d.owner(r)
		if e != nil {
			consoleError(w, e)
			return
		}
		if e = consoleQuery(r); e != nil {
			consoleError(w, e)
			return
		}
		select {
		case hardwareTestSlots <- struct{}{}:
			defer func() { <-hardwareTestSlots }()
		default:
			w.Header().Set("Retry-After", "1")
			consoleError(w, operations.ErrCapacity)
			return
		}
		auth := d.consoleAuthority(p, true)
		if d.Playback != nil {
			d.Playback.HardwareTest(ctx)
		}
		s.capacity.Invalidate()
		report, e := s.capacityReport(ctx, auth)
		if e != nil {
			consoleError(w, e)
			return
		}
		if e = d.ConsoleCheck(ctx, auth); e != nil {
			consoleError(w, e)
			return
		}
		consoleWrite(w, 200, map[string]any{"scope": telemetryScope(d, p), "data": report})
	})
}

func telemetryScope(d Dependencies, p identity.Principal) map[string]string {
	return map[string]string{"serverId": d.Identity.ID(), "viewerFence": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}
}
