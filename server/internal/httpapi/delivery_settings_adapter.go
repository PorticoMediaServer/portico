package httpapi

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

// The registry is the single authority for transcoding policy: the delivery
// service reads it through this adapter, and the capacity report reads the
// delivery service's real probes back through playbackProbes.
var _ playback.DeliverySettings = (*RegistryDeliverySettings)(nil)

// Delivery maps the owner's transcoding settings onto the delivery seam. The
// server-wide remote bitrate limit is a connectivity registry row, and it
// arrives here as MaxVideoBitrateBPS; the height clamp is still not a registry
// field, so it stays at zero (no clamp) and HDR stays allowed, with tone mapping
// deciding what happens to it.
func (a *RegistryDeliverySettings) Delivery() playback.DeliveryConfiguration {
	s, bitrate := a.current()
	return deliveryFromSettings(s, bitrate)
}

func deliveryFromSettings(s telemetry.TranscodeSettings, bitrate int) playback.DeliveryConfiguration {
	return playback.DeliveryConfiguration{
		ConversionMode: s.ConversionMode, OutputFormat: s.OutputFormat, DownloadMaxQuality: s.DownloadMaxQuality, MaxMusicQuality: s.MaxMusicQuality,
		MaxVideoBitrateBPS:     bitrate,
		SessionStorageMiB:      s.SessionStorageMiB,
		MemoryCap:              s.MemoryCap,
		TranscodeLocation:      s.TranscodeLocation,
		HardwareBackend:        s.HardwareBackend,
		HardwareDevice:         s.HardwareDevice,
		HardwareSelection:      s.HardwareSelection,
		SoftwareDecode:         !s.HardwareDecode,
		ToneMapping:            s.HDRToneMapping,
		ToneMapAlgorithm:       s.HDRToneMappingAlgorithm,
		SoftwarePreset:         s.X264Preset,
		PlanningPolicy:         s.PlanningPolicy,
		ThrottleBufferSeconds:  s.ThrottleBufferMs / 1000,
		PlayedRetentionSeconds: s.PlayedRetentionMs / 1000,
		RemuxEnabled:           s.DirectStreamRemux,
		AllowHDR:               true,
		MaxConversions:         s.MaxConcurrentSessions,
		MaxHardwareConversions: s.MaxHardwareSessions,
		MaxSoftwareConversions: s.MaxSoftwareSessions,
	}
}

// DeliveryTx reads actual stored settings with the caller's exact transaction;
// it never refreshes the diagnostic cache or invokes tool validation.
func (a *RegistryDeliverySettings) DeliveryTx(ctx context.Context, tx *sql.Tx) (playback.DeliveryConfiguration, error) {
	if a == nil || a.Console == nil {
		return playback.DefaultDeliveryConfiguration(), nil
	}
	authorize := a.Authorize
	if authorize == nil {
		authorize = operations.AllowServerScope
	}
	document, err := a.Console.SettingsTx(ctx, tx, authorize)
	if err != nil {
		return playback.DeliveryConfiguration{}, err
	}
	return deliveryFromSettings(transcodeSettings(document.Effective), document.Effective.RemoteBitrateLimitKbps*1000), nil
}

// playbackProbes publishes the delivery service's encoder probes to the
// capacity report, so the admin page shows what this host can really do.
type playbackProbes struct{ playback *playback.Service }

func (p playbackProbes) Probe(ctx context.Context, device string) []telemetry.HardwareProbe {
	if p.playback == nil {
		return telemetry.SoftwareOnlyProbes{}.Probe(ctx, device)
	}
	out := []telemetry.HardwareProbe{}
	for _, probe := range p.playback.HardwareReport(ctx) {
		detail := probe.Reason
		if probe.Encoder != "" {
			detail = strings.TrimSpace(detail + " (" + probe.Encoder + ")")
		}
		out = append(out, telemetry.HardwareProbe{Backend: string(probe.Backend), Supported: probe.Available, Detail: detail, Device: probe.Device, Encoder: probe.Encoder})
	}
	return out
}

// Facts adds the adapters that exist and the measured per-format grid to the
// backend probes. Every value comes from a probe that ran on this host; an
// unprobed cell stays false, which reads as the processor doing that job.
func (p playbackProbes) Facts(ctx context.Context, device, configured string) decoder.HardwareFacts {
	if p.playback == nil {
		return decoder.HardwareFacts{Devices: []decoder.HardwareFactDevice{}, Formats: []decoder.HardwareFactFormat{}}
	}
	selected := p.playback.HardwareSelection(ctx)
	return decoder.ComposeHardwareFacts(p.playback.HardwareDevices(ctx), p.playback.HardwareReport(ctx), configured, &selected)
}

// RegistryDeliverySettings answers delivery questions from the owner settings
// registry through a short-lived snapshot. A settings save calls Refresh so it
// takes effect on the next operation without a restart, which is what the
// registry's application mode promises.
type RegistryDeliverySettings struct {
	Console *operations.Store
	// Authorize is the console authority used for the read. Delivery reads are
	// server-scoped, not viewer-scoped, so this is the server's own authority.
	Authorize operations.Authorize

	// Ordinary diagnostics use this short-lived cache. Playback accepting
	// transactions use DeliveryTx and never use the cache as configuration proof.
	mu         sync.RWMutex
	snapshot   telemetry.TranscodeSettings
	bitrateBPS int
	loaded     bool
	loadedAt   time.Time
	refreshing bool
}

const deliverySettingsTTL = 5 * time.Second

// Refresh reads the effective settings now. Call it at startup and after an
// owner applies settings; diagnostics read the snapshot while accepting playback
// reads actual stored settings with DeliveryTx.
func (a *RegistryDeliverySettings) Refresh(ctx context.Context) {
	if a == nil || a.Console == nil {
		return
	}
	authorize := a.Authorize
	if authorize == nil {
		authorize = operations.AllowServerScope
	}
	document, e := a.Console.Settings(ctx, authorize)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshing = false
	if e != nil {
		return
	}
	decoder.SetBackgroundConversionLimit(document.Effective.MaxBackgroundSessions)
	a.snapshot, a.bitrateBPS, a.loaded, a.loadedAt = transcodeSettings(document.Effective), document.Effective.RemoteBitrateLimitKbps*1000, true, time.Now()
}

// current answers from the snapshot and, when it is missing or older than the
// TTL, starts one background refresh. The remote bitrate ceiling is in bits
// per second; zero means no ceiling. The per-member cap is applied separately
// at admission, and the lower of the two wins.
func (a *RegistryDeliverySettings) current() (telemetry.TranscodeSettings, int) {
	a.mu.RLock()
	loaded, stale, refreshing, snapshot, bitrate := a.loaded, time.Since(a.loadedAt) > deliverySettingsTTL, a.refreshing, a.snapshot, a.bitrateBPS
	a.mu.RUnlock()
	if a.Console != nil && (!loaded || stale) && !refreshing {
		a.mu.Lock()
		if !a.refreshing {
			a.refreshing = true
			supervise.Go("delivery.settings-refresh", func() { a.Refresh(context.Background()) })
		}
		a.mu.Unlock()
	}
	if !loaded {
		return transcodeSettings(operations.DefaultSettings()), 0
	}
	return snapshot, bitrate
}

// Transcoding returns the effective transcoding policy, or the registry
// defaults when the document cannot be read. Falling back to defaults rather
// than failing keeps playback working when the console store is briefly busy.
func (a *RegistryDeliverySettings) Transcoding(_ context.Context) telemetry.TranscodeSettings {
	if a == nil {
		return transcodeSettings(operations.DefaultSettings())
	}
	settings, _ := a.current()
	return settings
}
